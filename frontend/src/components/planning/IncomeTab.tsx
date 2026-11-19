import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, type IncomeData, type IncomeKind, type IncomeMonth, type IncomeStream, type IncomeYear, type PlanningProfile } from '../../api/client';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import { Section, SubHeading, MetricCard, MetricGrid, NumberField, SelectField, EmptyState, ErrorState, LoadingState, Chip, AddChip, useConfirm } from '../ui';
import { fmtEUR, fmtMoney, fmtNum, fmtPct, fmtAxis, fmtMonthShort, fmtDate } from '../../utils/format';
import { ScrollTable } from './shared';
import IncomeStreamEditor, { KIND_LABEL, newStream, shapeLabel } from './IncomeStreamEditor';

const MONTH_NAMES = ['January', 'February', 'March', 'April', 'May', 'June', 'July', 'August', 'September', 'October', 'November', 'December'];
const pct = (fraction: number, digits = 1) => fmtPct(fraction * 100, { digits });
// Row headers in an apple-table: plain body text that wraps, not column-head capitals.
const ROW_TH = '!normal-case !tracking-normal !font-sans !text-[14px] !whitespace-normal !bg-transparent !text-ink-body text-left align-top';

function Toggle<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: { id: T; label: string }[]; onChange: (v: T) => void }) {
  return (
    <div role="group" aria-label={label} className="inline-flex flex-wrap gap-1.5">
      {options.map(o => (
        <button
          key={o.id}
          type="button"
          aria-pressed={value === o.id}
          onClick={() => onChange(o.id)}
          className={`min-h-[44px] px-3.5 rounded-[4px] border text-[14px] transition-colors ${value === o.id
            ? 'bg-forest border-forest text-parchment font-semibold'
            : 'border-field text-ink-body font-medium hover:border-forest hover:text-ink'}`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Local copy of the profile that saves itself shortly after each change. */
function useProfileDraft(profile: PlanningProfile, save: (p: PlanningProfile) => Promise<void>) {
  const [draft, setDraft] = useState(profile);
  const [error, setError] = useState<string | null>(null);
  const pending = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => { if (pending.current === 0) setDraft(profile); }, [profile]);
  const update = useCallback((patch: Partial<PlanningProfile>) => {
    setDraft(prev => {
      const next = { ...prev, ...patch };
      if (timer.current) clearTimeout(timer.current);
      pending.current++;
      timer.current = setTimeout(() => {
        save(next)
          .then(() => setError(null))
          .catch(e => setError(e instanceof Error ? e.message : 'Couldn’t save'))
          .finally(() => { pending.current = Math.max(0, pending.current - 1); });
      }, 500);
      return next;
    });
  }, [save]);
  return [draft, update, error] as const;
}

function yearNet(y: IncomeYear) {
  return {
    base: y.base_gross * y.base_keep,
    bonus: (y.bonus_gross + y.one_off_gross) * y.variable_keep,
    held: y.held_gross * y.variable_keep,
    refresh: y.refresh_gross * y.variable_keep,
  };
}

function streamSummary(s: IncomeStream): string {
  switch (s.kind) {
    case 'salary': {
      const raise = s.growth ? ` · +${pct(s.growth)} each ${MONTH_NAMES[(s.month ?? 1) - 1]}` : '';
      return `${fmtEUR(s.amount)} a year from ${fmtDate(s.start_date)}${raise}`;
    }
    case 'bonus':
      return `${pct(s.amount, 0)} of last year’s base salary, paid in ${MONTH_NAMES[(s.month ?? 3) - 1]} · payout factor ${pct(s.payout_factor ?? 1, 0)}`;
    case 'rsu_refresh': {
      const every = s.vest_every === 3 ? 'quarterly' : s.vest_every === 6 ? 'every half year' : s.vest_every === 12 ? 'yearly' : 'monthly';
      const shape = s.vest_weights?.length ? `, ${shapeLabel(s.vest_weights)} a year` : '';
      return `${fmtMoney(s.amount, s.currency, { cents: false })} granted every ${MONTH_NAMES[(s.month ?? 3) - 1]} · vests ${every} over ${(s.vest_months ?? 48) / 12} years${shape}`;
    }
    default:
      return `${fmtEUR(s.amount)} on ${fmtDate(s.start_date)}`;
  }
}

const KIND_TONE: Record<IncomeKind, 'info' | 'warning' | 'neutral' | 'success'> = {
  salary: 'info', bonus: 'warning', rsu_refresh: 'neutral', one_off: 'neutral',
};
const KIND_CHIP: Record<IncomeKind, string> = {
  salary: 'Contract', bonus: 'Target', rsu_refresh: 'Modelled', one_off: 'One-off',
};

export default function IncomeTab() {
  const { income, incomeFailed, reloadIncome } = usePlanning();
  if (!income) {
    return (
      <Section id="income" title="Expected income">
        {incomeFailed
          ? <ErrorState message="Your expected income couldn’t be loaded." onRetry={reloadIncome} />
          : <LoadingState message="Loading your expected income" lines={4} />}
      </Section>
    );
  }
  return <IncomeTabLoaded income={income} />;
}

function IncomeTabLoaded({ income }: { income: IncomeData }) {
  const { saveProfile, reloadIncome, reloadProjection, projData, setFireInflation } = usePlanning();
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
  const [profile, updateProfile, profileError] = useProfileDraft(income.profile, saveProfile);
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState<IncomeStream | null>(null);
  const [chartMode, setChartMode] = useState<'net' | 'gross'>('net');

  const fullYears = useMemo(() => income.years.filter(y => y.months === 12), [income.years]);
  const [year, setYear] = useState<number>(() => fullYears[0]?.year ?? income.years[0]?.year ?? new Date().getFullYear());
  const Y = income.years.find(y => y.year === year) ?? fullYears[0] ?? income.years[0];
  const chartYears = fullYears.slice(0, 10);
  const next12 = income.months.slice(0, 12);

  const afterStreamChange = () => { reloadIncome(); reloadProjection(); };
  const saveStream = async (s: IncomeStream) => {
    if (s.id) await api.updateIncomeStream(s.id, s);
    else await api.createIncomeStream(s);
    setEditing(null);
    setAdding(null);
    afterStreamChange();
  };
  const removeStream = async (s: IncomeStream) => {
    if (!s.id) return;
    if (!(await confirm({ title: `Remove “${s.name}”?`, body: 'Planning stops counting this income.', confirmLabel: 'Remove' }))) return;
    await api.deleteIncomeStream(s.id);
    afterStreamChange();
  };

  const addMenu = (
    <div className="flex flex-wrap items-center gap-2">
      {(['salary', 'bonus', 'rsu_refresh', 'one_off'] as IncomeKind[]).map(k => (
        <AddChip key={k} onClick={() => { setAdding(newStream(k)); setEditing(null); }}>{KIND_LABEL[k]}</AddChip>
      ))}
    </div>
  );

  if (!income.has_income) {
    return (
      <>
        <Section id="income" title="Expected income" subtitle="What you expect to earn, after income tax and social security, and how much of it you save.">
          {adding
            ? <IncomeStreamEditor stream={adding} onSave={saveStream} onCancel={() => setAdding(null)} />
            : (
              <EmptyState
                title="Describe your pay"
                message="Add your base salary, bonus and RSU refreshes. wealth works out your net income after tax and social security, and every Planning calculator then uses it instead of the last 12 months."
              >
                <div className="mt-2">{addMenu}</div>
              </EmptyState>
            )}
        </Section>
        {confirmDialog}
      </>
    );
  }

  const net = Y ? yearNet(Y) : null;
  const yearSavings = Y?.savings ?? 0;
  const lawLine = !Y ? '' : Y.ceilings_enacted && Y.tariff_enacted
    ? `${Y.year} law as enacted.`
    : Y.ceilings_enacted
      ? `Contribution ceilings are final for ${Y.year}. Tax brackets are the 2026 law raised by ${pct(profile.inflation)} a year, because later tariffs aren’t law yet.`
      : `Assumption: contribution ceilings grow ${pct(profile.wage_growth)} a year from their last final values, tax brackets ${pct(profile.inflation)} a year from 2026; the 45 % threshold stays at 277.826 €.`;

  const costs = profile.savings_mode === 'costs';
  const vestShare = costs || profile.rsu_count_full ? 1 : profile.savings_rate;
  const monthParts = (m: IncomeMonth) => {
    const salary = costs ? m.base_net - m.running_costs : profile.savings_rate * m.base_net;
    const bonus = (costs ? 1 : profile.savings_rate) * (m.bonus_net + m.one_off_net);
    return { salary, bonus, held: m.held_net * vestShare, refresh: m.refresh_net * vestShare };
  };
  const nextSavings = next12.reduce((a, m) => a + m.savings, 0);
  const leanest = next12.length ? Math.min(...next12.map(m => m.savings)) : 0;
  const richest = next12.length ? next12.reduce((best, m) => (m.savings > best.savings ? m : best), next12[0]) : null;
  const refreshDecal = { symbol: 'rect', dashArrayX: [1, 0], dashArrayY: [2, 3], rotation: -Math.PI / 4, color: tc.parchment };

  const yearlyOption = {
    tooltip: { trigger: 'axis' as const, axisPointer: { type: 'shadow' as const }, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
    legend: { bottom: 0, itemWidth: 14 },
    grid: { left: 56, right: 16, top: 24, bottom: 64 },
    xAxis: { type: 'category' as const, data: chartYears.map(y => String(y.year)), axisLabel: { fontSize: 11 } },
    yAxis: { type: 'value' as const, axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
    series: [
      { name: 'Base salary', type: 'bar' as const, stack: 'pay', color: tc.forest, data: chartYears.map(y => Math.round(chartMode === 'net' ? yearNet(y).base : y.base_gross)) },
      { name: 'Bonus and one-offs', type: 'bar' as const, stack: 'pay', color: tc.gold, data: chartYears.map(y => Math.round(chartMode === 'net' ? yearNet(y).bonus : y.bonus_gross + y.one_off_gross)) },
      { name: 'RSUs you hold', type: 'bar' as const, stack: 'pay', color: tc.sage, data: chartYears.map(y => Math.round(chartMode === 'net' ? yearNet(y).held : y.held_gross)) },
      { name: 'RSU refreshes (modelled)', type: 'bar' as const, stack: 'pay', color: tc.sage, itemStyle: { color: tc.sage, opacity: 0.6, decal: refreshDecal }, data: chartYears.map(y => Math.round(chartMode === 'net' ? yearNet(y).refresh : y.refresh_gross)) },
      ...(chartMode === 'net' ? [{
        name: costs ? 'Running costs' : 'Not saved', type: 'line' as const, step: 'middle' as const, symbol: 'none',
        color: tc.ink, lineStyle: { color: tc.ink, width: 2, type: 'dashed' as const },
        data: chartYears.map(y => Math.round(y.net - y.savings)),
      }] : []),
    ],
  };

  const monthlyOption = {
    tooltip: { trigger: 'axis' as const, axisPointer: { type: 'shadow' as const }, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
    legend: { bottom: 0, itemWidth: 14 },
    grid: { left: 56, right: 16, top: 24, bottom: 64 },
    xAxis: { type: 'category' as const, data: next12.map(m => fmtMonthShort(m.month)), axisLabel: { fontSize: 11 } },
    yAxis: { type: 'value' as const, axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
    series: [
      { name: costs ? 'Salary after running costs' : 'Saved from salary', type: 'bar' as const, stack: 'save', color: tc.forest, data: next12.map(m => Math.round(monthParts(m).salary)) },
      { name: 'Bonus and one-offs', type: 'bar' as const, stack: 'save', color: tc.gold, data: next12.map(m => Math.round(monthParts(m).bonus)) },
      { name: 'RSUs you hold', type: 'bar' as const, stack: 'save', color: tc.sage, data: next12.map(m => Math.round(monthParts(m).held)) },
      { name: 'RSU refreshes', type: 'bar' as const, stack: 'save', color: tc.sage, itemStyle: { color: tc.sage, opacity: 0.6, decal: refreshDecal }, data: next12.map(m => Math.round(monthParts(m).refresh)) },
    ],
  };

  const streamsByKind = (k: IncomeKind) => income.streams.filter(s => s.kind === k).length;
  const kindAmounts = (k: IncomeKind): [number, number] | null => {
    if (!Y || !net) return null;
    switch (k) {
      case 'salary': return [Y.base_gross, net.base];
      case 'bonus': return [Y.bonus_gross, Y.bonus_gross * Y.variable_keep];
      case 'one_off': return [Y.one_off_gross, Y.one_off_gross * Y.variable_keep];
      default: return [Y.refresh_gross, net.refresh];
    }
  };
  const held = income.held;
  const p = Y?.payroll;
  const gross = p?.gross ?? 0;
  const ofGross = (v: number) => gross > 0 ? pct(v / gross) : '–';

  return (
    <>
      <Section
        id="income"
        title="Expected income"
        subtitle="What you expect to earn, after income tax and social security, and how much of it you save."
        actions={
          <SelectField label="Year" value={String(year)} onChange={e => setYear(Number(e.target.value))}>
            {income.years.map(y => <option key={y.year} value={y.year}>{y.year}{y.months < 12 ? ` (from ${fmtMonthShort(income.months[0].month)})` : ''}</option>)}
          </SelectField>
        }
      >
        <MetricGrid>
          <MetricCard label={`Gross income ${year}`} value={fmtEUR(p?.gross ?? 0)} sublabel={Y && Y.months < 12 ? 'Months still ahead this year' : 'Base, bonus and RSU vests'} />
          <MetricCard label={`Net income ${year}`} value={fmtEUR(Y?.net ?? 0)} sublabel={`You keep ${pct((p?.net ?? 0) / (gross || 1))} of gross`} />
          <MetricCard label="Planned savings" value={fmtEUR(Y ? yearSavings / Y.months : 0)} tone="positive" sublabel={<>A month on average; <span data-privacy="blur">{fmtEUR(yearSavings)}</span> in {year}, in the months it arrives</>} />
          <MetricCard
            label="Net flow into tracked accounts"
            value={fmtEUR(projData?.default_contribution ?? 0)}
            sublabel="A month over the last 12 months: deposits and vests minus withdrawals, sale wires included. Cash on your Girokonto isn’t in it, so this isn’t the same as planned savings."
          />
        </MetricGrid>

        <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
          <SubHeading>{chartMode === 'net' ? 'Income after tax by year' : 'Gross income by year'}</SubHeading>
          <Toggle label="Show amounts" value={chartMode} onChange={setChartMode} options={[{ id: 'net', label: 'After tax' }, { id: 'gross', label: 'Gross' }]} />
        </div>
        <EChartWrapper option={yearlyOption} height="300px" />
        <p className="mt-1 text-[13px] text-ink-muted">
          {chartMode === 'net' && (costs ? 'The part above the dashed line is what you save. ' : 'The dashed line is what you don’t save. ')}
          Ten years shown; the schedule runs to your retirement age, and salary, bonus and unvested RSUs stop at your FIRE date.
        </p>
      </Section>

      <Section id="income-streams" title="Income streams" subtitle="Grants you hold come from your Morgan Stanley import. A modelled refresh steps aside in any year where a real grant shows up in an import, so no grant counts twice.">
        <ScrollTable label="Income streams">
          <table className="apple-table min-w-[620px]">
            <caption className="sr-only">Income streams with gross and net amounts for {year}</caption>
            <thead>
              <tr>
                <th scope="col">Stream</th>
                <th scope="col" className="num">Gross {year}</th>
                <th scope="col" className="num">Net {year}</th>
                <th scope="col"><span className="sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {income.streams.map(s => {
                const amounts = streamsByKind(s.kind) === 1 ? kindAmounts(s.kind) : null;
                return (
                  <tr key={s.id}>
                    <th scope="row" className={`${ROW_TH} !font-normal`}>
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-semibold">{s.name}</span>
                        <Chip tone={KIND_TONE[s.kind]}>{KIND_CHIP[s.kind]}</Chip>
                      </div>
                      <div className="mt-0.5 text-[13px] text-ink-body" data-privacy="blur">{streamSummary(s)}</div>
                      {s.steps.length > 0 && (
                        <div className="text-[13px] text-ink-muted" data-privacy="blur">
                          {s.steps.map(st => `From ${fmtDate(st.date)}: ${fmtEUR(st.amount)}`).join(' · ')}
                        </div>
                      )}
                    </th>
                    <td className="num font-serif" data-privacy="blur">{amounts ? fmtEUR(amounts[0]) : '–'}</td>
                    <td className="num font-serif" data-privacy="blur">{amounts ? fmtEUR(amounts[1]) : '–'}</td>
                    <td className="text-right whitespace-nowrap">
                      <button type="button" className="min-h-[44px] px-2 text-[14px] font-semibold text-forest underline underline-offset-2" onClick={() => { setEditing(s.id ?? null); setAdding(null); }}>Edit</button>
                      <button type="button" className="min-h-[44px] px-2 text-[14px] font-semibold text-claret underline underline-offset-2" onClick={() => removeStream(s)}>Remove…</button>
                    </td>
                  </tr>
                );
              })}
              {held.to_vest > 0 && (
                <tr>
                  <th scope="row" className={`${ROW_TH} !font-normal`}>
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-semibold">RSUs you hold</span>
                      <Chip tone="success">Granted</Chip>
                    </div>
                    <div className="mt-0.5 text-[13px] text-ink-body">
                      <span data-privacy="blur">{fmtMoney(held.to_vest, held.currency, { cents: false })}</span> still to vest at today’s price{held.last_vest ? `, until ${fmtDate(held.last_vest)}` : ''}
                    </div>
                    <div className="text-[13px] text-ink-muted">From your Morgan Stanley import{held.imported_on ? ` of ${fmtDate(held.imported_on)}` : ''} · taxed like the rest of your pay</div>
                  </th>
                  <td className="num font-serif" data-privacy="blur">{Y && Y.held_gross > 0 ? fmtEUR(Y.held_gross) : '–'}</td>
                  <td className="num font-serif" data-privacy="blur">{Y && net && Y.held_gross > 0 ? fmtEUR(net.held) : '–'}</td>
                  <td className="text-right"><a href="/portfolio" className="min-h-[44px] inline-flex items-center px-2 text-[14px] font-semibold text-forest underline underline-offset-2">In Portfolio</a></td>
                </tr>
              )}
              <tr>
                <th scope="row" className={`${ROW_TH} !font-semibold`}>Total</th>
                <td className="num font-serif font-semibold" data-privacy="blur">{fmtEUR(gross)}</td>
                <td className="num font-serif font-semibold" data-privacy="blur">{fmtEUR(p?.net ?? 0)}</td>
                <td />
              </tr>
            </tbody>
          </table>
        </ScrollTable>

        {editing && (() => {
          const s = income.streams.find(x => x.id === editing);
          return s ? <div className="mt-4"><IncomeStreamEditor key={editing} stream={s} onSave={saveStream} onCancel={() => setEditing(null)} /></div> : null;
        })()}
        {adding && <div className="mt-4"><IncomeStreamEditor stream={adding} onSave={saveStream} onCancel={() => setAdding(null)} /></div>}

        <div className="mt-4 flex flex-col gap-2">
          {addMenu}
          <p className="text-[13px] text-ink-muted">All taxed as wages. Use a salary step change for unpaid leave; rent and other income after FIRE stay under Other income in the FIRE Calculator.</p>
        </div>
      </Section>

      <Section id="income-savings" title="Savings" subtitle="Your Girokonto isn’t imported, so wealth can’t see your spending. Set what you spend, or the share of your pay you save.">
        <Toggle label="How savings are set" value={profile.savings_mode} onChange={v => updateProfile({ savings_mode: v })} options={[{ id: 'costs', label: 'Running costs' }, { id: 'rate', label: 'Savings rate' }]} />
        <div className="mt-4 grid grid-cols-1 gap-x-3 gap-y-4 md:grid-cols-3">
          {costs ? (
            <NumberField label="Running costs" unit="€ / month" value={profile.running_costs_monthly} onValueChange={v => updateProfile({ running_costs_monthly: Math.max(0, v) })}
              hint={`In today’s money; rises with inflation (${pct(profile.inflation)}).`} />
          ) : (<>
            <NumberField label="Savings rate" unit="%" value={Math.round(profile.savings_rate * 1000) / 10} onValueChange={v => updateProfile({ savings_rate: Math.min(100, Math.max(0, v)) / 100 })} hint="Of salary and bonus after tax." />
            <label className="flex min-h-[44px] items-center gap-2.5 self-end text-[15px]">
              <input type="checkbox" className="h-5 w-5 accent-forest" checked={profile.rsu_count_full} onChange={e => updateProfile({ rsu_count_full: e.target.checked })} />
              Vested RSUs count as savings in full
            </label>
          </>)}
        </div>
        <div className="mt-3 max-w-3xl text-[13px] text-ink-body">
          {costs
            ? <>Everything that leaves your Girokonto: rent, insurance, groceries, holidays. Health and care insurance aren’t in it, because payroll pays them now; the FIRE Calculator adds them after FIRE and starts from <span data-privacy="blur">{fmtEUR(income.summary.fire_expenses_today)}</span> a year in today’s money.</>
            : <>With RSUs you save {pct(Y && Y.net > 0 ? yearSavings / Y.net : 0)} of your net income in {year}. The FIRE Calculator starts from what you don’t save of salary and bonus: about <span data-privacy="blur">{fmtEUR(income.summary.fire_expenses_today)}</span> a year.</>}
        </div>
        {costs && profile.running_costs_monthly === 0 && (
          <p className="mt-3 border-l-[3px] border-amber bg-inset px-3 py-2 text-[14px] text-ink-body">Enter your running costs: until then all of your net income counts as savings.</p>
        )}
        {profileError && <p role="alert" className="mt-3 text-[14px] text-claret">Couldn’t save: {profileError}</p>}

        <SubHeading className="mt-6">Savings, next 12 months</SubHeading>
        <EChartWrapper option={monthlyOption} height="280px" />
        <p className="mt-1 text-[13px] text-ink-muted">
          You save <span data-privacy="blur">{fmtEUR(nextSavings)}</span> over the next 12 months, <span data-privacy="blur">{fmtEUR(leanest)}</span> in the leanest month
          {richest && <> and <span data-privacy="blur">{fmtEUR(richest.savings)}</span> in {fmtMonthShort(richest.month)}</>}. Projection and FIRE take each month as it comes.
        </p>
      </Section>

      <Section id="income-tax" title={`Tax and social security, ${year}`}>
        <details className="group">
          <summary className="cursor-pointer min-h-[44px] text-[14px] text-ink-body">
            <span data-privacy="blur">Net {fmtEUR(p?.net ?? 0)}</span> · income tax and surcharges {ofGross((p?.income_tax ?? 0) + (p?.soli ?? 0) + (p?.church_tax ?? 0))} · social security {ofGross((p?.rv ?? 0) + (p?.av ?? 0) + (p?.kv ?? 0) + (p?.pv ?? 0))}
            <span className="ml-2 font-semibold text-forest underline underline-offset-2 group-open:hidden">Show</span>
          </summary>
          <div className="mt-4 grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
            <div className="flex flex-col gap-4">
              <SelectField label="Church tax" value={String(profile.church_rate)} onChange={e => updateProfile({ church_rate: Number(e.target.value) })} hint="Shared with the Tax page.">
                <option value="0">None</option>
                <option value="0.08">8 % (Bavaria, Baden-Württemberg)</option>
                <option value="0.09">9 % (other states)</option>
              </SelectField>
              <div className="flex flex-col gap-1.5">
                <span className="text-[13px] text-ink-body">Health insurance</span>
                <Toggle label="Health insurance" value={profile.health_kind} onChange={v => updateProfile({ health_kind: v })} options={[{ id: 'gkv', label: 'Statutory' }, { id: 'pkv', label: 'Private' }]} />
              </div>
              {profile.health_kind === 'gkv' ? (
                <NumberField label="Zusatzbeitrag of your Krankenkasse" unit="%" decimals={2} value={Math.round(profile.gkv_zusatzbeitrag * 10000) / 100} onValueChange={v => updateProfile({ gkv_zusatzbeitrag: Math.min(10, Math.max(0, v)) / 100 })} hint="2,90 % is the 2026 average; you pay half." />
              ) : (<>
                <NumberField label="Premium incl. care" unit="€ / month" value={profile.pkv_premium_monthly} onValueChange={v => updateProfile({ pkv_premium_monthly: Math.max(0, v) })} />
                <NumberField label="Basic cover share" unit="%" value={Math.round(profile.pkv_basic_share * 100)} onValueChange={v => updateProfile({ pkv_basic_share: Math.min(100, Math.max(0, v)) / 100 })} hint="Tax-deductible part; your insurer states it each year." />
                {p && <p className="text-[13px] text-ink-body">Your employer pays <span data-privacy="blur">{fmtEUR(p.employer_subsidy / 12)}</span> a month: half the premium, at most <span data-privacy="blur">{fmtEUR(p.max_subsidy / 12)}</span>.</p>}
              </>)}
              <p className="text-[13px] text-ink-muted">Children: {profile.children}, from Your Profile.{profile.children === 0 ? ' Without children, long-term care costs 0,6 % more.' : ''} Filing is single.</p>
            </div>

            {p && Y && (
              <div className="flex min-w-0 flex-col gap-4">
                <ScrollTable label={`Gross to net for ${year}`}>
                  <table className="apple-table min-w-[420px]">
                    <caption className="sr-only">Gross to net for {year}</caption>
                    <thead><tr><th scope="col">Line</th><th scope="col" className="num">Amount</th><th scope="col" className="num">Of gross</th></tr></thead>
                    <tbody>
                      <tr><th scope="row" className={`${ROW_TH} !font-semibold`}>Gross income</th><td className="num font-serif" data-privacy="blur">{fmtEUR(p.gross)}</td><td className="num">100,0 %</td></tr>
                      <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Income tax<div className="text-[12px] text-ink-muted">On taxable income of <span data-privacy="blur">{fmtEUR(p.taxable_income)}</span></div></th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.income_tax)}</td><td className="num">{ofGross(p.income_tax)}</td></tr>
                      <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Solidarity surcharge</th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.soli)}</td><td className="num">{ofGross(p.soli)}</td></tr>
                      <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Church tax<div className="text-[12px] text-ink-muted">{profile.church_rate > 0 ? `${pct(profile.church_rate, 0)} of income tax, deductible` : 'Not a church member'}</div></th><td className="num font-serif" data-privacy="blur">{p.church_tax > 0 ? `−${fmtEUR(p.church_tax)}` : fmtEUR(0)}</td><td className="num">{ofGross(p.church_tax)}</td></tr>
                      <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Pension insurance<div className="text-[12px] text-ink-muted">9,3 % up to <span data-privacy="blur">{fmtEUR(Y.rv_cap)}</span></div></th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.rv)}</td><td className="num">{ofGross(p.rv)}</td></tr>
                      <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Unemployment insurance<div className="text-[12px] text-ink-muted">1,3 % up to <span data-privacy="blur">{fmtEUR(Y.rv_cap)}</span></div></th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.av)}</td><td className="num">{ofGross(p.av)}</td></tr>
                      {profile.health_kind === 'gkv' ? (<>
                        <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Health insurance<div className="text-[12px] text-ink-muted">7,3 % + {fmtPct(profile.gkv_zusatzbeitrag * 50, { digits: 2 })} Zusatzbeitrag up to <span data-privacy="blur">{fmtEUR(Y.kv_cap)}</span></div></th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.kv)}</td><td className="num">{ofGross(p.kv)}</td></tr>
                        <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Long-term care insurance<div className="text-[12px] text-ink-muted">Up to <span data-privacy="blur">{fmtEUR(Y.kv_cap)}</span></div></th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.pv)}</td><td className="num">{ofGross(p.pv)}</td></tr>
                      </>) : (
                        <tr><th scope="row" className={`${ROW_TH} !font-normal`}>Private health and care insurance<div className="text-[12px] text-ink-muted">Premium minus <span data-privacy="blur">{fmtEUR(p.employer_subsidy)}</span> employer subsidy</div></th><td className="num font-serif text-claret" data-privacy="blur">−{fmtEUR(p.kv)}</td><td className="num">{ofGross(p.kv)}</td></tr>
                      )}
                      <tr><th scope="row" className={`${ROW_TH} !font-semibold`}>Net income</th><td className="num font-serif font-semibold" data-privacy="blur">{fmtEUR(p.net)}</td><td className="num font-semibold">{ofGross(p.net)}</td></tr>
                    </tbody>
                  </table>
                </ScrollTable>
                <MetricGrid>
                  <MetricCard label="Base salary keeps" money={false} value={pct(Y.base_keep)} />
                  <MetricCard label="Bonus and RSUs keep" money={false} value={pct(Y.variable_keep)} />
                  <MetricCard label="The next euro keeps" money={false} value={pct(Y.marginal_keep)} />
                </MetricGrid>
                <ul className="list-disc pl-5 text-[13px] text-ink-body flex flex-col gap-1">
                  <li>{lawLine}</li>
                  <li>This is your yearly tax bill (the assessment), not the monthly withholding; the difference comes back as a refund or a back-payment.</li>
                  <li>Base salary is taxed first. Bonus and RSUs share the rest of the tax in proportion, so the order of the streams doesn’t matter.</li>
                  <li>Social security on the bonus and vests is counted against the yearly ceiling. Payroll spreads it by month, which only matters when base salary is below a ceiling.</li>
                  {Y.months < 12 && <li>This year is taxed as if its remaining months ran all year.</li>}
                </ul>
              </div>
            )}
          </div>
        </details>
      </Section>

      <Section id="income-assumptions" title="Assumptions">
        <details className="group">
          <summary className="cursor-pointer min-h-[44px] text-[14px] text-ink-body">
            Inflation {pct(profile.inflation)} · wage growth {pct(profile.wage_growth)} · share price until vest {pct(profile.share_drift)} · {fmtNum(profile.usd_per_eur ?? income.usd_per_eur, 2)} USD per €
            <span className="ml-2 font-semibold text-forest underline underline-offset-2 group-open:hidden">Change</span>
          </summary>
          <div className="mt-4 grid grid-cols-1 gap-x-3 gap-y-4 md:grid-cols-2 lg:grid-cols-4">
            <NumberField label="Inflation" unit="% / year" value={Math.round(profile.inflation * 1000) / 10} onValueChange={v => { updateProfile({ inflation: v / 100 }); setFireInflation(String(v)); }} hint="Shared with the FIRE Calculator. Raises running costs and, from 2027, the tax brackets." />
            <NumberField label="Wage growth" unit="% / year" value={Math.round(profile.wage_growth * 1000) / 10} onValueChange={v => updateProfile({ wage_growth: v / 100 })} hint="Raises the contribution ceilings after 2027. The 45 % threshold stays flat." />
            <NumberField label="Share price until vest" unit="% / year" value={Math.round(profile.share_drift * 1000) / 10} onValueChange={v => updateProfile({ share_drift: v / 100 })} hint="For RSUs you hold and refreshes. Once vested, shares earn the projection’s expected return." />
            <NumberField label="Exchange rate" unit="USD per €" decimals={4} value={profile.usd_per_eur ?? income.usd_per_eur} onValueChange={v => updateProfile({ usd_per_eur: v > 0 ? v : null })} hint="Starts at today’s ECB rate; set 0 to follow it again." />
          </div>
        </details>
      </Section>
      {confirmDialog}
    </>
  );
}
