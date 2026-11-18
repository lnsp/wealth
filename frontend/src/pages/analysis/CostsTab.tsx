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
                        {data.benchmark.detail}. Your funds’ weighted charge of {fmtPct(data.benchmark.your_ter, { digits: 2 })} compares with about {fmtPct(data.benchmark.avg_ter, { digits: 2 })} for a typical German fund investor
                        {num(data.benchmark.annual_saving) > 0 && <>: <Money value={data.benchmark.annual_saving} /> a year less on <Money value={data.benchmark.funds_value ?? coveredValue} /> in funds</>}
                        {num(data.benchmark.annual_saving) < 0 && <>: <Money value={-num(data.benchmark.annual_saving)} /> a year more on <Money value={data.benchmark.funds_value ?? coveredValue} /> in funds</>}.
                        {coverage < 99.5 && ' Single stocks and funds without a charge on record are left out of the grade.'}
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

const PROJECTION_MILESTONES = new Set([1, 5, 10, 20, 30]);

function CostProjection({ costs }: { costs: LoadState<CostData> }) {
  const tc = useThemeColors();
  return (
    <Section title="Cost Drag Projection" subtitle="What fund charges take out of your portfolio over 1 to 30 years, including the growth the charged money would have earned.">
      <Loaded state={costs} lines={4}>
        {data => {
          const growth = num(data?.projection_growth_pct) || 5;
          const projection = arr(data?.projection);
          if (projection.length === 0 || num(data?.annual_cost) <= 0) {
            return <Statement>No projection yet. It appears once at least one fund has an ongoing charge on record.</Statement>;
          }
          const at = (year: number) => projection.find(p => p.year === year)?.cumulative;
          const assumption = `At ${fmtPct(growth, { digits: 0 })} growth a year`;
          return (
            <>
              <MetricGrid>
                <MetricCard label="After 10 years" value={fmtEUR(at(10))} sublabel={assumption} />
                <MetricCard label="After 20 years" value={fmtEUR(at(20))} sublabel={assumption} />
                <MetricCard label="After 30 years" value={fmtEUR(at(30))} sublabel={assumption} />
                <MetricCard label="Per day" value={fmtEUR(data.daily_cost, { cents: true })} sublabel="At today’s values" />
              </MetricGrid>
              <SubHeading className="mt-6 mb-2">Cumulative Fee Drag</SubHeading>
              <EChartWrapper option={{
                grid: { top: 28, right: 24, bottom: 28, left: 52 },
                xAxis: {
                  type: 'value' as const, min: 0, max: 31, interval: 5,
                  axisLabel: { formatter: (v: number) => (v > 0 && v <= 30 ? `Year ${v}` : '') },
                  splitLine: { show: false },
                },
                yAxis: { type: 'value' as const, min: 0, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
                series: [{
                  type: 'bar' as const,
                  name: 'Fee drag',
                  data: projection.map(p => [p.year, p.cumulative]),
                  color: tc.forest,
                  itemStyle: { color: tc.forest, borderRadius: [2, 2, 0, 0] },
                  barMaxWidth: 10,
                  label: {
                    show: true, position: 'top' as const, fontSize: 11, color: tc.inkBody,
                    formatter: (p: unknown) => {
                      const [year, value] = (p as { value: [number, number] }).value;
                      return PROJECTION_MILESTONES.has(year) ? fmtEUR(value) : '';
                    },
                  },
                }],
                tooltip: {
                  trigger: 'axis' as const,
                  formatter: (p: unknown) => {
                    const a = p as { value: [number, number] }[];
                    if (!Array.isArray(a) || a.length === 0) return '';
                    const [year, value] = a[0].value;
                    return `After ${escapeHtml(plural(year, 'year', 'years'))}: ${fmtEUR(value)}`;
                  },
                },
              }} height="220px" />
              <p className="mt-2 text-[12px] text-ink-muted">
                Assumes the funds you hold today and {fmtPct(growth, { digits: 0 })} growth a year before costs. Without growth, today’s charges would add up to <Money value={num(data.annual_cost) * 30} /> over 30 years.
              </p>
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
    <Section title="Total Cost of Ownership" subtitle="Fund charges plus what trading and custody cost you in a typical year.">
      <Loaded state={costs} lines={3}>
        {data => {
          const t = data?.total_cost_ownership;
          if (!t) return <Statement>No fund charges or trades on record yet.</Statement>;
          const years = num(t.years);
          return (
            <>
              <MetricGrid>
                <MetricCard label="Fund charges" value={fmtEUR(t.ter_cost)} sublabel="Per year" />
                <MetricCard label="Fees and custody" value={fmtEUR(t.transaction_fees)}
                  sublabel={years > 0 ? `Per year, averaged over ${fmtNum(years, 1)} years` : 'Per year'} />
                <MetricCard label="Bid-ask spread" value={fmtEUR(t.spread_estimate)} sublabel="Estimated at 0,05 % of volume" />
                <MetricCard label="Total per year" value={fmtEUR(t.total_annual)} sublabel={`${fmtPct(t.total_annual_pct, { digits: 2 })} of the portfolio`} />
              </MetricGrid>
              {num(t.transaction_count) > 0 && (
                <p className="mt-3 text-[13px] text-ink-muted">
                  Since your first trade: <Money value={t.lifetime_fees} /> in fees and custody charges, {fmtNum(t.transaction_count)} {t.transaction_count === 1 ? 'trade' : 'trades'} on a volume of <Money value={t.lifetime_volume} />.
                </p>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Cheaper alternatives ───────────── */

/** What switching would cost in tax, in one sentence. */
function switchTaxNote(h: Alternatives['alternatives'][number]) {
  if (h.switch_tax == null) return 'The purchase cost is not on record, so the tax on switching is not estimated.';
  if (num(h.switch_tax) <= 0) return 'Selling now realises no taxable gain, so switching costs no tax.';
  return null;
}

function CheaperAlternatives() {
  const alternatives = useLoad<Alternatives>(() => api.getAlternatives());
  return (
    <Section title="Cheaper Alternatives" subtitle="Lower-cost ETFs on the same or a similar index, and what switching costs in tax.">
      <Loaded state={alternatives} lines={3}>
        {data => {
          const list = arr(data?.alternatives).filter(h => arr(h.alternatives).length > 0);
          if (list.length === 0) return <Statement>No cheaper alternative on record for the funds you hold.</Statement>;
          const growth = num(data?.growth_pct) || 5;
          return (
            <div className="flex flex-col gap-5">
              {list.map(h => {
                const note = switchTaxNote(h);
                return (
                  <div key={h.isin}>
                    <p className="text-[14px] font-medium text-ink">
                      {h.name} <span className="font-normal text-ink-muted">· ongoing charge {fmtPct(h.current_ter, { digits: 2 })}{h.index ? ` · ${h.index}` : ''}</span>
                    </p>
                    <p className="mt-1 text-[13px] text-ink-body">
                      {note ?? <>Selling <Money value={h.value} /> now realises a gain of <Money value={h.unrealized_gain} /> and about <Money value={h.switch_tax} className="text-ink" /> in tax (Teilfreistellung applied, saver’s allowance not counted). Keeping the fund defers that tax until you sell.</>}
                    </p>
                    <ul className="mt-2 divide-y divide-divider border-y border-divider">
                      {arr(h.alternatives).map(a => (
                        <li key={a.isin} className="flex flex-col gap-1 py-2 text-[13px]">
                          <span className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                            <span className="min-w-0">
                              <span className="text-ink">{a.name}</span>
                              <span className="text-ink-muted"> · {fmtPct(a.ter, { digits: 2 })}, {fmtNum(num(h.current_ter) - num(a.ter), 2)} points lower</span>
                            </span>
                            <span className="text-ink-body">
                              Saves <Money value={a.annual_saving} className="text-ink" /> a year, <Money value={a.ten_year_saving} /> over 10 years before tax
                            </span>
                          </span>
                          <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-ink-muted">
                            {a.same_index
                              ? <Chip tone="success">Same index</Chip>
                              : <Chip tone="warning" title="Returns will differ from your fund's">Different index{a.index ? `: ${a.index}` : ''}</Chip>}
                            {a.ten_year_net_after_tax != null && (
                              <span>
                                After the switch tax: {a.breakeven_years != null
                                  ? <>ahead after {plural(a.breakeven_years, 'year', 'years')}</>
                                  : <>not ahead within 50 years</>}
                                {' '}(after 10 years <Money value={a.ten_year_net_after_tax} sign />, at {fmtPct(growth, { digits: 0 })} growth).
                              </span>
                            )}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                );
              })}
            </div>
          );
        }}
      </Loaded>
    </Section>
  );
}
