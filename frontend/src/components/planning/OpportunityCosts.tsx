import { useEffect, useState } from 'react';
import { usePlanning } from './PlanningContext';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';

type OpportunityCostData = {
  cash_drag: { total_cash: number; excess_cash: number; annual_cost: number; recommended_reserve: number; accounts: { account_name: string; cash_balance: number; opportunity_cost_annual: number }[] };
  timing: { avg_invest_day: number; annual_cost: number };
  rebalance: { annual_cost: number };
  fsa_waste: { unused: number; annual_cost: number };
  total_annual: number;
  monthly_rollup: { month: string; total: number }[];
};

// "Money left on the table" — quantifies four sources of friction:
// cash drag (idle cash above the recommended reserve), timing delay
// (avg day-of-month for investing late in market hours), missed
// rebalancing opportunities, and unused Sparerpauschbetrag (FSA).
// Hidden below 50 EUR/yr total because below that the analysis is noise.
export default function OpportunityCosts() {
  const { fmt } = usePlanning();
  const tc = useThemeColors();
  const [data, setData] = useState<OpportunityCostData | null>(null);

  useEffect(() => {
    fetch('/api/analysis/opportunity-cost').then(r => r.json()).then(setData).catch(() => {});
  }, []);

  if (!data || data.total_annual <= 50) return null;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Opportunity Costs</h2>
      <p className="text-[13px] text-ink-muted mb-3">
        Estimated annual cost of inaction — money left on the table.
      </p>
      <div className="grid grid-cols-2 gap-2 mb-3">
        <div className="rounded-lg bg-parchment-deep p-2.5">
          <p className="text-[11px] text-ink-muted">Cash Drag</p>
          <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(data.cash_drag.annual_cost)}/yr</p>
          <p className="text-[11px] text-ink-muted">{fmt(data.cash_drag.excess_cash)} excess</p>
        </div>
        <div className="rounded-lg bg-parchment-deep p-2.5">
          <p className="text-[11px] text-ink-muted">Timing Delay</p>
          <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(data.timing.annual_cost)}/yr</p>
          <p className="text-[11px] text-ink-muted">Avg day {data.timing.avg_invest_day}</p>
        </div>
        <div className="rounded-lg bg-parchment-deep p-2.5">
          <p className="text-[11px] text-ink-muted">Rebalancing</p>
          <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(data.rebalance.annual_cost)}/yr</p>
        </div>
        <div className="rounded-lg bg-parchment-deep p-2.5">
          <p className="text-[11px] text-ink-muted">FSA Waste</p>
          <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(data.fsa_waste.annual_cost)}/yr</p>
          <p className="text-[11px] text-ink-muted">{fmt(data.fsa_waste.unused)} unused</p>
        </div>
      </div>
      <div className="rounded-xl bg-inset border-l-[3px] border-claret px-3 py-2 text-center">
        <p className="text-[12px] text-ink-muted">Total Annual Opportunity Cost</p>
        <p className="font-serif text-[20px] font-bold tabular-nums text-claret">{fmt(data.total_annual)}</p>
      </div>
      {data.monthly_rollup.length > 0 && (
        <div className="mt-3">
          <EChartWrapper option={{
            tooltip: { trigger: 'axis' as const },
            grid: { top: 10, right: 10, bottom: 25, left: 40 },
            xAxis: { type: 'category' as const, data: data.monthly_rollup.map(m => { const d = new Date(m.month + '-01'); return d.toLocaleDateString('de-DE', { month: 'short' }); }), axisLabel: { fontSize: 10 } },
            yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, formatter: (v: number) => `${Math.round(v)}€` } },
            series: [{ type: 'bar' as const, data: data.monthly_rollup.map(m => m.total), itemStyle: { color: tc.claret, borderRadius: [4, 4, 0, 0] } }],
          }} height="140px" />
        </div>
      )}
    </div>
  );
}
