import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { ReactNode, SelectHTMLAttributes } from 'react';
import { api, type TransactionRow, type ImportHistoryEntry, type ImportResult, type Account } from '../api/client';
import CsvUploader from '../components/CsvUploader';
import {
  PageHeader, Section, Field, Chip,
  LoadingState, ErrorState, EmptyState, SectionBoundary,
} from '../components/ui';
import { fmtMoney, fmtNum, fmtQty, fmtDate, fmtDateTime, fmtMonth, labelFor } from '../utils/format';

const TXN_TYPES = [
  'buy', 'sell', 'dividend', 'deposit', 'withdrawal', 'interest', 'fee', 'tax',
  'transfer', 'transfer_out', 'savings_plan', 'cash_transfer_in', 'cash_transfer_out',
];

// Cash-flow categories user can override transactions with. Must match
// internal/cashflow.AllCategories() — backend rejects unknown values.
const CASHFLOW_CATEGORIES = [
  'salary', 'interest', 'refund',
  'housing', 'utilities', 'insurance', 'subscriptions',
  'groceries', 'dining', 'transport', 'health',
  'entertainment', 'shopping', 'tax',
  'investment', 'internal', 'asset_purchase', 'loan_payment', 'loan_principal', 'other',
];
const categoryLabel = (c: string) => (c === 'internal' ? 'Internal transfer' : labelFor(c));

// Cash-transaction types — only these get the category override, and only when the
// row moves cash: a transfer that carries a security is not a cashflow item.
const CASH_TXN_TYPES = new Set(['deposit', 'withdrawal', 'interest', 'transfer', 'transfer_out', 'cash_transfer_in', 'cash_transfer_out', 'fee']);
const hasCategory = (t: TransactionRow) => CASH_TXN_TYPES.has(t.type) && !t.security_isin;

// Imports store `amount`, `fee` and `tax` as magnitudes; the direction is carried by the type.
// These helpers mirror txnSignedCashDelta in internal/handler/fx_cash.go so the amount shown is
// the cash the row actually moved, and its sign always agrees with the type chip next to it.
const INFLOW_TYPES = new Set(['deposit', 'interest', 'sell', 'dividend', 'cash_transfer_in']);
const OUTFLOW_TYPES = new Set(['buy', 'savings_plan', 'withdrawal', 'fee', 'tax', 'cash_transfer_out']);

function cashDirection(t: TransactionRow): 1 | -1 | 0 {
  if (INFLOW_TYPES.has(t.type)) return 1;
  if (OUTFLOW_TYPES.has(t.type)) return -1;
  if (t.type === 'transfer' && !t.security_isin) return -1;
  return 0; // in-kind movements: no cash effect
}

/**
 * Net cash effect: ±|amount| − |fee| − |tax|, or null for an in-kind security movement.
 * Worked examples: a 20,00 € card payment with a 0,50 € fee → −20,50 €; a 600,00 € crypto
 * buy with a 1,49 € fee → −601,49 €; a 100,00 USD dividend with 15,00 USD tax → +85,00 USD.
 */
function cashDelta(t: TransactionRow): number | null {
  const dir = cashDirection(t);
  if (dir === 0) return null;
  return dir * Math.abs(t.amount) - Math.abs(t.fee ?? 0) - Math.abs(t.tax ?? 0);
}

type Unit = 'share' | 'unit';
const unitWord = (q: number, unit: Unit) => `${unit}${q === 1 ? '' : 's'}`;

interface AmountView {
  text: string;
  className: string;
  /** Second line: what the amount includes ("incl. 0,50 € fee") or what the moved shares are worth. */
  detail?: string;
}

/** Amount text and colour, both derived from the same signed cash value. */
function amountView(t: TransactionRow, unit: Unit): AmountView {
  const delta = cashDelta(t);
  if (delta === null) {
    // Shares move in or out; cash doesn't. Show the quantity and its direction.
    const arrow = t.type === 'transfer_out' ? '→ out' : '← in';
    const q = Math.abs(t.quantity ?? 0);
    const worth = Math.abs(t.amount);
    if (q > 0) {
      return {
        text: `${fmtQty(q)} ${unitWord(q, unit)} ${arrow}`,
        className: 'text-ink',
        detail: worth > 0 ? `worth ${fmtMoney(worth, t.currency, { cents: true })}` : undefined,
      };
    }
    return { text: worth === 0 ? '—' : `${fmtMoney(worth, t.currency, { cents: true })} ${arrow}`, className: 'text-ink' };
  }
  const fee = Math.abs(t.fee ?? 0);
  const tax = Math.abs(t.tax ?? 0);
  const parts: string[] = [];
  if (fee > 0) parts.push(`${fmtMoney(fee, t.currency, { cents: true })} fee`);
  if (tax > 0) parts.push(`${fmtMoney(tax, t.currency, { cents: true })} tax`);
  return {
    text: fmtMoney(delta, t.currency, { cents: true, sign: true }),
    className: delta >= 0.005 ? 'text-sage' : 'text-ink',
    // Outflows pay fees and tax on top ("incl."); inflows arrive net of them ("after").
    detail: parts.length > 0 ? `${cashDirection(t) < 0 ? 'incl.' : 'after'} ${parts.join(' · ')}` : undefined,
  };
}

const displayName = (t: TransactionRow) => t.counterparty || t.reference || t.security_isin || t.account_name;

/** "5,123 shares at 97,60 €" for security rows ("units" for crypto), otherwise null. */
function quantityLine(t: TransactionRow, unit: Unit): string | null {
  if (!t.security_isin || t.quantity == null) return null;
  const q = Math.abs(t.quantity);
  if (q === 0) return null;
  const qty = `${fmtQty(q)} ${unitWord(q, unit)}`;
  return t.price != null && t.price > 0 ? `${qty} at ${fmtMoney(t.price, t.currency, { cents: true })}` : qty;
}

const pad = (n: number) => String(n).padStart(2, '0');
const monthDate = (ym: string) => {
  const [y, m] = ym.split('-');
  return new Date(Number(y), Number(m) - 1, 1);
};

/** Months from the current one back to `firstDate`'s month ('YYYY-MM', newest first); 36 when unknown. */
function monthsBackTo(firstDate: string | undefined, now = new Date()): string[] {
  const m = /^(\d{4})-(\d{2})/.exec(firstDate ?? '');
  const count = m
    ? (now.getFullYear() - Number(m[1])) * 12 + (now.getMonth() + 1 - Number(m[2])) + 1
    : 36;
  return Array.from({ length: Math.min(Math.max(count, 1), 1200) }, (_, i) => {
    const d = new Date(now.getFullYear(), now.getMonth() - i, 1);
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}`;
  });
}

/** Crypto and metals are counted in units, not shares. */
function unitFor(t: TransactionRow, accountType: string | undefined): Unit {
  if (t.security_isin?.startsWith('CRYPTO:')) return 'unit';
  return accountType === 'crypto' || accountType === 'precious_metals' ? 'unit' : 'share';
}

const HISTORY_PAGE = 50;

/** Compact native select with a chevron and an accessible name; keeps the global focus outline. */
function CompactSelect({ label, dense = false, className = '', children, ...select }: { label: string; dense?: boolean; className?: string; children: ReactNode } & Omit<SelectHTMLAttributes<HTMLSelectElement>, 'className'>) {
  return (
    <div className={`relative min-w-0 ${className}`}>
      <select
        {...select}
        aria-label={label}
        className={`w-full appearance-none truncate rounded-[6px] border border-field bg-parchment-deep text-ink cursor-pointer min-h-[44px] text-[16px] ${
          dense ? 'pl-2.5 pr-8 md:min-h-[32px] md:text-[13px]' : 'pl-2 pr-6 md:pl-3 md:pr-9 md:text-[14px]'
        }`}
      >
        {children}
      </select>
      <svg className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 w-4 h-4 text-ink-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M6 9l6 6 6-6" />
      </svg>
    </div>
  );
}

const LIMIT = 25;

export default function Transactions() {
  const [transactions, setTransactions] = useState<TransactionRow[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [accountFilter, setAccountFilter] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  // '' = all dates, 'YYYY-MM' = one month, 'custom' = the From/To fields below.
  const [period, setPeriod] = useState('');
  const [customFrom, setCustomFrom] = useState('');
  const [customTo, setCustomTo] = useState('');
  const [search, setSearch] = useState('');
  const [searchInput, setSearchInput] = useState('');
  const [importHistory, setImportHistory] = useState<ImportHistoryEntry[]>([]);
  const [historyTotal, setHistoryTotal] = useState(0);
  const [showAllHistory, setShowAllHistory] = useState(false);
  // All accounts, inactive ones included: their transactions are listed and counted too.
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [firstDate, setFirstDate] = useState<string | undefined>(undefined);
  const [showUploader, setShowUploader] = useState(false);
  const [importResult, setImportResult] = useState<ImportResult | null>(null);
  const [categoryError, setCategoryError] = useState<string | null>(null);
  const requestSeq = useRef(0);

  const isMonth = /^\d{4}-\d{2}$/.test(period);
  const [dateFrom, dateTo] = useMemo<[string, string]>(() => {
    if (period === 'custom') return [customFrom, customTo];
    if (isMonth) {
      const d = monthDate(period);
      const last = new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate();
      return [`${period}-01`, `${period}-${pad(last)}`];
    }
    return ['', ''];
  }, [period, isMonth, customFrom, customTo]);

  // Back to the month of the first transaction, so every row is reachable.
  const monthOptions = useMemo(() => monthsBackTo(firstDate), [firstDate]);

  const accountsById = useMemo(() => new Map(accounts.map(a => [a.id, a])), [accounts]);
  const activeAccounts = useMemo(() => accounts.filter(a => a.is_active), [accounts]);
  const unitOf = (t: TransactionRow) => unitFor(t, accountsById.get(t.account_id)?.type);

  const loadTransactions = useCallback(async () => {
    const seq = ++requestSeq.current;
    setLoading(true);
    try {
      const res = await api.listTransactions(LIMIT, offset, typeFilter, search, dateFrom, dateTo, accountFilter);
      if (seq !== requestSeq.current) return;
      setTransactions(res.transactions || []);
      setTotal(res.total);
      if (res.first_date) setFirstDate(res.first_date);
      setError(null);
    } catch (e) {
      if (seq !== requestSeq.current) return;
      setError(e instanceof Error ? e.message : 'Failed to load transactions');
    } finally {
      if (seq === requestSeq.current) {
        setLoading(false);
        setLoaded(true);
      }
    }
  }, [offset, typeFilter, search, dateFrom, dateTo, accountFilter]);

  const loadHistory = useCallback(() => {
    api.getImportHistory(HISTORY_PAGE, 0).then(r => {
      const rows = r.history || [];
      setImportHistory(rows);
      setHistoryTotal(r.total ?? rows.length);
    }).catch(() => {});
  }, []);

  const loadMoreHistory = () => {
    api.getImportHistory(HISTORY_PAGE, importHistory.length).then(r => {
      setImportHistory(prev => [...prev, ...(r.history || [])]);
      if (r.total != null) setHistoryTotal(r.total);
    }).catch(() => {});
  };

  useEffect(() => { loadTransactions(); }, [loadTransactions]);
  useEffect(() => { loadHistory(); }, [loadHistory]);
  useEffect(() => { api.listAllAccounts().then(r => setAccounts(r.accounts || [])).catch(() => {}); }, []);

  /** Any filter change returns to the first page. */
  const withReset = <T,>(setter: (v: T) => void) => (v: T) => { setter(v); setOffset(0); };
  const changeAccount = withReset(setAccountFilter);
  const changeType = withReset(setTypeFilter);
  const changePeriod = withReset(setPeriod);
  const changeFrom = withReset(setCustomFrom);
  const changeTo = withReset(setCustomTo);
  const applySearch = () => { setSearch(searchInput.trim()); setOffset(0); };

  const filtersActive = !!(accountFilter || typeFilter || search || dateFrom || dateTo);
  const clearFilters = () => {
    setAccountFilter(''); setTypeFilter(''); setPeriod(''); setCustomFrom(''); setCustomTo('');
    setSearch(''); setSearchInput(''); setOffset(0);
  };

  const filterSummary = (() => {
    const parts: string[] = [];
    const acc = accounts.find(a => a.id === accountFilter);
    if (acc) parts.push(`in ${acc.name}`);
    if (typeFilter) parts.push(`of type “${labelFor(typeFilter)}”`);
    if (isMonth) parts.push(`in ${fmtMonth(monthDate(period))}`);
    else if (dateFrom && dateTo) parts.push(`between ${fmtDate(dateFrom)} and ${fmtDate(dateTo)}`);
    else if (dateFrom) parts.push(`from ${fmtDate(dateFrom)}`);
    else if (dateTo) parts.push(`up to ${fmtDate(dateTo)}`);
    if (search) parts.push(`matching “${search}”`);
    return parts.join(', ');
  })();

  const updateCategory = async (t: TransactionRow, next: string) => {
    setCategoryError(null);
    setTransactions(prev => prev.map(x => x.id === t.id ? { ...x, category: next || null } : x));
    try {
      await api.updateTransactionCategory(t.id, next);
    } catch {
      // Revert on failure — UI is optimistic.
      setTransactions(prev => prev.map(x => x.id === t.id ? { ...x, category: t.category } : x));
      setCategoryError(`The category for ${displayName(t)} could not be saved. It was reset to its previous value.`);
    }
  };

  const categorySelect = (t: TransactionRow, className: string) => (
    <CompactSelect
      dense
      label={`Category for ${displayName(t)}`}
      className={className}
      value={t.category || ''}
      onChange={(e) => updateCategory(t, e.target.value)}
    >
      <option value="">Auto</option>
      {CASHFLOW_CATEGORIES.map(c => (
        <option key={c} value={c}>{categoryLabel(c)}</option>
      ))}
    </CompactSelect>
  );

  // Group the current page by calendar month (rows arrive newest first).
  const groups = useMemo(() => {
    const out: { month: string; rows: TransactionRow[] }[] = [];
    for (const t of transactions) {
      const month = String(t.date).slice(0, 7);
      const last = out[out.length - 1];
      if (last && last.month === month) last.rows.push(t);
      else out.push({ month, rows: [t] });
    }
    return out;
  }, [transactions]);

  const groupLabel = (g: { month: string; rows: TransactionRow[] }) => {
    const count = isMonth
      ? `${fmtNum(total)} ${total === 1 ? 'transaction' : 'transactions'}`
      : `${fmtNum(g.rows.length)} on this page`;
    return `${fmtMonth(monthDate(g.month))} · ${count}`;
  };

  const columnCount = 6;

  const pages = Math.max(1, Math.ceil(total / LIMIT));
  const currentPage = Math.floor(offset / LIMIT) + 1;
  const rangeStart = total === 0 ? 0 : offset + 1;
  const rangeEnd = Math.min(offset + transactions.length, total);

  const importErrors = importResult?.errors ?? [];
  const importWarnings = importResult?.warnings ?? [];
  const importNotes = [...importErrors, ...importWarnings];

  return (
    <div>
      {importResult && (
        <div role="status" className="mb-3.5 flex items-start gap-2.5 rounded-apple border border-divider bg-inset py-1.5 pl-3.5">
          <svg className={`mt-3 w-5 h-5 shrink-0 ${importErrors.length > 0 ? 'text-ink-muted' : 'text-sage'}`} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
            {importErrors.length > 0 ? <path d="M12 8v5M12 16.5v.5M4 20h16L12 4z" /> : <path d="M5 12.5l4.5 4.5L19 7.5" />}
          </svg>
          <div className="flex-1 min-w-0 flex flex-col gap-0.5 py-1.5">
            <span className="text-[14px] font-semibold text-ink">Import finished</span>
            <span className="flex flex-wrap gap-x-3.5 text-[13px] text-ink-body">
              <span>{fmtNum(importResult.imported)} new</span>
              {(importResult.updated ?? 0) > 0 && <span>{fmtNum(importResult.updated)} updated</span>}
              <span>{fmtNum(importResult.skipped)} {importResult.skipped === 1 ? 'duplicate' : 'duplicates'} skipped</span>
              {(importResult.rsu_vests ?? 0) > 0 && (
                <span>{fmtNum(importResult.rsu_vests)} vesting {importResult.rsu_vests === 1 ? 'event' : 'events'}</span>
              )}
              <span>{fmtNum(importErrors.length)} {importErrors.length === 1 ? 'error' : 'errors'}</span>
              {importResult.new_securities?.length > 0 && (
                <span>{fmtNum(importResult.new_securities.length)} new {importResult.new_securities.length === 1 ? 'security' : 'securities'}</span>
              )}
            </span>
            {importNotes.length > 0 && (
              <details className="text-[13px] text-ink-body">
                <summary className="cursor-pointer min-h-[44px] md:min-h-[32px] flex items-center underline">
                  Show {fmtNum(importNotes.length)} {importNotes.length === 1 ? 'message' : 'messages'} from the import
                </summary>
                <ul className="list-disc pl-5 pb-1 space-y-0.5 break-words">
                  {importNotes.map((msg, i) => <li key={i}>{msg}</li>)}
                </ul>
              </details>
            )}
          </div>
          <button
            type="button"
            aria-label="Dismiss import result"
            onClick={() => setImportResult(null)}
            className="shrink-0 inline-flex items-center justify-center w-11 h-11 text-ink-muted hover:text-ink"
          >
            <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" aria-hidden="true">
              <path d="M6 6l12 12M18 6L6 18" />
            </svg>
          </button>
        </div>
      )}

      <PageHeader
        title="Transactions"
        meta={loaded && !error ? (
          <span aria-live="polite">
            {filtersActive
              ? `${fmtNum(total)} matching ${total === 1 ? 'transaction' : 'transactions'}`
              : `${fmtNum(total)} ${total === 1 ? 'transaction' : 'transactions'} in total`}
          </span>
        ) : undefined}
        actions={
          <button
            type="button"
            aria-expanded={showUploader}
            aria-controls="csv-import-panel"
            onClick={() => setShowUploader(v => !v)}
            className="apple-btn-secondary min-h-[44px]"
          >
            Import CSV
          </button>
        }
      />

      {showUploader && (
        <div id="csv-import-panel" className="mt-3 rounded-apple border border-divider p-4">
          <SectionBoundary>
            <CsvUploader
              accounts={activeAccounts}
              onImportComplete={(result) => {
                setImportResult(result);
                setShowUploader(false);
                if (offset === 0) loadTransactions(); else setOffset(0);
                loadHistory();
              }}
            />
          </SectionBoundary>
        </div>
      )}

      {/* Filters */}
      <div className="mt-3 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div role="group" aria-label="Filter transactions" className="flex gap-2 min-w-0">
          <CompactSelect label="Filter by account" className="flex-[1.35] lg:flex-none lg:w-[200px]" value={accountFilter} onChange={(e) => changeAccount(e.target.value)}>
            <option value="">All accounts</option>
            {accounts.map(a => <option key={a.id} value={a.id}>{a.is_active ? a.name : `${a.name} (inactive)`}</option>)}
          </CompactSelect>
          <CompactSelect label="Filter by transaction type" className="flex-1 lg:flex-none lg:w-[170px]" value={typeFilter} onChange={(e) => changeType(e.target.value)}>
            <option value="">All types</option>
            {TXN_TYPES.map(t => <option key={t} value={t}>{labelFor(t)}</option>)}
          </CompactSelect>
          <CompactSelect label="Filter by month" className="flex-1 lg:flex-none lg:w-[170px]" value={period} onChange={(e) => changePeriod(e.target.value)}>
            <option value="">All dates</option>
            {monthOptions.map(m => <option key={m} value={m}>{fmtMonth(monthDate(m))}</option>)}
            <option value="custom">Custom range…</option>
          </CompactSelect>
        </div>
        <form
          role="search"
          className="flex items-end gap-2 lg:w-[360px]"
          onSubmit={(e) => { e.preventDefault(); applySearch(); }}
        >
          <Field
            label="Search"
            type="search"
            className="flex-1"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder="Search counterparty, ISIN..."
          />
          <button type="submit" className="apple-btn-secondary min-h-[44px]">Search</button>
        </form>
      </div>
      {period === 'custom' && (
        <div className="mt-3 grid grid-cols-2 gap-2 max-w-md">
          <Field label="From" type="date" value={customFrom} max={customTo || undefined} onChange={(e) => changeFrom(e.target.value)} />
          <Field label="To" type="date" value={customTo} min={customFrom || undefined} onChange={(e) => changeTo(e.target.value)} />
        </div>
      )}
      {filtersActive && (
        <div className="mt-1">
          <button type="button" onClick={clearFilters} className="inline-flex items-center min-h-[44px] text-[14px] font-medium text-forest underline">
            Clear filters
          </button>
        </div>
      )}

      {categoryError && (
        <p role="alert" className="mt-3 text-[13px] text-claret">{categoryError}</p>
      )}

      <SectionBoundary>
        <div className="mt-4">
          {error ? (
            <ErrorState message="Transactions couldn’t be loaded." details={error} onRetry={loadTransactions} />
          ) : !loaded ? (
            <LoadingState message="Loading transactions" lines={8} />
          ) : transactions.length === 0 && loading ? (
            <LoadingState message="Loading transactions" lines={8} />
          ) : transactions.length === 0 ? (
            filtersActive ? (
              <EmptyState
                title="No matching transactions"
                message={`There are no transactions ${filterSummary}.`}
                action={{ label: 'Clear filters', onClick: clearFilters }}
              />
            ) : (
              <EmptyState
                title="No transactions yet"
                message="Import a CSV export from your bank or broker to get started."
                action={{ label: 'Import CSV', onClick: () => setShowUploader(true) }}
              />
            )
          ) : (
            <div aria-busy={loading} className={`transition-opacity ${loading ? 'opacity-50' : ''}`}>
              {loading && <span role="status" className="sr-only">Loading transactions</span>}

              {/* Phones: cards */}
              <div className="md:hidden">
                {groups.map(g => (
                  <section key={g.month} aria-label={fmtMonth(monthDate(g.month))}>
                    <h2 className="label-caps mt-4 py-2 border-b border-ink">{groupLabel(g)}</h2>
                    <ul>
                      {g.rows.map(t => {
                        const amount = amountView(t, unitOf(t));
                        const qty = quantityLine(t, unitOf(t));
                        return (
                          <li key={t.id} className="flex flex-col gap-1.5 py-3 border-b border-divider">
                            <div className="flex items-baseline justify-between gap-3">
                              <span className="min-w-0 truncate text-[14px] font-medium text-ink" title={displayName(t)}>{displayName(t)}</span>
                              <span className="flex flex-col items-end shrink-0" data-privacy="blur">
                                <span className={`font-serif text-[18px] leading-6 whitespace-nowrap ${amount.className}`}>{amount.text}</span>
                                {amount.detail && <span className="text-[12px] text-ink-muted whitespace-nowrap">{amount.detail}</span>}
                              </span>
                            </div>
                            <div className="flex items-center gap-2.5">
                              <div className="flex-1 min-w-0 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-ink-muted">
                                <span>{fmtDate(t.date)} · {t.account_name}</span>
                                <Chip>{labelFor(t.type)}</Chip>
                                {qty && <span className="basis-full text-ink-body">{qty}</span>}
                                {t.security_isin && t.security_isin !== displayName(t) && (
                                  <span className="basis-full font-mono text-[12px]">{t.security_isin}</span>
                                )}
                              </div>
                              {hasCategory(t) && categorySelect(t, 'w-[146px] shrink-0')}
                            </div>
                          </li>
                        );
                      })}
                    </ul>
                  </section>
                ))}
              </div>

              {/* Desktop: table */}
              <div className="hidden md:block overflow-x-auto" role="region" tabIndex={0} aria-label="Transactions table">
                <table className="apple-table">
                  <thead>
                    <tr>
                      <th scope="col">Date</th>
                      <th scope="col">Description</th>
                      <th scope="col">Type</th>
                      <th scope="col">Account</th>
                      <th scope="col">Category</th>
                      <th scope="col" className="num">Amount</th>
                    </tr>
                  </thead>
                  {groups.map(g => (
                    <tbody key={g.month}>
                      <tr>
                        <th scope="colgroup" colSpan={columnCount} className="!bg-transparent !pt-5 !pb-2 !border-b-ink">
                          {groupLabel(g)}
                        </th>
                      </tr>
                      {g.rows.map(t => {
                        const amount = amountView(t, unitOf(t));
                        const qty = quantityLine(t, unitOf(t));
                        return (
                          <tr key={t.id} className="hover:bg-parchment-deep">
                            <td className="!py-2 whitespace-nowrap">{fmtDate(t.date)}</td>
                            <td className="!py-2 max-w-[280px]">
                              <div className="truncate text-ink" title={displayName(t)}>{displayName(t)}</div>
                              {(qty || (t.security_isin && t.security_isin !== displayName(t))) && (
                                <div className="truncate text-[12px] text-ink-muted">
                                  {t.security_isin && t.security_isin !== displayName(t) && <span className="font-mono">{t.security_isin}</span>}
                                  {qty && t.security_isin !== displayName(t) && ' · '}
                                  {qty}
                                </div>
                              )}
                            </td>
                            <td className="!py-2"><Chip>{labelFor(t.type)}</Chip></td>
                            <td className="!py-2 max-w-[200px]"><div className="truncate" title={t.account_name}>{t.account_name}</div></td>
                            <td className="!py-2">{hasCategory(t) ? categorySelect(t, 'w-[150px]') : null}</td>
                            <td className="!py-2 num whitespace-nowrap" data-privacy="blur">
                              <div className={`font-serif text-[16px] ${amount.className}`}>{amount.text}</div>
                              {amount.detail && <div className="text-[12px] text-ink-muted">{amount.detail}</div>}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  ))}
                </table>
              </div>
            </div>
          )}
        </div>

        {/* Pager and totals, together at the bottom */}
        {!error && loaded && total > 0 && (
          <div className="mt-4">
            {pages > 1 && (
              <nav aria-label="Pagination" className="flex items-center justify-between gap-2 md:justify-center md:gap-4">
                <button
                  type="button"
                  onClick={() => setOffset(Math.max(0, offset - LIMIT))}
                  disabled={offset === 0}
                  className="apple-btn-secondary min-h-[44px]"
                >
                  Previous
                </button>
                <CompactSelect
                  label="Go to page"
                  className="w-[150px]"
                  value={currentPage}
                  onChange={(e) => setOffset((Number(e.target.value) - 1) * LIMIT)}
                >
                  {Array.from({ length: pages }, (_, i) => (
                    <option key={i + 1} value={i + 1}>Page {fmtNum(i + 1)} of {fmtNum(pages)}</option>
                  ))}
                </CompactSelect>
                <button
                  type="button"
                  onClick={() => setOffset(offset + LIMIT)}
                  disabled={offset + LIMIT >= total}
                  className="apple-btn-secondary min-h-[44px]"
                >
                  Next
                </button>
              </nav>
            )}
            <p className="mt-2.5 text-[12px] text-ink-muted text-center">
              Showing {fmtNum(rangeStart)}–{fmtNum(rangeEnd)} of {fmtNum(total)}
              {filtersActive && filterSummary ? ` ${filterSummary}` : ''}.
              {isMonth && ' Change the month above to go further back.'}
            </p>
          </div>
        )}
      </SectionBoundary>

      {/* Import History */}
      {importHistory.length > 0 && (
        <SectionBoundary>
          <Section
            title="Import History"
            actions={<span className="text-[13px] text-ink-muted">{fmtNum(historyTotal)} {historyTotal === 1 ? 'import' : 'imports'}</span>}
          >
            <ul className="divide-y divide-divider border-t border-divider">
              {(showAllHistory ? importHistory : importHistory.slice(0, 5)).map((entry) => (
                <li key={entry.id} className="py-3 flex items-start justify-between gap-3">
                  <div className="min-w-0 flex-1">
                    <p className="text-[14px] font-medium text-ink truncate" title={entry.filename || undefined}>
                      {entry.filename || (entry.institution ? labelFor(entry.institution) : 'CSV import')}
                    </p>
                    <p className="text-[13px] text-ink-muted mt-0.5">
                      {entry.account_name} · {fmtDateTime(entry.imported_at)}
                    </p>
                  </div>
                  <div className="text-right shrink-0 text-[13px]">
                    <p>
                      <span className={entry.imported > 0 ? 'font-medium text-ink' : 'text-ink-muted'}>{fmtNum(entry.imported)} new</span>
                      {(entry.updated ?? 0) > 0 && (
                        <span className="text-ink-muted"> · {fmtNum(entry.updated)} updated</span>
                      )}
                      {entry.skipped > 0 && (
                        <span className="text-ink-muted"> · {fmtNum(entry.skipped)} skipped</span>
                      )}
                    </p>
                    {(entry.rsu_vests ?? 0) > 0 && (
                      <p className="text-ink-muted mt-0.5">
                        {fmtNum(entry.rsu_vests)} vesting {entry.rsu_vests === 1 ? 'event' : 'events'}
                      </p>
                    )}
                    {entry.new_securities && entry.new_securities.length > 0 && (
                      <p className="text-ink-muted mt-0.5">
                        {fmtNum(entry.new_securities.length)} new {entry.new_securities.length === 1 ? 'security' : 'securities'}
                      </p>
                    )}
                  </div>
                </li>
              ))}
            </ul>
            <div className="flex flex-wrap gap-x-5">
              {importHistory.length > 5 && (
                <button
                  type="button"
                  aria-expanded={showAllHistory}
                  onClick={() => setShowAllHistory(v => !v)}
                  className="mt-1 inline-flex items-center min-h-[44px] text-[14px] font-medium text-forest underline"
                >
                  {showAllHistory ? 'Show fewer imports' : `Show all ${fmtNum(historyTotal)} imports`}
                </button>
              )}
              {showAllHistory && importHistory.length < historyTotal && (
                <button
                  type="button"
                  onClick={loadMoreHistory}
                  className="mt-1 inline-flex items-center min-h-[44px] text-[14px] font-medium text-forest underline"
                >
                  Load {fmtNum(Math.min(HISTORY_PAGE, historyTotal - importHistory.length))} older imports
                </button>
              )}
            </div>
          </Section>
        </SectionBoundary>
      )}
    </div>
  );
}
