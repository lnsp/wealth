import { api, type CostData } from '../../api/client';
import EChartWrapper from '../../components/charts/EChartWrapper';
import { Chip, EmptyState, MetricCard, MetricGrid, Section, SectionBoundary, SubHeading } from '../../components/ui';
import { useThemeColors } from '../../hooks/useThemeColors';
import { escapeHtml, fmtAxis, fmtEUR, fmtNum, fmtPct } from '../../utils/format';
import { KeyValues, Loaded, Money, Statement, TableRegion, arr, num, plural, useJobTrigger, useLoad } from './shared';
import type { LoadState } from './shared';

type Alternatives = Awaited<ReturnType<typeof api.getAlternatives>>;

export default function CostsTab() {
  const costs = useLoad(() => api.getCosts());
  return (
    <>
      <p className="mt-5 text-[14px] text-ink-body">What holding your funds costs each year, and what that adds up to over time.</p>
      <SectionBoundary><CostOverview costs={costs} /></SectionBoundary>
      <SectionBoundary><CostProjection costs={costs} /></SectionBoundary>
      <SectionBoundary><TotalCostOfOwnership costs={costs} /></SectionBoundary>
      <SectionBoundary><CheaperAlternatives /></SectionBoundary>
    </>
  );
}

/* ───────────── KPI tiles + fund costs by holding ───────────── */

function CostOverview({ costs }: { costs: LoadState<CostData> }) {
  const refresh = useJobTrigger('/api/settings/refresh-etf-metadata', costs.reload);
  return (
    <Loaded state={costs} lines={6}>
      {data => {
        const holdings = arr(data?.holdings);
        const coveredValue = holdings.reduce((s, h) => s + num(h.value), 0);
        const totalValue = num(data?.total_value);
        const coverage = num(data?.coverage_pct);
        return (
          <>
            {holdings.length > 0 && (
              <MetricGrid className="mt-5">
                <MetricCard label="Annual fund costs" value={fmtEUR(data.annual_cost)} sublabel="At today’s values" />
                <MetricCard money={false} label="Weighted ongoing charge" value={fmtPct(data.weighted_ter_covered_only, { digits: 2 })}
                  sublabel={<>Across {plural(holdings.length, 'fund', 'funds')}, <Money value={coveredValue} /></>} />
                <MetricCard money={false} label="Share of whole portfolio" value={fmtPct(data.weighted_ter, { digits: 2 })}
                  sublabel={coverage < 99.5 ? 'Including holdings without a charge' : 'Every holding has a charge on record'} />
                <MetricCard money={false} label="Data coverage" value={fmtPct(coverage)}
                  sublabel="Of portfolio value has a known charge" />
              </MetricGrid>
            )}

            <Section title="Fund Costs by Holding" subtitle="The ongoing charge (TER, total expense ratio) of each fund, applied to its current value.">
              {holdings.length === 0 ? (
                <EmptyState
                  title="No cost data yet"
                  message="None of your holdings has an ongoing charge on record yet. Fund data is fetched in the background after an import."
                  action={{ label: 'Refresh fund data', onClick: refresh.run }}
                >
                  {refresh.message && <p role="status" className="text-[13px] text-ink-muted">{refresh.message}</p>}
                </EmptyState>
              ) : (
                <>
                  <ul className="md:hidden flex flex-col gap-3">
                    {holdings.map(h => (
                      <li key={h.isin} className="rounded-apple border border-divider px-4 py-3">
                        <p className="mb-2 text-[14px] font-medium text-ink">{h.name}</p>
                        <KeyValues rows={[
                          { label: 'Value', value: <Money value={h.value} /> },
                          { label: 'Ongoing charge', value: fmtPct(h.ter, { digits: 2 }) },
                          { label: 'Cost per year', value: <Money value={h.annual_cost} /> },
                        ]} />
                      </li>
                    ))}
                    <li className="rounded-apple border border-divider bg-inset px-4 py-3">
                      <p className="mb-2 text-[14px] font-semibold text-ink">Total</p>
                      <KeyValues rows={[
                        { label: 'Portfolio value', value: <Money value={totalValue} /> },
                        { label: 'Ongoing charge', value: fmtPct(data.weighted_ter, { digits: 2 }) },
                        { label: 'Cost per year', value: <Money value={data.annual_cost} /> },
                      ]} />
                    </li>
                  </ul>

                  <TableRegion label="Fund costs by holding" className="hidden md:block">
                    <table className="apple-table">
                      <thead>
                        <tr>
                          <th scope="col">Security</th>
                          <th scope="col" className="num">Value</th>
                          <th scope="col" className="num">Ongoing charge</th>
                          <th scope="col" className="num">Cost per year</th>
                        </tr>
                      </thead>
                      <tbody>
                        {holdings.map(h => (
                          <tr key={h.isin}>
                            <td className="font-medium text-ink">{h.name}</td>
                            <td className="num"><Money value={h.value} /></td>
                            <td className="num">{fmtPct(h.ter, { digits: 2 })}</td>
                            <td className="num"><Money value={h.annual_cost} className="text-ink" /></td>
                          </tr>
                        ))}
                        {totalValue - coveredValue > 0.5 && (
                          <tr>
                            <td className="font-medium text-ink">Other holdings</td>
                            <td className="num"><Money value={totalValue - coveredValue} /></td>
                            <td className="num">No charge on record</td>
                            <td className="num"><Money value={0} /></td>
                          </tr>
                        )}
                        <tr className="bg-inset">
                          <td className="font-semibold text-ink">Total</td>
                          <td className="num font-semibold text-ink"><Money value={totalValue} /></td>
                          <td className="num">{fmtPct(data.weighted_ter, { digits: 2 })}</td>
                          <td className="num font-semibold text-ink"><Money value={data.annual_cost} /></td>
                        </tr>
                      </tbody>
                    </table>
                  </TableRegion>

                  {data.benchmark && (
                    <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-1 text-[13px] text-ink-body">
                      <Chip tone="info">Cost grade {data.benchmark.grade}</Chip>
                      <span>
                        {data.benchmark.detail}. Your charge of {fmtPct(data.benchmark.your_ter, { digits: 2 })} compares with about {fmtPct(data.benchmark.avg_ter, { digits: 2 })} for a typical German fund investor
                        {num(data.benchmark.annual_saving) > 0 && <>, a saving of <Money value={data.benchmark.annual_saving} /> a year</>}.
                      </span>
                    </div>
                  )}
                </>
              )}
            </Section>
          </>
        );
      }}
    </Loaded>
  );
}

/* ───────────── Cost drag projection ───────────── */

function CostProjection({ costs }: { costs: LoadState<CostData> }) {
  const tc = useThemeColors();
  return (
    <Section title="Cost Drag Projection" subtitle="Fund costs added up over 1 to 30 years at today’s values, with no growth assumed.">
      <Loaded state={costs} lines={4}>
        {data => {
          const projection = arr(data?.projection);
          if (projection.length === 0 || num(data?.annual_cost) <= 0) {
            return <Statement>No projection yet. It appears once at least one fund has an ongoing charge on record.</Statement>;
          }
          const at = (year: number) => projection.find(p => p.year === year)?.cumulative ?? num(data.annual_cost) * year;
          return (
            <>
              <MetricGrid>
                <MetricCard label="After 10 years" value={fmtEUR(at(10))} />
                <MetricCard label="After 20 years" value={fmtEUR(at(20))} />
                <MetricCard label="After 30 years" value={fmtEUR(at(30))} />
                <MetricCard label="Per day" value={fmtEUR(data.daily_cost, { cents: true })} sublabel="At today’s values" />
              </MetricGrid>
              <SubHeading className="mt-6 mb-2">Cumulative Fee Drag</SubHeading>
              <EChartWrapper option={{
                grid: { top: 28, right: 16, bottom: 28, left: 52 },
                xAxis: { type: 'category' as const, data: projection.map(p => plural(p.year, 'year', 'years')) },
                yAxis: { type: 'value' as const, min: 0, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
                series: [{
                  type: 'bar' as const,
                  name: 'Cumulative fund costs',
                  data: projection.map(p => p.cumulative),
                  color: tc.forest,
                  itemStyle: { color: tc.forest, borderRadius: [2, 2, 0, 0] },
                  barMaxWidth: 48,
                  label: { show: true, position: 'top' as const, fontSize: 11, color: tc.inkBody, formatter: (p: unknown) => fmtEUR((p as { value: number }).value) },
                }],
                tooltip: {
                  trigger: 'axis' as const,
                  formatter: (p: unknown) => {
                    const a = p as { name: string; value: number }[];
                    if (!Array.isArray(a) || a.length === 0) return '';
                    return `${escapeHtml(a[0].name)}: ${fmtEUR(a[0].value)}`;
                  },
                },
              }} height="220px" />
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Total cost of ownership ───────────── */

function TotalCostOfOwnership({ costs }: { costs: LoadState<CostData> }) {
  return (
    <Section title="Total Cost of Ownership" subtitle="Fund charges plus what trading costs you in a typical year.">
      <Loaded state={costs} lines={3}>
        {data => {
          const t = data?.total_cost_ownership;
          if (!t) return <Statement>No trading costs on record yet.</Statement>;
          return (
            <>
              <MetricGrid>
                <MetricCard label="Fund charges" value={fmtEUR(t.ter_cost)} sublabel="Per year" />
                <MetricCard label="Transaction fees" value={fmtEUR(t.transaction_fees)} sublabel="Per year, averaged" />
                <MetricCard label="Bid-ask spread" value={fmtEUR(t.spread_estimate)} sublabel="Estimated at 0,05 % of volume" />
                <MetricCard label="Total per year" value={fmtEUR(t.total_annual)} sublabel={`${fmtPct(t.total_annual_pct, { digits: 2 })} of the portfolio`} />
              </MetricGrid>
              <p className="mt-3 text-[13px] text-ink-muted">
                Since your first trade: <Money value={t.lifetime_fees} /> in fees across {fmtNum(t.transaction_count)} {t.transaction_count === 1 ? 'transaction' : 'transactions'}, on a volume of <Money value={t.lifetime_volume} />.
              </p>
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Cheaper alternatives ───────────── */

function CheaperAlternatives() {
  const alternatives = useLoad<Alternatives>(() => api.getAlternatives());
  return (
    <Section title="Cheaper Alternatives" subtitle="Lower-cost ETFs that track a similar index.">
      <Loaded state={alternatives} lines={3}>
        {data => {
          const list = arr(data?.alternatives).filter(h => arr(h.alternatives).length > 0);
          if (list.length === 0) return <Statement>No cheaper alternative on record for the funds you hold.</Statement>;
          return (
            <div className="flex flex-col gap-5">
              {list.map(h => (
                <div key={h.isin}>
                  <p className="text-[14px] font-medium text-ink">
                    {h.name} <span className="font-normal text-ink-muted">· ongoing charge {fmtPct(h.current_ter, { digits: 2 })}</span>
                  </p>
                  <ul className="mt-1 divide-y divide-divider border-y border-divider">
                    {arr(h.alternatives).map(a => (
                      <li key={a.name} className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1 py-2 text-[13px]">
                        <span className="min-w-0">
                          <span className="text-ink">{a.name}</span>
                          <span className="text-ink-muted"> · {fmtPct(a.ter, { digits: 2 })}, {fmtNum(num(h.current_ter) - num(a.ter), 2)} points lower</span>
                        </span>
                        <span className="text-ink-body">
                          Saves <Money value={a.annual_saving} className="text-ink" /> a year, <Money value={a.ten_year_saving} /> over 10 years
                        </span>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          );
        }}
      </Loaded>
    </Section>
  );
}
