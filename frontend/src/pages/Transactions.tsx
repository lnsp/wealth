import { useState, useEffect, useCallback } from 'react';
import { api, type TransactionRow, type ImportHistoryEntry, type Account } from '../api/client';
import CsvUploader from '../components/CsvUploader';

const txnTypes = ['', 'buy', 'sell', 'dividend', 'deposit', 'withdrawal', 'interest', 'fee', 'transfer', 'transfer_out', 'savings_plan'];

// Cash-flow categories user can override transactions with. Must match
// internal/cashflow.AllCategories() — backend rejects unknown values.
const CASHFLOW_CATEGORIES = [
  'salary', 'interest', 'refund',
  'housing', 'utilities', 'insurance', 'subscriptions',
  'groceries', 'dining', 'transport', 'health',
  'entertainment', 'shopping', 'tax',
  'investment', 'internal', 'other',
];

// Cash-transaction types — only these get the category override dropdown.
// Buys/sells/dividends route through the investment classifier on the server
// and never appear in the cashflow page anyway.
const CASH_TXN_TYPES = new Set(['deposit', 'withdrawal', 'interest', 'transfer', 'transfer_out', 'cash_transfer_in', 'cash_transfer_out', 'fee']);

export default function Transactions() {
  const [transactions, setTransactions] = useState<TransactionRow[]>([]);
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [typeFilter, setTypeFilter] = useState('');
  const [search, setSearch] = useState('');
  const [searchInput, setSearchInput] = useState('');
  const [dateFrom, setDateFrom] = useState('');
  const [dateTo, setDateTo] = useState('');
  const [importHistory, setImportHistory] = useState<ImportHistoryEntry[]>([]);
  const [showAllHistory, setShowAllHistory] = useState(false);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [showUploader, setShowUploader] = useState(false);
  const limit = 50;

  const loadTransactions = useCallback(async () => {
    setLoading(true);
    try {
      const res = await api.listTransactions(limit, offset, typeFilter, search, dateFrom, dateTo);
      setTransactions(res.transactions || []);
      setTotal(res.total);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load transactions');
    } finally {
      setLoading(false);
    }
  }, [offset, typeFilter, search, dateFrom, dateTo]);

  useEffect(() => { loadTransactions(); }, [loadTransactions]);
  useEffect(() => { api.getImportHistory().then(r => setImportHistory(r.history || [])).catch(() => {}); }, []);
  useEffect(() => { api.listAccounts().then(r => setAccounts(r.accounts || [])).catch(() => {}); }, []);

  // Reset offset when filters change
  useEffect(() => { setOffset(0); }, [typeFilter, search, dateFrom, dateTo]);

  const handleSearch = () => {
    setSearch(searchInput);
  };

  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);
  const fmtDate = (d: string) =>
    new Date(d).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' });

  // Outlined-chip pattern: `bg-parchment-deep + border border-{color} +
  // text-{color}`. The previous `bg-{color}/10 text-{color}` relied on
  // Tailwind's alpha-modifier utility, which doesn't generate against
  // this codebase's var-backed color tokens — so the bg tint silently
  // disappeared and only the colored text remained. Outlined chips give
  // the badge a proper pill affordance while keeping the spec's color
  // mapping intact (forest/amber/sage/claret/walnut/slate per type, fee
  // stays as the muted divider chip).
  const typeStyles: Record<string, string> = {
    buy: 'bg-parchment-deep border border-forest text-forest',
    sell: 'bg-parchment-deep border border-amber text-amber',
    dividend: 'bg-parchment-deep border border-sage text-sage',
    deposit: 'bg-parchment-deep border border-sage text-sage',
    withdrawal: 'bg-parchment-deep border border-claret text-claret',
    interest: 'bg-parchment-deep border border-amber text-amber',
    fee: 'bg-divider text-ink-body',
    transfer: 'bg-parchment-deep border border-walnut text-walnut',
    transfer_out: 'bg-parchment-deep border border-walnut text-walnut',
    savings_plan: 'bg-parchment-deep border border-slate text-slate',
  };

  const pages = Math.ceil(total / limit);
  const currentPage = Math.floor(offset / limit) + 1;

  return (
    <div className="space-y-6">
      {/* Import CSV */}
      <div>
        <button
          onClick={() => setShowUploader(!showUploader)}
          className="apple-btn-primary flex items-center gap-2"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5" />
          </svg>
          Import CSV
        </button>
        {showUploader && (
          <div className="mt-4">
            <CsvUploader
              accounts={accounts}
              onImportComplete={() => {
                setShowUploader(false);
                loadTransactions();
                api.getImportHistory().then(r => setImportHistory(r.history || [])).catch(() => {});
              }}
            />
          </div>
        )}
      </div>

      {/* Filters */}
      <div className="space-y-2">
        <div className="flex gap-2">
          <select
            aria-label="Filter by transaction type"
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            className="shrink-0 rounded-[8px] border border-divider bg-parchment px-3 py-2 text-[15px] text-ink focus:outline-none focus:ring-2 focus:ring-forest/30"
          >
            <option value="">All types</option>
            {txnTypes.filter(Boolean).map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
          {(typeFilter || search || dateFrom || dateTo) && (
            <button
              onClick={() => { setTypeFilter(''); setSearch(''); setSearchInput(''); setDateFrom(''); setDateTo(''); }}
              className="shrink-0 rounded-[8px] px-3 py-2 text-[15px] text-claret hover:bg-parchment-deep transition-colors"
            >
              Clear
            </button>
          )}
          <span className="ml-auto shrink-0 text-[13px] text-ink-muted self-center">{total} total</span>
        </div>

        <div className="flex">
          <input
            aria-label="Search transactions"
            type="text"
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
            placeholder="Search counterparty, ISIN..."
            className="flex-1 min-w-0 rounded-l-[8px] border border-r-0 border-divider bg-parchment px-3 py-2 text-[15px] text-ink placeholder:text-ink-muted focus:outline-none focus:ring-2 focus:ring-forest/30"
          />
          <button
            onClick={handleSearch}
            className="shrink-0 rounded-r-[8px] border border-divider bg-parchment-deep px-3 py-2 text-[15px] text-ink-muted hover:bg-divider transition-colors"
          >
            Search
          </button>
        </div>

        <div className="flex gap-2 items-center">
          <input
            aria-label="Date from"
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="flex-1 min-w-0 rounded-[8px] border border-divider bg-parchment px-2 py-2 text-[15px] text-ink focus:outline-none focus:ring-2 focus:ring-forest/30"
          />
          <span className="text-[13px] text-ink-muted shrink-0">to</span>
          <input
            aria-label="Date to"
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="flex-1 min-w-0 rounded-[8px] border border-divider bg-parchment px-2 py-2 text-[15px] text-ink focus:outline-none focus:ring-2 focus:ring-forest/30"
          />
        </div>
      </div>

      {error ? (
        <div className="flex flex-col items-center justify-center py-20 gap-3">
          <svg className="w-10 h-10 text-claret" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
          </svg>
          <p className="text-[16px] text-claret">{error}</p>
          <button onClick={() => { setError(null); loadTransactions(); }} className="text-forest text-[15px] font-medium">Retry</button>
        </div>
      ) : loading ? (
        <div className="flex items-center justify-center py-20 text-[16px] text-ink-muted">Loading...</div>
      ) : transactions.length === 0 ? (
        <div className="py-12 text-center text-[16px] text-ink-muted">
          {typeFilter || search ? 'No transactions match your filters.' : 'No transactions yet. Import a CSV to get started.'}
        </div>
      ) : (
        <>
          {/* Mobile: card layout */}
          <div className="md:hidden divide-y divide-divider">
            {transactions.map((t) => (
              <div key={t.id} className="py-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="text-[15px] font-medium text-ink truncate">
                      {t.counterparty || t.reference || t.account_name}
                    </p>
                    <div className="flex items-center gap-2 mt-1">
                      <span className={`apple-badge ${typeStyles[t.type] || 'bg-parchment-deep text-ink-muted'}`}>
                        {t.type}
                      </span>
                      <span className="text-[12px] text-ink-muted tabular-nums">{fmtDate(t.date)}</span>
                    </div>
                    {t.security_isin && t.quantity != null && t.quantity > 0 && t.price != null && t.price > 0 && (
                      <p className="text-[12px] text-ink-muted mt-1 tabular-nums">
                        {t.quantity} × {fmt(t.price)}
                        <span className="mx-1">·</span>
                        <span className="font-mono">{t.security_isin}</span>
                      </p>
                    )}
                    {t.security_isin && !(t.quantity != null && t.quantity > 0 && t.price != null && t.price > 0) && (
                      <p className="text-[12px] text-ink-muted mt-1 font-mono">{t.security_isin}</p>
                    )}
                  </div>
                  <p className={`text-[15px] font-medium tabular-nums shrink-0 ${
                    t.amount >= 0 ? 'text-ink' : 'text-claret'
                  }`}>
                    {fmt(t.amount)}
                  </p>
                </div>
              </div>
            ))}
          </div>

          {/* Desktop: table layout */}
          <div className="hidden md:block overflow-hidden">
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="text-left font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] bg-parchment-deep">
                    <th className="px-3 py-2.5 font-medium">Date</th>
                    <th className="px-3 py-2.5 font-medium">Type</th>
                    <th className="px-3 py-2.5 font-medium">Security</th>
                    <th className="px-3 py-2.5 font-medium text-right">Qty</th>
                    <th className="px-3 py-2.5 font-medium text-right">Price</th>
                    <th className="px-3 py-2.5 font-medium text-right">Amount</th>
                    <th className="px-3 py-2.5 font-medium">Account</th>
                    <th className="px-3 py-2.5 font-medium">Category</th>
                  </tr>
                </thead>
                <tbody>
                  {transactions.map((t, i) => (
                    <tr
                      key={t.id}
                      className={`transition-colors hover:bg-parchment-deep ${
                        i < transactions.length - 1 ? 'border-b border-divider' : ''
                      }`}
                    >
                      <td className="px-3 py-1 whitespace-nowrap text-[15px] tabular-nums">{fmtDate(t.date)}</td>
                      <td className="px-3 py-1">
                        <span className={`apple-badge ${typeStyles[t.type] || 'bg-parchment-deep text-ink-muted'}`}>
                          {t.type}
                        </span>
                      </td>
                      <td className="px-3 py-1 text-[15px] text-ink max-w-[200px] truncate">
                        {t.counterparty || t.reference || '—'}
                        {t.security_isin && (
                          <span className="block text-[12px] text-ink-muted font-mono">{t.security_isin}</span>
                        )}
                      </td>
                      <td className="px-3 py-1 text-right text-[15px] tabular-nums text-ink-muted">
                        {t.security_isin && t.quantity != null ? t.quantity.toLocaleString('de-DE', { maximumFractionDigits: 3 }) : '—'}
                      </td>
                      <td className="px-3 py-1 text-right text-[15px] tabular-nums text-ink-muted">
                        {t.security_isin && t.price != null && t.price > 0 ? fmt(t.price) : '—'}
                      </td>
                      <td className={`px-3 py-1 text-right text-[15px] font-medium whitespace-nowrap tabular-nums ${
                        t.amount >= 0 ? 'text-ink' : 'text-claret'
                      }`}>
                        {fmt(t.amount)}
                      </td>
                      <td className="px-3 py-1 text-[15px] text-ink-muted">{t.account_name}</td>
                      <td className="px-3 py-1">
                        {CASH_TXN_TYPES.has(t.type) ? (
                          <select
                            value={t.category || ''}
                            onChange={async (e) => {
                              const next = e.target.value;
                              setTransactions(prev => prev.map(x => x.id === t.id ? { ...x, category: next || null } : x));
                              try {
                                await api.updateTransactionCategory(t.id, next);
                              } catch {
                                // Revert on failure — UI is optimistic.
                                setTransactions(prev => prev.map(x => x.id === t.id ? { ...x, category: t.category } : x));
                              }
                            }}
                            className="text-[12px] bg-parchment-deep border border-divider rounded px-1.5 py-0.5 text-ink-body hover:border-forest focus:border-forest focus:outline-none transition-colors"
                          >
                            <option value="">auto</option>
                            {CASHFLOW_CATEGORIES.map(c => (
                              <option key={c} value={c}>{c}</option>
                            ))}
                          </select>
                        ) : (
                          <span className="text-[12px] text-ink-muted">—</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}

      {pages > 1 && (
        <div className="flex items-center justify-between">
          <button
            onClick={() => setOffset(Math.max(0, offset - limit))}
            disabled={offset === 0}
            className="apple-btn-secondary"
          >
            Previous
          </button>
          <span className="text-[13px] text-ink-muted">Page {currentPage} of {pages}</span>
          <button
            onClick={() => setOffset(offset + limit)}
            disabled={offset + limit >= total}
            className="apple-btn-secondary"
          >
            Next
          </button>
        </div>
      )}
      {/* Import History */}
      {importHistory.length > 0 && (
        <div>
          <div className="flex items-center justify-between mb-3">
            <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Import History</h2>
            <span className="text-[12px] text-ink-muted">{importHistory.length} imports</span>
          </div>
          <div className="divide-y divide-divider">
            {(showAllHistory ? importHistory : importHistory.slice(0, 5)).map((entry) => (
              <div key={entry.id} className="py-3">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="text-[15px] font-medium text-ink truncate">
                      {entry.filename || entry.institution || 'CSV Import'}
                    </p>
                    <p className="text-[12px] text-ink-muted mt-0.5">
                      {entry.account_name} · {new Date(entry.imported_at).toLocaleString('de-DE', {
                        day: '2-digit', month: '2-digit', year: 'numeric',
                        hour: '2-digit', minute: '2-digit',
                      })}
                    </p>
                  </div>
                  <div className="text-right shrink-0">
                    <p className="text-[15px] tabular-nums">
                      <span className="text-sage font-medium">{entry.imported}</span>
                      {entry.skipped > 0 && (
                        <span className="text-ink-muted"> / {entry.skipped} skipped</span>
                      )}
                    </p>
                    {entry.new_securities && entry.new_securities.length > 0 && (
                      <p className="text-[12px] text-forest mt-0.5">
                        {entry.new_securities.length} new {entry.new_securities.length === 1 ? 'security' : 'securities'}
                      </p>
                    )}
                  </div>
                </div>
              </div>
            ))}
          </div>
          {importHistory.length > 5 && !showAllHistory && (
            <button
              onClick={() => setShowAllHistory(true)}
              className="w-full mt-2 py-2 text-[13px] text-forest font-medium hover:text-forest-light"
            >
              Show all {importHistory.length} imports
            </button>
          )}
          {showAllHistory && importHistory.length > 5 && (
            <button
              onClick={() => setShowAllHistory(false)}
              className="w-full mt-2 py-2 text-[13px] text-forest font-medium hover:text-forest-light"
            >
              Show less
            </button>
          )}
        </div>
      )}
    </div>
  );
}
