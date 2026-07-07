import { useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';

type LifeEvent = { type: string; date: string; cash_impact: number; contrib_change: number; recurring_cost: number; duration_months: number; tax_impact: number; label: string };

type LifeEventResult = { baseline: { date: string; value: number }[]; with_events: { date: string; value: number }[]; net_impact: number; fire_shift_months: number; templates: { type: string; label: string; description: string; default_cash_impact: number; default_contrib_change: number; default_recurring_cost: number; default_duration_months: number; icon: string }[]; execution_plans?: { type: string; event_label: string; cash_needed?: number; sell_order?: { isin: string; name: string; buy_date?: string; sell_amount: number; tax_due: number; net_proceeds: number; effective_rate_pct: number }[]; total_tax?: number; total_proceeds?: number; windfall_amount?: number; lump_sum_10y?: number; dca_10y?: number; lump_sum_edge_pct?: number }[] };

const DEFAULT_TEMPLATES: LifeEventResult['templates'] = [
  { type: 'home_purchase', label: 'Home Purchase', icon: 'H', default_cash_impact: -80000, default_contrib_change: 0, default_recurring_cost: 1500, default_duration_months: 300, description: '' },
  { type: 'sabbatical', label: 'Sabbatical', icon: 'S', default_cash_impact: 0, default_contrib_change: -2000, default_recurring_cost: 500, default_duration_months: 12, description: '' },
  { type: 'child', label: 'Child', icon: 'C', default_cash_impact: -5000, default_contrib_change: -250, default_recurring_cost: 600, default_duration_months: 216, description: '' },
  { type: 'job_change', label: 'Job Change', icon: 'J', default_cash_impact: 0, default_contrib_change: 500, default_recurring_cost: 0, default_duration_months: 0, description: '' },
  { type: 'inheritance', label: 'Inheritance', icon: 'I', default_cash_impact: 100000, default_contrib_change: 0, default_recurring_cost: 0, default_duration_months: 0, description: '' },
  { type: 'marriage', label: 'Marriage', icon: 'M', default_cash_impact: -15000, default_contrib_change: 0, default_recurring_cost: 0, default_duration_months: 0, description: '' },
  { type: 'early_retirement', label: 'Early Retirement', icon: 'R', default_cash_impact: 0, default_contrib_change: -999999, default_recurring_cost: 3000, default_duration_months: 0, description: '' },
];

const ICONS: Record<string, string> = { home_purchase: 'H', sabbatical: 'S', marriage: 'M', child: 'C', job_change: 'J', inheritance: 'I', early_retirement: 'R' };

// "What-if" event simulator. Each event is (lump_sum, monthly_contrib_change,
// monthly_recurring_cost, duration_months) and the backend at
// POST /api/portfolio/life-events overlays them onto the baseline projection.
// When an event triggers a sell (e.g. home purchase), the response includes
// an execution_plan with the FIFO sell order and tax estimate; for windfalls
// (inheritance) it suggests lump-sum vs DCA.
export default function LifeEventSimulator() {
  const { projData, projContrib, projReturn, fmt } = usePlanning();
  const tc = useThemeColors();
  const [lifeEvents, setLifeEvents] = useState<LifeEvent[]>([]);
  const [lifeEventResult, setLifeEventResult] = useState<LifeEventResult | null>(null);
  const [lifeEventLoading, setLifeEventLoading] = useState(false);

  const templates = lifeEventResult?.templates || DEFAULT_TEMPLATES;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Life Event Simulator</h2>
      <p className="text-[13px] text-ink-muted mb-3">
        Model how major life events affect your net worth trajectory.
      </p>

      <div className="flex flex-wrap gap-2 mb-3">
        {templates.map(t => (
          <button key={t.type} onClick={() => {
            const now = new Date();
            const defaultDate = `${now.getFullYear() + 2}-${String(now.getMonth() + 1).padStart(2, '0')}`;
            setLifeEvents(prev => [...prev, {
              type: t.type, label: t.label, date: defaultDate,
              cash_impact: t.default_cash_impact, contrib_change: t.default_contrib_change,
              recurring_cost: t.default_recurring_cost, duration_months: t.default_duration_months, tax_impact: 0,
            }]);
          }} className="rounded-[8px] bg-parchment-deep px-3 py-1.5 text-[13px] font-medium text-ink-body hover:bg-divider">
            {t.icon} {t.label}
          </button>
        ))}
      </div>

      {lifeEvents.length > 0 && (
        <div className="space-y-2 mb-3">
          {lifeEvents.map((ev, i) => (
            <div key={i} className="rounded-xl bg-parchment-deep px-3 py-2.5">
              <div className="flex items-center justify-between mb-2">
                <span className="text-[15px] font-medium text-ink">{ev.label}</span>
                <button onClick={() => setLifeEvents(prev => prev.filter((_, j) => j !== i))} className="text-claret text-[12px]">Remove</button>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <div>
                  <label className="text-[11px] text-ink-muted">When</label>
                  <input aria-label="Event date" type="month" value={ev.date} onChange={e => {
                    const updated = [...lifeEvents]; updated[i] = { ...ev, date: e.target.value }; setLifeEvents(updated);
                  }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                </div>
                <div>
                  <label className="text-[11px] text-ink-muted">Lump sum</label>
                  <input aria-label="Cash impact" type="number" value={ev.cash_impact} onChange={e => {
                    const updated = [...lifeEvents]; updated[i] = { ...ev, cash_impact: parseFloat(e.target.value) || 0 }; setLifeEvents(updated);
                  }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                </div>
                <div>
                  <label className="text-[11px] text-ink-muted">Monthly savings Δ</label>
                  <input aria-label="Contribution change" type="number" value={ev.contrib_change} onChange={e => {
                    const updated = [...lifeEvents]; updated[i] = { ...ev, contrib_change: parseFloat(e.target.value) || 0 }; setLifeEvents(updated);
                  }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                </div>
                <div>
                  <label className="text-[11px] text-ink-muted">Monthly cost</label>
                  <input aria-label="Recurring cost" type="number" value={ev.recurring_cost} onChange={e => {
                    const updated = [...lifeEvents]; updated[i] = { ...ev, recurring_cost: parseFloat(e.target.value) || 0 }; setLifeEvents(updated);
                  }} className="w-full rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums" />
                </div>
              </div>
            </div>
          ))}

          <button onClick={async () => {
            setLifeEventLoading(true);
            try {
              const contrib = projData ? (parseFloat(projContrib) || projData.contribution) : 0;
              const ret = projData ? (parseFloat(projReturn) || projData.return_pct) : 7;
              const res = await fetch('/api/portfolio/life-events', {
                method: 'POST', headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ events: lifeEvents, contribution: contrib, return_pct: ret }),
              });
              const data = await res.json();
              setLifeEventResult(prev => ({ ...prev, ...data }));
            } catch { /* ignore */ } finally { setLifeEventLoading(false); }
          }} className="w-full apple-btn-primary" disabled={lifeEventLoading}>
            {lifeEventLoading ? 'Simulating...' : 'Simulate Impact'}
          </button>
        </div>
      )}

      {lifeEventResult?.baseline && lifeEventResult.baseline.length > 0 && lifeEvents.length > 0 && (
        <div className="space-y-3">
          <div className="grid grid-cols-2 gap-2">
            <div className="rounded-lg bg-parchment-deep p-2.5">
              <p className="text-[11px] text-ink-muted">30-Year Impact</p>
              <p className={`text-[13px] font-semibold tabular-nums ${(lifeEventResult.net_impact ?? 0) >= 0 ? 'text-sage' : 'text-claret'}`}>
                {(lifeEventResult.net_impact ?? 0) >= 0 ? '+' : ''}{fmt(lifeEventResult.net_impact ?? 0)}
              </p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-2.5">
              <p className="text-[11px] text-ink-muted">FIRE Date Shift</p>
              <p className={`text-[13px] font-semibold tabular-nums ${(lifeEventResult.fire_shift_months ?? 0) <= 0 ? 'text-sage' : 'text-claret'}`}>
                {(lifeEventResult.fire_shift_months ?? 0) === 0 ? 'No change' :
                  `${Math.abs(lifeEventResult.fire_shift_months ?? 0)} mo ${(lifeEventResult.fire_shift_months ?? 0) > 0 ? 'later' : 'earlier'}`}
              </p>
            </div>
          </div>

          {lifeEvents.length > 1 && (() => {
            const sorted = [...lifeEvents].sort((a, b) => a.date.localeCompare(b.date));
            return (
              <div className="rounded-xl bg-parchment-deep px-3 py-2">
                <p className="text-[11px] text-ink-muted mb-1.5">Event Timeline</p>
                <div className="relative h-8">
                  <div className="absolute left-0 right-0 top-1/2 h-px bg-divider" />
                  {sorted.map((ev, i) => {
                    const allDates = sorted.map(e => new Date(e.date + '-01').getTime());
                    const minDate = Math.min(...allDates);
                    const maxDate = Math.max(...allDates);
                    const range = maxDate - minDate || 1;
                    const pos = ((new Date(ev.date + '-01').getTime() - minDate) / range) * 100;
                    const left = Math.max(5, Math.min(95, sorted.length === 1 ? 50 : pos));
                    return (
                      <div key={i} className="absolute -translate-x-1/2 flex flex-col items-center" style={{ left: `${left}%`, top: 0 }}>
                        <span className="text-sm leading-none">{ICONS[ev.type] || '·'}</span>
                        <span className="text-[9px] text-ink-muted mt-0.5 whitespace-nowrap">{ev.date}</span>
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })()}

          {(() => {
            const eventDates = lifeEvents.map(ev => ev.date);
            const markLines = lifeEvents.map(ev => ({
              xAxis: ev.date, label: { formatter: ICONS[ev.type] || ev.label, fontSize: 12, position: 'start' as const },
              lineStyle: { type: 'dashed' as const, color: tc.divider, width: 1 },
            }));
            return (
              <EChartWrapper option={{
                tooltip: { trigger: 'axis' as const, formatter: (params: unknown) => {
                  const ps = params as { seriesName: string; value: number; axisValue: string }[];
                  const evtIdx = eventDates.indexOf(ps[0].axisValue);
                  const evtLabel = evtIdx >= 0 ? `<b>${lifeEvents[evtIdx].label}</b><br/>` : '';
                  return `${evtLabel}${ps[0].axisValue}<br/>${ps.map(p => `${p.seriesName}: ${fmt(p.value)}`).join('<br/>')}`;
                }},
                legend: { data: ['Baseline', 'With Events'], bottom: 0, textStyle: { fontSize: 11 } },
                grid: { top: 20, right: 10, bottom: 30, left: 50 },
                xAxis: { type: 'category' as const, data: lifeEventResult.baseline.map(p => p.date), axisLabel: { fontSize: 10, interval: Math.max(Math.floor(lifeEventResult.baseline.length / 6) - 1, 1) } },
                yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, formatter: (v: number) => v >= 1e6 ? `${(v/1e6).toFixed(1)}M` : v >= 1000 ? `${Math.round(v/1000)}K` : String(v) } },
                series: [
                  { name: 'Baseline', type: 'line' as const, data: lifeEventResult.baseline.map(p => p.value), smooth: true, lineStyle: { width: 2 }, itemStyle: { color: tc.inkMuted }, showSymbol: false },
                  { name: 'With Events', type: 'line' as const, data: lifeEventResult.with_events.map(p => p.value), smooth: true, lineStyle: { width: 2 }, itemStyle: { color: tc.forest }, showSymbol: false,
                    markLine: markLines.length > 0 ? { data: markLines, symbol: 'none' as const, silent: true } : undefined,
                  },
                ],
              }} height="220px" />
            );
          })()}

          {(lifeEventResult.execution_plans ?? []).map((plan, pi) => (
            <div key={pi} className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[15px] font-medium text-ink mb-2">
                {plan.type === 'liquidation' ? 'Sell Order' : 'Invest Strategy'}: {plan.event_label}
              </p>
              {plan.type === 'liquidation' && plan.sell_order && (
                <>
                  <div className="space-y-1.5 mb-2">
                    {plan.sell_order.map((step, si) => (
                      <div key={si} className="flex items-center justify-between text-[12px]">
                        <div className="min-w-0 flex-1">
                          <span className="font-medium text-ink">{step.name}</span>
                          {step.buy_date && <span className="text-ink-muted ml-1 text-[11px]">({step.buy_date.slice(0, 4)})</span>}
                          <span className="text-ink-muted ml-1">{step.effective_rate_pct}% tax</span>
                        </div>
                        <div className="text-right shrink-0 ml-2 tabular-nums">
                          <span className="text-ink">{fmt(step.sell_amount)}</span>
                          <span className="text-claret ml-1">−{fmt(step.tax_due)}</span>
                        </div>
                      </div>
                    ))}
                  </div>
                  <div className="flex justify-between text-[13px] font-medium border-t border-divider pt-1.5">
                    <span className="text-ink-muted">Total tax: <span className="text-claret">{fmt(plan.total_tax ?? 0)}</span></span>
                    <span className="text-ink">Net: {fmt(plan.total_proceeds ?? 0)}</span>
                  </div>
                </>
              )}
              {plan.type === 'invest' && (
                <div className="grid grid-cols-2 gap-2">
                  <div className="rounded-lg bg-parchment p-2">
                    <p className="text-[11px] text-ink-muted">Lump Sum (10Y)</p>
                    <p className="text-[13px] font-semibold tabular-nums text-ink">{fmt(plan.lump_sum_10y ?? 0)}</p>
                  </div>
                  <div className="rounded-lg bg-parchment p-2">
                    <p className="text-[11px] text-ink-muted">12-Mo DCA (10Y)</p>
                    <p className="text-[13px] font-semibold tabular-nums text-ink">{fmt(plan.dca_10y ?? 0)}</p>
                  </div>
                  <div className="col-span-2 text-[12px] text-ink-muted">
                    Lump sum has a <span className={`font-medium ${(plan.lump_sum_edge_pct ?? 0) >= 0 ? 'text-sage' : 'text-claret'}`}>
                      {(plan.lump_sum_edge_pct ?? 0) >= 0 ? '+' : ''}{plan.lump_sum_edge_pct ?? 0}%
                    </span> edge over DCA after 10 years. Historically, lump sum beats DCA ~67% of the time.
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {lifeEvents.length === 0 && (
        <p className="text-[13px] text-ink-muted">
          Add a life event above to see how it impacts your wealth trajectory.
        </p>
      )}
    </div>
  );
}
