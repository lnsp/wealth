import { useState } from 'react';
import type { FormEvent } from 'react';
import { api, type Account, type Security } from '../../api/client';
import {
  Section, SubHeading, Field, SelectField, Chip, AddChip,
  LoadingState, ErrorState, EmptyState, SectionBoundary, useConfirm,
} from '../../components/ui';
import { fmtPct, fmtQty, labelFor } from '../../utils/format';
import { useLoad, errorText, InlineError, RowList, ROW_BTN, ROW_BTN_DANGER, FORM_CARD } from './shared';

const INSTITUTIONS: [string, string][] = [
  ['sparkasse', 'Sparkasse'],
  ['n26', 'N26'],
  ['revolut', 'Revolut'],
  ['scalable_capital', 'Scalable Capital'],
  ['trade_republic', 'Trade Republic'],
  ['ing', 'ING'],
  ['dkb', 'DKB'],
  ['comdirect', 'comdirect'],
  ['morgan_stanley', 'Morgan Stanley'],
  ['delta', 'Delta'],
  ['manual', 'Manual entry'],
  ['other', 'Other'],
];

const ACCOUNT_TYPES: [string, string][] = [
  ['checking', 'Checking'],
  ['savings', 'Savings'],
  ['brokerage', 'Brokerage'],
  ['credit', 'Credit card'],
  ['real_estate', 'Real estate'],
  ['pension', 'Pension'],
  ['precious_metals', 'Precious metals'],
  ['liability', 'Liability'],
];

/** German tax wrappers keep their legal names; the gloss says what they are. */
const TAX_TREATMENTS: [string, string][] = [
  ['taxable', 'Taxable (Abgeltungssteuer, flat tax on capital income)'],
  ['bav', 'bAV (company pension)'],
  ['riester', 'Riester (subsidised private pension)'],
  ['rurup', 'Rürup (tax-deductible basic pension)'],
  ['savings', 'Tax-free savings'],
];
const TAX_SHORT: Record<string, string> = { bav: 'bAV', riester: 'Riester', rurup: 'Rürup', savings: 'Tax-free' };

const CURRENCIES: [string, string][] = [
  ['EUR', 'EUR — Euro'],
  ['USD', 'USD — US dollar'],
  ['GBP', 'GBP — Pound sterling'],
  ['CHF', 'CHF — Swiss franc'],
];

const lookup = (list: [string, string][], value: string) => list.find(([v]) => v === value)?.[1] ?? labelFor(value);

/** "12,5" or "12.5" → 12.5; empty → undefined; anything else → NaN. */
function parsePct(text: string): number | undefined {
  const t = text.trim();
  if (!t) return undefined;
  return Number(t.replace(',', '.'));
}

function pctError(text: string): string | undefined {
  const n = parsePct(text);
  if (n === undefined) return undefined;
  return Number.isFinite(n) && n >= 0 && n <= 100 ? undefined : 'Enter a percentage between 0 and 100';
}

/**
 * Morgan Stanley files never name the stock, so imports into such an account take
 * it from here and are refused without it (vests would otherwise book as cash).
 */
function isinError(text: string, required: boolean): string | undefined {
  const v = text.trim().toUpperCase();
  if (!v) return required ? 'Enter the ISIN of the employer stock, e.g. US02079K1079' : undefined;
  return /^[A-Z]{2}[A-Z0-9]{9}[0-9]$/.test(v) ? undefined : 'Enter a 12-character ISIN, e.g. US02079K1079';
}

const STOCK_ISIN_HINT = 'The employer stock held in this account. Morgan Stanley files don’t name it, so imports need it.';

function AddAccountForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState('');
  const [institution, setInstitution] = useState('sparkasse');
  const [type, setType] = useState('checking');
  const [iban, setIban] = useState('');
  const [tax, setTax] = useState('taxable');
  const [match, setMatch] = useState('');
  const [isin, setIsin] = useState('');
  const [submitted, setSubmitted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const nameErr = submitted && !name.trim() ? 'Enter a name for the account' : undefined;
  const matchErr = submitted && tax === 'bav' ? pctError(match) : undefined;
  const isMS = institution === 'morgan_stanley';
  const isinErr = submitted && isMS ? isinError(isin, false) : undefined;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    setError('');
    if (!name.trim() || (tax === 'bav' && pctError(match)) || (isMS && isinError(isin, false))) return;
    setSaving(true);
    try {
      await api.createAccount({
        name: name.trim(),
        institution,
        type,
        iban: iban.trim() || undefined,
        tax_treatment: tax,
        employer_match_pct: tax === 'bav' ? parsePct(match) : undefined,
        import_security_isin: isMS ? isin.trim().toUpperCase() || undefined : undefined,
      });
      onDone();
    } catch (err) {
      setError(errorText(err, 'The account couldn’t be created.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className={`${FORM_CARD} mb-4`}>
      <SubHeading>Add Account</SubHeading>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Account name" value={name} onChange={e => setName(e.target.value)} error={nameErr} autoFocus className="md:col-span-2" />
        <SelectField label="Institution" value={institution} onChange={e => setInstitution(e.target.value)}>
          {INSTITUTIONS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>
        <SelectField label="Type" value={type} onChange={e => setType(e.target.value)}>
          {ACCOUNT_TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>
        <Field label="IBAN" hint="Optional. Used to match imported files to this account." value={iban} onChange={e => setIban(e.target.value)} autoComplete="off" spellCheck={false} />
        <SelectField label="Tax treatment" value={tax} onChange={e => setTax(e.target.value)}>
          {TAX_TREATMENTS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>
        {tax === 'bav' && (
          <Field label="Employer match" unit="%" inputMode="decimal" hint="Optional. Share of your contribution the employer adds." value={match} onChange={e => setMatch(e.target.value)} error={matchErr} />
        )}
        {isMS && (
          <Field label="Stock ISIN" hint={STOCK_ISIN_HINT} value={isin} onChange={e => setIsin(e.target.value)} error={isinErr} autoComplete="off" spellCheck={false} />
        )}
      </div>
      <InlineError>{error}</InlineError>
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">Cancel</button>
        <button type="submit" disabled={saving} className="apple-btn-primary min-h-[44px] md:min-h-[40px]">{saving ? 'Adding…' : 'Add account'}</button>
      </div>
    </form>
  );
}

function EditAccountForm({ acc, onDone, onCancel }: { acc: Account; onDone: () => void; onCancel: () => void }) {
  const [name, setName] = useState(acc.name);
  const [tax, setTax] = useState(acc.tax_treatment || 'taxable');
  const [match, setMatch] = useState(acc.employer_match_pct != null ? String(acc.employer_match_pct).replace('.', ',') : '');
  const [currency, setCurrency] = useState(acc.currency || 'EUR');
  const [isin, setIsin] = useState(acc.import_security_isin ?? '');
  const [submitted, setSubmitted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const isMS = acc.institution === 'morgan_stanley';
  const nameErr = submitted && !name.trim() ? 'Enter a name for the account' : undefined;
  const matchErr = submitted && tax === 'bav' ? pctError(match) : undefined;
  const isinErr = submitted && isMS ? isinError(isin, true) : undefined;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    setError('');
    if (!name.trim() || (tax === 'bav' && pctError(match)) || (isMS && isinError(isin, true))) return;
    const nextISIN = isin.trim().toUpperCase();
    setSaving(true);
    try {
      await api.updateAccount(acc.id, {
        name: name.trim() !== acc.name ? name.trim() : undefined,
        tax_treatment: tax,
        employer_match_pct: tax === 'bav' ? parsePct(match) : undefined,
        currency: currency !== acc.currency ? currency : undefined,
        import_security_isin: isMS && nextISIN !== (acc.import_security_isin ?? '') ? nextISIN : undefined,
      });
      onDone();
    } catch (err) {
      setError(errorText(err, 'The changes couldn’t be saved.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className="px-4 pb-4 flex flex-col gap-4">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="Account name" value={name} onChange={e => setName(e.target.value)} error={nameErr} />
        <SelectField label="Currency" value={currency} onChange={e => setCurrency(e.target.value)}>
          {CURRENCIES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>
        <SelectField label="Tax treatment" value={tax} onChange={e => setTax(e.target.value)}>
          {TAX_TREATMENTS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </SelectField>
        {tax === 'bav' && (
          <Field label="Employer match" unit="%" inputMode="decimal" value={match} onChange={e => setMatch(e.target.value)} error={matchErr} />
        )}
        {isMS && (
          <Field label="Stock ISIN" hint={STOCK_ISIN_HINT} value={isin} onChange={e => setIsin(e.target.value)} error={isinErr} autoComplete="off" spellCheck={false} />
        )}
      </div>
      <InlineError>{error}</InlineError>
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">Cancel</button>
        <button type="submit" disabled={saving} className="apple-btn-primary min-h-[44px] md:min-h-[40px]">{saving ? 'Saving…' : 'Save changes'}</button>
      </div>
    </form>
  );
}

function AccountsSection() {
  const { data, loading, error, reload } = useLoad(() => api.listAllAccounts());
  const [confirm, confirmDialog] = useConfirm();
  const [adding, setAdding] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');
  const accounts = data?.accounts ?? [];

  const remove = async (acc: Account) => {
    const ok = await confirm({
      title: `Delete account “${acc.name}”?`,
      body: 'The account and all of its transactions are removed. This can’t be undone.',
      confirmLabel: 'Delete account',
    });
    if (!ok) return;
    setActionError('');
    try {
      await api.deleteAccount(acc.id);
      await reload();
    } catch (err) {
      setActionError(errorText(err, `“${acc.name}” couldn’t be deleted.`));
    }
  };

  const toggleActive = async (acc: Account) => {
    setActionError('');
    try {
      await api.updateAccount(acc.id, { is_active: !acc.is_active });
      await reload();
    } catch (err) {
      setActionError(errorText(err, `“${acc.name}” couldn’t be updated.`));
    }
  };

  return (
    <Section
      title="Accounts"
      first
      actions={!adding && accounts.length > 0 ? <AddChip onClick={() => setAdding(true)}>Add account</AddChip> : undefined}
    >
      {adding && <AddAccountForm onCancel={() => setAdding(false)} onDone={() => { setAdding(false); reload(); }} />}
      <InlineError>{actionError}</InlineError>
      {loading ? <LoadingState lines={5} /> : error && !data ? (
        <ErrorState message="The accounts couldn’t be loaded." details={error} onRetry={reload} />
      ) : accounts.length === 0 ? (
        !adding && (
          <EmptyState
            title="No accounts yet"
            message="Add the bank and brokerage accounts you want to track, then import their statements."
            action={{ label: 'Add account', onClick: () => setAdding(true) }}
          />
        )
      ) : (
        <RowList>
          {accounts.map(acc => {
            const editing = editingId === acc.id;
            return (
              <li key={acc.id}>
                <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
                  <div className="min-w-0 flex-1 basis-64">
                    <p className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-ink">
                      <span className="break-words">{acc.name}</span>
                      <Chip tone={acc.is_active ? 'success' : 'neutral'}>{acc.is_active ? 'Active' : 'Inactive'}</Chip>
                      {acc.tax_treatment && acc.tax_treatment !== 'taxable' && (
                        <Chip>{TAX_SHORT[acc.tax_treatment] || labelFor(acc.tax_treatment)}</Chip>
                      )}
                      {acc.institution === 'morgan_stanley' && !acc.import_security_isin?.trim() && (
                        <Chip tone="warning">No stock ISIN</Chip>
                      )}
                    </p>
                    <p className="text-[13px] text-ink-muted mt-0.5">
                      {lookup(INSTITUTIONS, acc.institution)} · {lookup(ACCOUNT_TYPES, acc.type)}
                      {acc.currency && acc.currency !== 'EUR' ? ` · ${acc.currency}` : ''}
                      {acc.employer_match_pct != null && acc.employer_match_pct > 0 && ` · ${fmtQty(acc.employer_match_pct)} % employer match`}
                    </p>
                    {acc.iban && (
                      <p className="text-[13px] text-ink-muted mt-0.5 font-mono break-all" data-privacy="blur">{acc.iban}</p>
                    )}
                  </div>
                  <div className="flex flex-wrap items-center gap-2 shrink-0">
                    <button type="button" onClick={() => setEditingId(editing ? null : acc.id)} aria-expanded={editing} aria-label={`${editing ? 'Close' : 'Edit'} ${acc.name}`} className={ROW_BTN}>
                      {editing ? 'Close' : 'Edit'}
                    </button>
                    <button type="button" onClick={() => toggleActive(acc)} aria-label={`${acc.is_active ? 'Deactivate' : 'Activate'} ${acc.name}`} className={ROW_BTN}>
                      {acc.is_active ? 'Deactivate' : 'Activate'}
                    </button>
                    <button type="button" onClick={() => remove(acc)} aria-label={`Delete ${acc.name}…`} className={ROW_BTN_DANGER}>Delete…</button>
                  </div>
                </div>
                {editing && (
                  <EditAccountForm acc={acc} onCancel={() => setEditingId(null)} onDone={() => { setEditingId(null); reload(); }} />
                )}
              </li>
            );
          })}
        </RowList>
      )}
      {confirmDialog}
    </Section>
  );
}

function SymbolForm({ sec, onDone, onCancel }: { sec: Security; onDone: () => void; onCancel: () => void }) {
  const [symbol, setSymbol] = useState(sec.symbol ?? '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    setSaving(true);
    try {
      await api.updateSecuritySymbol(sec.isin, symbol.trim());
      onDone();
    } catch (err) {
      setError(errorText(err, 'The ticker couldn’t be saved.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className="px-4 pb-4 flex flex-col gap-3">
      <div className="flex flex-wrap items-end gap-2.5">
        <Field
          label="Yahoo Finance ticker"
          hint="The symbol Yahoo Finance lists this security under, for example VWCE.DE."
          value={symbol}
          onChange={e => setSymbol(e.target.value)}
          autoComplete="off"
          spellCheck={false}
          autoFocus
          className="flex-1 basis-56"
        />
      </div>
      <InlineError>{error}</InlineError>
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">Cancel</button>
        <button type="submit" disabled={saving} className="apple-btn-primary min-h-[44px] md:min-h-[40px]">{saving ? 'Saving…' : 'Save ticker'}</button>
      </div>
    </form>
  );
}

function SecuritiesSection() {
  const { data, loading, error, reload } = useLoad(() => api.listSecurities());
  const [editingIsin, setEditingIsin] = useState<string | null>(null);
  const securities = data?.securities ?? [];

  return (
    <Section
      title="Securities"
      subtitle="Each security needs a Yahoo Finance ticker to receive price updates."
      actions={<a href="/api/settings/template" className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]">Download holdings template</a>}
    >
      {loading ? <LoadingState lines={5} /> : error && !data ? (
        <ErrorState message="The securities couldn’t be loaded." details={error} onRetry={reload} />
      ) : securities.length === 0 ? (
        <EmptyState
          title="No securities yet"
          message="Securities appear here once you import transactions that carry an ISIN."
          action={{ label: 'Import transactions', href: '/transactions' }}
        />
      ) : (
        <RowList>
          {securities.map(sec => {
            const editing = editingIsin === sec.isin;
            return (
              <li key={sec.isin}>
                <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
                  <div className="min-w-0 flex-1 basis-64">
                    <p className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-ink">
                      <span className="break-words">{sec.name}</span>
                      {!sec.symbol && <Chip tone="warning">No ticker</Chip>}
                    </p>
                    <p className="text-[13px] text-ink-muted mt-0.5">
                      <span className="font-mono">{sec.isin}</span>
                      {sec.wkn && <> · <span className="font-mono">{sec.wkn}</span></>}
                      {sec.asset_class && <> · {/^(etf|etc|etn)$/i.test(sec.asset_class) ? sec.asset_class.toUpperCase() : labelFor(sec.asset_class)}</>}
                      {sec.symbol && <> · <span className="font-mono">{sec.symbol}</span></>}
                      {sec.ter != null && sec.ter > 0 && <> · TER {fmtPct(sec.ter, { digits: 2 })}</>}
                    </p>
                  </div>
                  <button
                    type="button"
                    onClick={() => setEditingIsin(editing ? null : sec.isin)}
                    aria-expanded={editing}
                    aria-label={`${editing ? 'Close' : sec.symbol ? 'Edit ticker for' : 'Set ticker for'} ${sec.name}`}
                    className={ROW_BTN}
                  >
                    {editing ? 'Close' : sec.symbol ? 'Edit ticker' : 'Set ticker'}
                  </button>
                </div>
                {editing && (
                  <SymbolForm sec={sec} onCancel={() => setEditingIsin(null)} onDone={() => { setEditingIsin(null); reload(); }} />
                )}
              </li>
            );
          })}
        </RowList>
      )}
    </Section>
  );
}

export default function AccountsTab() {
  return (
    <>
      <SectionBoundary><AccountsSection /></SectionBoundary>
      <SectionBoundary><SecuritiesSection /></SectionBoundary>
    </>
  );
}
