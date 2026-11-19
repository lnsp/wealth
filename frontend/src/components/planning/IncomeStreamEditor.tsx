import { useState } from 'react';
import type { IncomeKind, IncomeStream } from '../../api/client';
import { Field, NumberField, SelectField } from '../ui';
import { ItemCard, RemoveButton } from './shared';

const MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];

export const KIND_LABEL: Record<IncomeKind, string> = {
  salary: 'Base salary',
  bonus: 'Bonus',
  rsu_refresh: 'RSU refresh',
  one_off: 'One-off payment',
};

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

export function newStream(kind: IncomeKind): IncomeStream {
  const base: IncomeStream = {
    kind, name: KIND_LABEL[kind], currency: 'EUR', amount: 0, growth: 0, month: null, payout_factor: null,
    vest_months: null, vest_every: null, cliff_months: 0, account_id: null, first_year: null,
    start_date: today(), end_date: null, steps: [],
  };
  switch (kind) {
    case 'salary': return { ...base, growth: 0.03, month: 1 };
    case 'bonus': return { ...base, amount: 0.15, month: 3, payout_factor: 1 };
    case 'rsu_refresh': return { ...base, currency: 'USD', month: 3, vest_months: 48, vest_every: 1 };
    default: return base;
  }
}

function MonthSelect({ label, value, onChange }: { label: string; value: number | null; onChange: (v: number) => void }) {
  return (
    <SelectField label={label} value={String(value ?? 1)} onChange={e => onChange(Number(e.target.value))}>
      {MONTHS.map((m, i) => <option key={m} value={i + 1}>{m}</option>)}
    </SelectField>
  );
}

interface Props {
  stream: IncomeStream;
  onSave: (s: IncomeStream) => Promise<void>;
  onCancel: () => void;
}

/** Edits one income stream; the backend validates the same rules. */
export default function IncomeStreamEditor({ stream, onSave, onCancel }: Props) {
  const [s, setS] = useState<IncomeStream>(stream);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (patch: Partial<IncomeStream>) => setS(prev => ({ ...prev, ...patch }));
  const pct = (v: number) => Math.round(v * 10000) / 100;

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      await onSave({ ...s, end_date: s.end_date || null });
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Couldn’t save');
    } finally {
      setSaving(false);
    }
  };

  return (
    <ItemCard className="bg-parchment-deep">
      <div className="grid grid-cols-1 gap-x-3 gap-y-4 md:grid-cols-3">
        <Field label="Name" value={s.name} onChange={e => set({ name: e.target.value })} maxLength={100} />

        {s.kind === 'salary' && (<>
          <NumberField label="Gross salary" unit="€ / year" value={s.amount} onValueChange={v => set({ amount: Math.max(0, v) })} hint="As of the start date." />
          <NumberField label="Yearly raise" unit="%" value={pct(s.growth)} onValueChange={v => set({ growth: v / 100 })} />
          <MonthSelect label="Raise month" value={s.month} onChange={v => set({ month: v })} />
        </>)}

        {s.kind === 'bonus' && (<>
          <NumberField label="Target" unit="% of base" value={pct(s.amount)} onValueChange={v => set({ amount: Math.max(0, v) / 100 })} hint="Of last year’s base salary." />
          <NumberField label="Payout factor" unit="%" value={pct(s.payout_factor ?? 1)} onValueChange={v => set({ payout_factor: Math.max(0, v) / 100 })} hint="100 % pays the target." />
          <MonthSelect label="Paid in" value={s.month} onChange={v => set({ month: v })} />
        </>)}

        {s.kind === 'rsu_refresh' && (<>
          <NumberField label="Grant value" unit="USD" value={s.amount} onValueChange={v => set({ amount: Math.max(0, v), currency: 'USD' })} hint="Fixed in USD: the share count is set at the grant price." />
          <MonthSelect label="Granted every" value={s.month} onChange={v => set({ month: v })} />
          <NumberField label="Grant value grows" unit="% / year" value={pct(s.growth)} onValueChange={v => set({ growth: v / 100 })} />
          <SelectField label="Vests over" value={String(s.vest_months ?? 48)} onChange={e => set({ vest_months: Number(e.target.value) })}>
            {[12, 24, 36, 48, 60].map(m => <option key={m} value={m}>{m / 12} {m === 12 ? 'year' : 'years'}</option>)}
          </SelectField>
          <SelectField label="Vests every" value={String(s.vest_every ?? 1)} onChange={e => set({ vest_every: Number(e.target.value) })}>
            <option value={1}>Month</option>
            <option value={3}>Quarter</option>
            <option value={6}>Half year</option>
            <option value={12}>Year</option>
          </SelectField>
          <SelectField label="Cliff" value={String(s.cliff_months)} onChange={e => set({ cliff_months: Number(e.target.value) })}>
            <option value={0}>None</option>
            <option value={12}>12 months</option>
          </SelectField>
          <NumberField label="First modelled grant" unit="year" integer value={s.first_year ?? 0} onValueChange={v => set({ first_year: v > 0 ? v : null })} hint="0: the next grant month. A real grant in a later import replaces that year’s refresh." />
        </>)}

        {s.kind === 'one_off' && (
          <NumberField label="Amount" unit="€" value={s.amount} onValueChange={v => set({ amount: Math.max(0, v) })} hint="Taxed as wages in its month." />
        )}

        <Field label={s.kind === 'one_off' ? 'Paid on' : 'Starts'} type="date" value={s.start_date} onChange={e => set({ start_date: e.target.value })} />
        {s.kind !== 'one_off' && (
          <Field label="Ends" type="date" value={s.end_date ?? ''} onChange={e => set({ end_date: e.target.value || null })} hint="Empty: until your FIRE date or retirement." />
        )}
      </div>

      {s.kind === 'salary' && (
        <div className="mt-5">
          <p className="label-caps mb-2">Step changes</p>
          <p className="mb-3 text-[13px] text-ink-body">A promotion, a new job, or 0 € for unpaid leave. Raises continue from the new amount.</p>
          <div className="flex flex-col gap-3">
            {s.steps.map((st, i) => (
              <div key={i} className="grid grid-cols-[1fr_1fr_auto] items-end gap-3">
                <Field label="From" type="date" value={st.date} onChange={e => set({ steps: s.steps.map((x, j) => j === i ? { ...x, date: e.target.value } : x) })} />
                <NumberField label="New gross" unit="€ / year" value={st.amount} onValueChange={v => set({ steps: s.steps.map((x, j) => j === i ? { ...x, amount: Math.max(0, v) } : x) })} />
                <RemoveButton name={`step from ${st.date}`} onClick={() => set({ steps: s.steps.filter((_, j) => j !== i) })} />
              </div>
            ))}
            <button type="button" className="self-start min-h-[44px] text-[14px] font-semibold text-forest underline underline-offset-2"
              onClick={() => set({ steps: [...s.steps, { date: today(), amount: s.amount }] })}>
              Add a step change
            </button>
          </div>
        </div>
      )}

      {error && <p role="alert" className="mt-3 text-[14px] text-claret">{error}</p>}
      <div className="mt-5 flex flex-wrap gap-3">
        <button type="button" className="apple-btn-primary min-h-[44px]" onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</button>
        <button type="button" className="apple-btn-secondary min-h-[44px]" onClick={onCancel} disabled={saving}>Cancel</button>
      </div>
    </ItemCard>
  );
}
