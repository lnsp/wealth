import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import {
  Section, SubHeading, MetricCard, MetricGrid, Field, NumberField, AddChip,
  EmptyState, ErrorState, useConfirm,
} from '../ui';
import { escapeHtml, fmtEUR, fmtPct, fmtNum, fmtAxis } from '../../utils/format';
import { ItemCard, RemoveButton, monthLabel, toNum } from './shared';
import { parsePct } from './planningMath';
import { DEFAULT_INFLATION_PCT, DEFAULT_RETURN_PCT } from './constants';

type LifeEvent = {
  type: string; date: string; cash_impact: number; contrib_change: number; recurring_cost: number;
  duration_months: number; tax_impact: number; label: string;
  /** Early retirement: contributions stop at the event date (sent as minus the current contribution). */
  stops_contributions: boolean;
};

type ExecutionPlan = {
  type: string; event_label: string; cash_needed?: number;
  sell_order?: { isin: string; name: string; buy_date?: string; sell_amount: number; tax_due: number; net_proceeds: number; effective_rate_pct: number }[];
  total_tax?: number; total_proceeds?: number; windfall_amount?: number;
  lump_sum_10y?: number; dca_10y?: number; lump_sum_edge_pct?: number;
};

type LifeEventResult = {
  baseline: { date: string; value: number }[];
  with_events: { date: string; value: number }[];
  net_impact: number;
  fire_shift_months: number;
  fire_target_known?: boolean;
  fire_baseline_month?: number;
  fire_with_events_month?: number;
  fire_target?: number;
  execution_plans?: ExecutionPlan[];
};

type Template = { type: string; label: string; cash: number; contrib: number; recurring: number; months: number; stopsContributions?: boolean };

// Defaults mirror the backend templates. Early retirement is modelled explicitly: saving stops
// and living costs are drawn from the portfolio — no sentinel value in a visible field.
const TEMPLATES: Template[] = [
  { type: 'home_purchase', label: 'Home purchase', cash: -80000, contrib: 0, recurring: 1500, months: 300 },
  { type: 'sabbatical', label: 'Sabbatical', cash: 0, contrib: -2000, recurring: 500, months: 12 },
  { type: 'child', label: 'Child', cash: -5000, contrib: -250, recurring: 600, months: 216 },
  { type: 'job_change', label: 'Job change', cash: 0, contrib: 500, recurring: 0, months: 0 },
  { type: 'inheritance', label: 'Inheritance', cash: 100000, contrib: 0, recurring: 0, months: 0 },
  { type: 'marriage', label: 'Marriage', cash: -15000, contrib: 0, recurring: 0, months: 0 },
  { type: 'early_retirement', label: 'Early retirement', cash: 0, contrib: 0, recurring: 3000, months: 0, stopsContributions: true },
];

function isLifeEventResult(body: unknown): body is LifeEventResult {
  if (!body || typeof body !== 'object') return false;
  const b = body as Record<string, unknown>;
  return Array.isArray(b.baseline) && Array.isArray(b.with_events)
    && (b.execution_plans === undefined || b.execution_plans === null || Array.isArray(b.execution_plans));
}

// "What-if" event simulator. Each event is (lump_sum, monthly_contrib_change,
// monthly_recurring_cost, duration_months) and the backend at
// POST /api/portfolio/life-events overlays them onto the baseline projection
// of cash and investments (a home bought or a loan taken is not part of it).
// The independence shift uses the FIRE Calculator's inputs and target. When
// an event needs cash (e.g. home purchase), the response includes an
// execution_plan: oldest lots first within each depot (FIFO), the cheapest
// depot/security first across them; for windfalls it compares lump-sum vs DCA.
export default function LifeEventSimulator() {
  const {
    projData, projContrib, projReturn, fireExpenses, otherIncome, pensionIncome, fireSWR, fireInflation,
    pensionCurrentAge, pensionAge, churchRate, joint, contribSource,
  } = usePlanning();
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
  const [events, setEvents] = useState<LifeEvent[]>([]);
  const [result, setResult] = useState<LifeEventResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // 0 % return and 0 € contributions are valid inputs, not "use the default".
  const contrib = projData ? parsePct(projContrib, projData.contribution) : 0;
  const ret = parsePct(projReturn, projData?.return_pct ?? DEFAULT_RETURN_PCT);
  const fireSpend = Math.max(toNum(fireExpenses) - toNum(otherIncome), 0);

  const addEvent = (t: Template) => {
    const now = new Date();
    const defaultDate = `${now.getFullYear() + 2}-${String(now.getMonth() + 1).padStart(2, '0')}`;
    setEvents(prev => [...prev, {
      type: t.type, label: t.label, date: defaultDate,
      cash_impact: t.cash, contrib_change: t.contrib,
      recurring_cost: t.recurring, duration_months: t.months, tax_impact: 0,
      stops_contributions: !!t.stopsContributions,
    }]);
  };
  const update = (i: number, patch: Partial<LifeEvent>) => {
    setEvents(prev => prev.map((ev, j) => (j === i ? { ...ev, ...patch } : ev)));
  };
  const remove = async (i: number) => {
    const ev = events[i];
    if (!(await confirm({ title: `Remove “${ev.label}”?`, body: 'The values you entered for this event will be lost.', confirmLabel: 'Remove event' }))) return;
    setEvents(prev => prev.filter((_, j) => j !== i));
  };

  const simulate = async () => {
    if (!projData && contribSource !== 'income' && events.some(ev => ev.stops_contributions)) {
      setError('Your current contribution could not be loaded, so stopping it cannot be simulated. Reload the page and try again.');
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const res = await fetch('/api/portfolio/life-events', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          events, // the backend stops the scheduled or flat contribution itself
          source: contribSource,
          contribution: contribSource === 'custom' && projData ? contrib : undefined,
          return_pct: ret,
          expenses: fireSpend,
          swr: parsePct(fireSWR, 3.5),
          pension: toNum(pensionIncome),
          current_age: pensionCurrentAge,
          pension_age: pensionAge,
          inflation_pct: parsePct(fireInflation, DEFAULT_INFLATION_PCT),
          church_rate: churchRate,
          joint,
        }),
      });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body: unknown = await res.json();
      if (!isLifeEventResult(body)) throw new Error('Unexpected response from the server');
      setResult(body);
    } catch (e) {
      setResult(null);
      setError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setLoading(false);
    }
  };

  const hasResult = !!result && result.baseline.length > 0 && events.length > 0;
  const netImpact = result?.net_impact ?? 0;
  const shift = result?.fire_shift_months ?? 0;
  const fireKnown = result?.fire_target_known ?? false;
  const baseFire = result?.fire_baseline_month ?? -1;
  const eventsFire = result?.fire_with_events_month ?? -1;
  const fireValue = !fireKnown ? 'Not set'
    : baseFire < 0 && eventsFire < 0 ? 'Not within 50 years'
      : baseFire >= 0 && eventsFire < 0 ? 'No longer within 50 years'
        : baseFire < 0 ? 'Now within 50 years'
          : shift === 0 ? 'No change' : `${fmtNum(Math.abs(shift))} months ${shift > 0 ? 'later' : 'earlier'}`;
  const fireTone = !fireKnown ? 'neutral' as const
    : (baseFire >= 0 && eventsFire < 0) || shift > 0 ? 'negative' as const
      : (baseFire < 0 && eventsFire >= 0) || shift < 0 ? 'positive' as const : 'neutral' as const;
  const sorted = [...events].sort((a, b) => a.date.localeCompare(b.date));

  return (
    <Section
      id="life-events"
      title="Life Event Simulator"
      subtitle="See how a major life event would change your cash and investments over the next 30 years."
    >
      <div className="flex flex-wrap gap-2">
        {TEMPLATES.map(t => (
          <AddChip key={t.type} onClick={() => addEvent(t)}>{t.label}</AddChip>
        ))}
      </div>

      {events.length === 0 && (
        <div className="mt-4">
          <EmptyState
            title="No events yet"
            message="Add an event above, adjust its amounts, then simulate the effect on your cash and investments."
            action={{ label: 'Add a home purchase', onClick: () => addEvent(TEMPLATES[0]) }}
          />
        </div>
      )}

      {events.length > 0 && (
        <div className="mt-4 flex flex-col gap-3">
          {events.map((ev, i) => (
            <ItemCard key={i}>
              <div className="mb-3 flex items-center justify-between gap-3">
                <span className="min-w-0 text-[15px] font-medium text-ink">{ev.label}</span>
                <RemoveButton name={ev.label} onClick={() => remove(i)} />
              </div>
              <div className="grid grid-cols-1 gap-x-3 gap-y-4 sm:grid-cols-2">
                <Field label="When" type="month" value={ev.date} onChange={e => update(i, { date: e.target.value })} />
                <NumberField
                  label="One-off amount"
                  unit="€"
                  value={ev.cash_impact}
                  onValueChange={v => update(i, { cash_impact: v })}
                  hint="Negative for a cost, positive for money received."
                />
                {ev.stops_contributions ? (
                  <div className="flex flex-col gap-1.5">
                    <span className="text-[13px] font-medium text-ink">Monthly savings</span>
                    <p className="flex min-h-[44px] items-center text-[14px] text-ink-body">
                      <span>Stop completely from this date (today: <span data-privacy="blur">{fmtEUR(contrib)}</span> a month).</span>
                    </p>
                  </div>
                ) : (
                  <NumberField
                    label="Change in monthly savings"
                    unit="€ / month"
                    value={ev.contrib_change}
                    onValueChange={v => update(i, { contrib_change: v })}
                    hint="Negative if you will save less."
                  />
                )}
                <NumberField
                  label={ev.stops_contributions ? 'Monthly living costs from the portfolio' : 'Monthly cost'}
                  unit="€ / month"
                  value={ev.recurring_cost}
                  onValueChange={v => update(i, { recurring_cost: v })}
                />
                <NumberField
                  label="Monthly cost lasts"
                  unit="months"
                  integer
                  value={ev.duration_months}
                  onValueChange={v => update(i, { duration_months: Math.max(0, v) })}
                  hint="0 means it never ends."
                />
              </div>
            </ItemCard>
          ))}

          <button type="button" onClick={simulate} className="apple-btn-primary min-h-[44px] w-full md:w-auto md:self-start" disabled={loading}>
            {loading ? 'Simulating…' : 'Simulate impact'}
          </button>
        </div>
      )}

      {error && (
        <div className="mt-4">
          <ErrorState message="The simulation couldn’t be run." details={error} onRetry={simulate} />
        </div>
      )}

      {hasResult && result && (
        <div className="mt-6">
          <MetricGrid>
            <MetricCard
              label="Effect after 30 years"
              tone={netImpact > 0 ? 'positive' : netImpact < 0 ? 'negative' : 'neutral'}
              value={fmtEUR(netImpact, { sign: true })}
              sublabel="Cash and investments with events minus without"
            />
            <MetricCard
              label="Financial independence"
              money={false}
              tone={fireTone}
              value={fireValue}
              sublabel={fireKnown
                ? <>Against the FIRE Calculator’s target{(result.fire_target ?? 0) > 0 && <> (<span data-privacy="blur">{fmtEUR(result.fire_target ?? 0)}</span> in today’s money)</>}</>
                : 'Enter your annual expenses in the FIRE Calculator to see the effect on your independence date'}
            />
          </MetricGrid>

          <SubHeading className="mt-6">Cash and investments with and without the events</SubHeading>
          {(() => {
            const dates = result.baseline.map(p => p.date);
            // Events sit between quarterly points: mark the first point on or after the event.
            const markLines = sorted.map((ev, n) => {
              const idx = dates.findIndex(d => d >= ev.date);
              return idx < 0 ? null : {
                xAxis: idx,
                label: { formatter: String(n + 1), fontSize: 12, color: tc.inkBody, position: 'end' as const },
                lineStyle: { type: 'dashed' as const, color: tc.inkMuted, width: 1 },
              };
            }).filter((m): m is NonNullable<typeof m> => m !== null);
            return (
              <>
                <EChartWrapper
                  option={{
                    tooltip: {
                      trigger: 'axis' as const,
                      formatter: (params: unknown) => {
                        const ps = params as { seriesName: string; value: number; axisValue: string }[];
                        if (!Array.isArray(ps) || ps.length === 0) return '';
                        return `${escapeHtml(ps[0].axisValue)}<br/>${ps.map(p => `${escapeHtml(p.seriesName)}: ${fmtEUR(p.value)}`).join('<br/>')}`;
                      },
                    },
                    legend: {
                      bottom: 0,
                      itemWidth: 16,
                      data: [
                        { name: 'Without events', icon: 'path://M0 4h5v2h-5zM7 4h5v2h-5zM14 4h5v2h-5z' },
                        { name: 'With events', icon: 'path://M0 4h19v2h-19z' },
                      ],
                    },
                    grid: { top: 28, right: 16, bottom: 44, left: 52 },
                    xAxis: {
                      type: 'category' as const,
                      data: dates.map(d => monthLabel(d, true)),
                      axisLabel: { fontSize: 11, interval: Math.max(Math.floor(dates.length / 6) - 1, 1) },
                    },
                    yAxis: { type: 'value' as const, name: '€', axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
                    series: [
                      {
                        name: 'Without events', type: 'line' as const, data: result.baseline.map(p => p.value), smooth: true, showSymbol: false,
                        color: tc.inkMuted, itemStyle: { color: tc.inkMuted }, lineStyle: { color: tc.inkMuted, width: 2, type: 'dashed' as const },
                      },
                      {
                        name: 'With events', type: 'line' as const, data: result.with_events.map(p => p.value), smooth: true, showSymbol: false,
                        color: tc.forest, itemStyle: { color: tc.forest }, lineStyle: { color: tc.forest, width: 2 },
                        markLine: markLines.length > 0 ? { data: markLines, symbol: 'none' as const, silent: true } : undefined,
                      },
                    ],
                  }}
                  height="260px"
                />
                <ol className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[13px] text-ink-body">
                  {sorted.map((ev, n) => (
                    <li key={n}><span className="font-semibold text-ink">{n + 1}</span> {ev.label}, {monthLabel(ev.date)}</li>
                  ))}
                </ol>
              </>
            );
          })()}
          <p className="mt-2 text-[13px] text-ink-muted">
            Assumes contributions of <span data-privacy="blur">{fmtEUR(contrib)}</span> a month and {fmtPct(ret)} growth a year — the values from the FIRE Calculator.
            A home or car you buy and the loan for it are not included: a down payment shows as money leaving your cash and investments.
          </p>

          {(result.execution_plans ?? []).map((plan, pi) => (
            <div key={pi} className="mt-6">
              <SubHeading>{plan.type === 'liquidation' ? 'What to sell for' : 'How to invest'}: {plan.event_label}</SubHeading>
              {plan.type === 'liquidation' && Array.isArray(plan.sell_order) && (
                <>
                  <ul className="mt-2 border-t border-divider">
                    {plan.sell_order.map((step, si) => (
                      <li key={si} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5 border-b border-divider py-2.5 text-[14px]">
                        <div className="min-w-0 flex-1 basis-48">
                          <span className="font-medium text-ink break-words">{step.name}</span>
                          <span className="block text-[13px] text-ink-muted">
                            {step.buy_date ? `Bought ${step.buy_date.slice(0, 4)} · ` : ''}{fmtPct(step.effective_rate_pct)} tax on the gain
                          </span>
                        </div>
                        <div className="text-right">
                          <span className="text-ink" data-privacy="blur">Sell {fmtEUR(step.sell_amount)}</span>
                          <span className="block text-[13px] text-ink-muted" data-privacy="blur">tax {fmtEUR(step.tax_due)}</span>
                        </div>
                      </li>
                    ))}
                  </ul>
                  <MetricGrid className="mt-3">
                    <MetricCard label="Tax on the sales" value={fmtEUR(plan.total_tax ?? 0)} sublabel="Gains and losses netted; share losses only against share gains" />
                    <MetricCard label="Received after tax" value={fmtEUR(plan.total_proceeds ?? 0)} />
                  </MetricGrid>
                  <p className="mt-2 text-[13px] text-ink-muted">
                    Within each depot the oldest shares must be sold first (FIFO); across depots and securities the sale with the least tax per euro raised comes first.
                    The saver’s allowance is not counted, in case it is already used.
                  </p>
                </>
              )}
              {plan.type === 'invest' && (
                <>
                  <MetricGrid className="mt-3">
                    <MetricCard label="All at once" value={fmtEUR(plan.lump_sum_10y ?? 0)} sublabel="Value after 10 years" />
                    <MetricCard label="Spread over 12 months" value={fmtEUR(plan.dca_10y ?? 0)} sublabel="Value after 10 years" />
                  </MetricGrid>
                  <p className="mt-3 text-[13px] text-ink-body">
                    Investing all at once ends {fmtPct(Math.abs(plan.lump_sum_edge_pct ?? 0))} {(plan.lump_sum_edge_pct ?? 0) >= 0 ? 'ahead of' : 'behind'} spreading it out after 10 years.
                    Historically, investing at once has come out ahead about two times in three.
                  </p>
                </>
              )}
            </div>
          ))}
        </div>
      )}
      {confirmDialog}
    </Section>
  );
}
