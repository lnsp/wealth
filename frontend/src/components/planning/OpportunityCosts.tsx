import { useCallback, useEffect, useState } from 'react';
import { useThemeColors } from '../../hooks/useThemeColors';
import EChartWrapper from '../charts/EChartWrapper';
import { Section, SubHeading, MetricCard, MetricGrid, LoadingState, ErrorState } from '../ui';
import { fmtEUR, fmtAxis, fmtMonthShort } from '../../utils/format';
import MoneyLeftOnTable from './MoneyLeftOnTable';

type OpportunityCostData = {
  cash_drag: { total_cash: number; excess_cash: number; annual_cost: number; recommended_reserve: number };
  timing: { avg_invest_day: number; annual_cost: number };
  rebalance: { annual_cost: number };
  fsa_waste: { unused: number; annual_cost: number };
  total_annual: number;
  monthly_rollup: { month: string; total: number }[];
};

/** Only a body with every field the view reads is stored — an error body never reaches render. */
function isOpportunityCostData(body: unknown): body is OpportunityCostData {
  if (!body || typeof body !== 'object') return false;
  const b = body as Record<string, unknown>;
  const obj = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object';
  return obj(b.cash_drag) && typeof b.cash_drag.annual_cost === 'number'
    && obj(b.timing) && typeof b.timing.annual_cost === 'number'
    && obj(b.rebalance) && typeof b.rebalance.annual_cost === 'number'
    && obj(b.fsa_waste) && typeof b.fsa_waste.annual_cost === 'number'
    && typeof b.total_annual === 'number'
    && Array.isArray(b.monthly_rollup);
}

/** Costs are claret only when there is a cost; zero is ink. */
const costTone = (v: number) => (v > 0 ? 'negative' as const : 'neutral' as const);

// "Money left on the table" — one section for both views of it:
//  1. costs of inaction from GET /api/analysis/opportunity-cost: cash drag
//     (idle cash above the recommended reserve), timing delay (investing late
//     in the month), rebalancing drift, and unused Sparer-Pauschbetrag;
//  2. unused tax-advantaged accounts (MoneyLeftOnTable.tsx).
// Below 50 EUR/yr the first analysis is noise, so it is summarised in a sentence.
export default function OpportunityCosts() {
  const tc = useThemeColors();
  const [data, setData] = useState<OpportunityCostData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    setLoading(true);
    setError(null);
    fetch('/api/analysis/opportunity-cost')
      .then(async r => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        const body: unknown = await r.json();
        if (!isOpportunityCostData(body)) throw new Error('Unexpected response from the server');
        setData(body);
      })
      .catch(e => { setData(null); setError(e instanceof Error ? e.message : 'Request failed'); })
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => { load(); }, [load]);

  const yearEnd = data && data.monthly_rollup.length > 0 ? data.monthly_rollup[data.monthly_rollup.length - 1].total : 0;

  return (
    <Section
      id="opportunity-costs"
      title="Money Left on the Table"
      subtitle="Opportunity costs: what idle cash, late investing and unused tax breaks cost you each year. All figures are estimates."
    >
      <SubHeading>Costs of inaction, per year</SubHeading>
      {loading && <LoadingState message="Loading opportunity costs" lines={3} />}
      {error && (
        <div className="mt-3">
          <ErrorState message="Opportunity costs couldn’t be loaded." details={error} onRetry={load} />
        </div>
      )}
      {data && data.total_annual <= 50 && (
        <p className="mt-2 text-[14px] text-ink-body">
          Under 50 € a year in total — nothing here is worth acting on.
        </p>
      )}
      {data && data.total_annual > 50 && (
        <>
          <MetricGrid className="mt-3">
            <MetricCard
              label="Idle cash"
              tone={costTone(data.cash_drag.annual_cost)}
              value={fmtEUR(data.cash_drag.annual_cost)}
              sublabel={<><span data-privacy="blur">{fmtEUR(data.cash_drag.excess_cash)}</span> above a reserve of <span data-privacy="blur">{fmtEUR(data.cash_drag.recommended_reserve)}</span> (5 % of your wealth, at least 5.000 €)</>}
            />
            <MetricCard
              label="Investing late in the month"
              tone={costTone(data.timing.annual_cost)}
              value={fmtEUR(data.timing.annual_cost)}
              sublabel={`You invest on day ${data.timing.avg_invest_day} of the month on average; compared with day 1`}
            />
            <MetricCard
              label="Not rebalancing"
              tone={costTone(data.rebalance.annual_cost)}
              value={fmtEUR(data.rebalance.annual_cost)}
              sublabel="Assumes 2 % lower return on the part that has drifted from your target weights"
            />
            <MetricCard
              label="Unused tax allowance"
              tone={costTone(data.fsa_waste.annual_cost)}
              value={fmtEUR(data.fsa_waste.annual_cost)}
              sublabel={<><span data-privacy="blur">{fmtEUR(data.fsa_waste.unused)}</span> of your saver’s allowance (Sparer-Pauschbetrag) is expected to go unused this year</>}
            />
            <MetricCard
              label="Total per year"
              tone={costTone(data.total_annual)}
              value={fmtEUR(data.total_annual)}
              sublabel="Sum of the four estimates"
            />
          </MetricGrid>

          {data.monthly_rollup.length > 0 && (
            <div className="mt-6">
              <SubHeading>How the cost adds up over the year</SubHeading>
              <p className="mt-1 text-[13px] text-ink-muted">
                Cumulative, not per month: each bar is the running total since January, reaching <span data-privacy="blur">{fmtEUR(yearEnd)}</span> by December.
              </p>
              <EChartWrapper
                option={{
                  color: [tc.claret],
                  tooltip: { trigger: 'axis' as const, valueFormatter: (v: unknown) => fmtEUR(Number(v)) },
                  grid: { top: 32, right: 12, bottom: 28, left: 48 },
                  xAxis: { type: 'category' as const, data: data.monthly_rollup.map(m => fmtMonthShort(`${m.month}-01`)), axisLabel: { fontSize: 11 } },
                  yAxis: { type: 'value' as const, name: 'Cumulative cost (€)', nameTextStyle: { align: 'left' as const }, min: 0, axisLabel: { fontSize: 11, formatter: (v: number) => fmtAxis(v) } },
                  series: [{
                    name: 'Cumulative cost', type: 'bar' as const, data: data.monthly_rollup.map(m => m.total),
                    color: tc.claret, itemStyle: { color: tc.claret, borderRadius: [2, 2, 0, 0] },
                  }],
                }}
                height="200px"
              />
            </div>
          )}
        </>
      )}

      <div className="mt-8">
        <MoneyLeftOnTable />
      </div>
    </Section>
  );
}
