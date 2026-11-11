import { useEffect, useState } from 'react';
import { api, type Account, type Loan, type LoanInput } from '../../api/client';
import { fmtCents, fieldCls, labelCls } from './format';

export interface LoanFormState {
  name: string;
  lender: string;
  principal: string;
  payout_date: string;
  first_payment_date: string;
  rate_pct: string;
  repayment_pct: string;
  monthly_payment: string;
  fixed_until: string;
  followup_rate_pct: string;
  extra_allowance_pct: string;
  payment_account_id: string;
}

const num = (s: string) => parseFloat(s.replace(',', '.'));

export function loanFormFrom(l: Loan | null, defaults?: Partial<LoanFormState>): LoanFormState {
  return {
    name: l?.name ?? 'Mortgage',
    lender: l?.lender ?? '',
    principal: l ? String(l.principal) : '',
    payout_date: l?.payout_date ?? '',
    first_payment_date: l?.first_payment_date ?? '',
    rate_pct: l ? String(+(l.nominal_rate * 100).toFixed(4)) : '',
    repayment_pct: l ? String(+(l.initial_repayment * 100).toFixed(2)) : '2',
    monthly_payment: l ? String(l.monthly_payment) : '',
    fixed_until: l?.fixed_until ?? '',
    followup_rate_pct: l?.followup_rate != null ? String(+(l.followup_rate * 100).toFixed(4)) : '',
    extra_allowance_pct: l ? String(+(l.extra_allowance_pct * 100).toFixed(2)) : '5',
    payment_account_id: l?.payment_account_id ?? '',
    ...defaults,
  };
}

/** Monthly annuity for an initial repayment rate, as the bank computes it. */
export function annuity(f: LoanFormState): number | null {
  const p = num(f.principal), r = num(f.rate_pct) / 100, t = num(f.repayment_pct) / 100;
  if ([p, r, t].some(isNaN) || p <= 0) return null;
  return Math.round((p * (r + t)) / 12 * 100) / 100;
}

export function loanInputFrom(f: LoanFormState): LoanInput {
  const payment = num(f.monthly_payment);
  return {
    name: f.name.trim() || 'Loan',
    lender: f.lender.trim(),
    principal: num(f.principal),
    payout_date: f.payout_date,
    first_payment_date: f.first_payment_date || undefined,
    nominal_rate: num(f.rate_pct) / 100,
    ...(payment > 0 ? { monthly_payment: payment } : { initial_repayment: num(f.repayment_pct) / 100 }),
    fixed_until: f.fixed_until || undefined,
    followup_rate: f.followup_rate_pct ? num(f.followup_rate_pct) / 100 : undefined,
    extra_allowance_pct: (num(f.extra_allowance_pct) || 0) / 100,
    payment_account_id: f.payment_account_id || undefined,
  };
}

export default function LoanFields({ value, onChange, idPrefix }: {
  value: LoanFormState;
  onChange: (v: LoanFormState) => void;
  idPrefix: string;
}) {
  const [accounts, setAccounts] = useState<Account[]>([]);
  useEffect(() => {
    api.listAccounts().then(r => setAccounts((r.accounts || []).filter(a => a.type === 'checking' || a.type === 'savings'))).catch(() => {});
  }, []);
  const set = (k: keyof LoanFormState) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
    onChange({ ...value, [k]: e.target.value });
  const computed = annuity(value);
  const id = (k: string) => `${idPrefix}-${k}`;

  return (
    <div className="grid gap-x-5 gap-y-3.5" style={{ gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 190px), 1fr))' }}>
      <label className={labelCls} htmlFor={id('lender')}>Lender
        <input id={id('lender')} className={fieldCls} value={value.lender} onChange={set('lender')} placeholder="e.g. Sparkasse" />
      </label>
      <label className={labelCls} htmlFor={id('name')}>Name
        <input id={id('name')} className={fieldCls} value={value.name} onChange={set('name')} />
      </label>
      <label className={labelCls} htmlFor={id('principal')}>Loan amount (€)
        <input id={id('principal')} className={fieldCls} type="number" min="1" step="0.01" required value={value.principal} onChange={set('principal')} />
      </label>
      <label className={labelCls} htmlFor={id('payout')}>Paid out on
        <input id={id('payout')} className={fieldCls} type="date" required value={value.payout_date} onChange={set('payout_date')} />
      </label>
      <label className={labelCls} htmlFor={id('first')}>First payment (optional)
        <input id={id('first')} className={fieldCls} type="date" value={value.first_payment_date} onChange={set('first_payment_date')} />
      </label>
      <label className={labelCls} htmlFor={id('rate')}>Nominal rate (%)
        <input id={id('rate')} className={fieldCls} type="number" min="0" max="20" step="0.01" required value={value.rate_pct} onChange={set('rate_pct')} />
      </label>
      <label className={labelCls} htmlFor={id('repay')}>Initial repayment (%/yr)
        <input id={id('repay')} className={fieldCls} type="number" min="0" max="20" step="0.01" value={value.repayment_pct} onChange={set('repayment_pct')} />
      </label>
      <label className={labelCls} htmlFor={id('payment')}>Monthly payment (€)
        <input id={id('payment')} className={fieldCls} type="number" min="0" step="0.01" value={value.monthly_payment} onChange={set('monthly_payment')}
          placeholder={computed != null ? String(computed) : 'from repayment rate'} />
      </label>
      <label className={labelCls} htmlFor={id('fixed')}>Fixed rate until
        <input id={id('fixed')} className={fieldCls} type="date" value={value.fixed_until} onChange={set('fixed_until')} />
      </label>
      <label className={labelCls} htmlFor={id('followup')}>Rate after that (%, optional)
        <input id={id('followup')} className={fieldCls} type="number" min="0" max="20" step="0.01" value={value.followup_rate_pct} onChange={set('followup_rate_pct')} placeholder="same as now" />
      </label>
      <label className={labelCls} htmlFor={id('extra')}>Extra repayment allowed (%/yr)
        <input id={id('extra')} className={fieldCls} type="number" min="0" max="100" step="0.5" value={value.extra_allowance_pct} onChange={set('extra_allowance_pct')} />
      </label>
      <label className={labelCls} htmlFor={id('acct')}>Paid from
        <select id={id('acct')} className={fieldCls} value={value.payment_account_id} onChange={set('payment_account_id')}>
          <option value="">Any account</option>
          {accounts.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
        </select>
      </label>
      <p className="text-[13px] text-ink-body" style={{ gridColumn: '1 / -1' }}>
        Monthly payment <strong className="tabular-nums">{value.monthly_payment ? fmtCents(num(value.monthly_payment)) : computed != null ? fmtCents(computed) : '—'}</strong>
        {!value.monthly_payment && computed != null && ' (from the repayment rate)'} · payments from the chosen account are matched automatically.
      </p>
    </div>
  );
}
