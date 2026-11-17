import { useEffect, useState } from 'react';
import { api, type Account, type Loan, type LoanInput } from '../../api/client';
import { Field, SelectField } from '../ui';
import { fmtEUR } from '../../utils/format';
import { formGrid } from './format';

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
  const derivedAnnuity = l
    ? Math.round((l.principal * (l.nominal_rate + l.initial_repayment)) / 12 * 100) / 100
    : null;
  const hasExplicitMonthlyOverride =
    l != null && derivedAnnuity != null && Math.abs(l.monthly_payment - derivedAnnuity) >= 0.02;
  return {
    name: l?.name ?? 'Mortgage',
    lender: l?.lender ?? '',
    principal: l ? String(l.principal) : '',
    payout_date: l?.payout_date ?? '',
    first_payment_date: l?.first_payment_date ?? '',
    rate_pct: l ? String(+(l.nominal_rate * 100).toFixed(4)) : '',
    repayment_pct: l ? String(+(l.initial_repayment * 100).toFixed(2)) : '2',
    monthly_payment: hasExplicitMonthlyOverride && l ? String(l.monthly_payment) : '',
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
  const computed = annuity(value);
  const set = (k: keyof LoanFormState) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => {
    const nextVal = e.target.value;
    const nextState: LoanFormState = { ...value, [k]: nextVal };
    if (k === 'repayment_pct') {
      nextState.monthly_payment = '';
    } else if ((k === 'principal' || k === 'rate_pct') && value.monthly_payment && computed != null && Math.abs(num(value.monthly_payment) - computed) < 0.02) {
      nextState.monthly_payment = '';
    }
    onChange(nextState);
  };
  const id = (k: string) => `${idPrefix}-${k}`;

  return (
    <div className="grid gap-x-5 gap-y-4" style={formGrid(190)}>
      <Field id={id('lender')} label="Lender" value={value.lender} onChange={set('lender')} hint="For example your bank" />
      <Field id={id('name')} label="Name" value={value.name} onChange={set('name')} />
      <Field id={id('principal')} label="Loan amount" unit="€" type="number" inputMode="decimal" min="1" step="0.01" required value={value.principal} onChange={set('principal')} />
      <Field id={id('payout')} label="Paid out on" type="date" required value={value.payout_date} onChange={set('payout_date')} />
      <Field id={id('first')} label="First payment (optional)" type="date" value={value.first_payment_date} onChange={set('first_payment_date')} />
      <Field id={id('rate')} label="Nominal rate" unit="% / year" type="number" inputMode="decimal" min="0" max="20" step="0.01" required value={value.rate_pct} onChange={set('rate_pct')} />
      <Field id={id('repay')} label="Initial repayment" unit="% / year" type="number" inputMode="decimal" min="0" max="20" step="0.01" value={value.repayment_pct} onChange={set('repayment_pct')} />
      <Field id={id('payment')} label="Monthly payment (optional)" unit="€" type="number" inputMode="decimal" min="0" step="0.01" value={value.monthly_payment} onChange={set('monthly_payment')}
        hint={computed != null ? `Leave empty to use ${fmtEUR(computed, { cents: true })} from the repayment rate` : 'Leave empty to derive it from the repayment rate'} />
      <Field id={id('fixed')} label="Fixed rate until" type="date" value={value.fixed_until} onChange={set('fixed_until')} />
      <Field id={id('followup')} label="Rate after that (optional)" unit="% / year" type="number" inputMode="decimal" min="0" max="20" step="0.01" value={value.followup_rate_pct} onChange={set('followup_rate_pct')} hint="Leave empty to assume the same rate" />
      <Field id={id('extra')} label="Extra repayment allowed" unit="% / year" type="number" inputMode="decimal" min="0" max="100" step="0.5" value={value.extra_allowance_pct} onChange={set('extra_allowance_pct')} />
      <SelectField id={id('acct')} label="Paid from" value={value.payment_account_id} onChange={set('payment_account_id')}>
        <option value="">Any account</option>
        {accounts.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
      </SelectField>
      <p className="text-[13px] text-ink-body" style={{ gridColumn: '1 / -1' }}>
        Monthly payment{' '}
        <strong data-privacy="blur">{value.monthly_payment ? fmtEUR(num(value.monthly_payment), { cents: true }) : computed != null ? fmtEUR(computed, { cents: true }) : '—'}</strong>
        {!value.monthly_payment && computed != null && ' (from the repayment rate)'}. Payments from the chosen account are matched automatically.
      </p>
    </div>
  );
}
