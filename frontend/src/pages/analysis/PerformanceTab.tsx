import { api } from '../../api/client';
import EChartWrapper from '../../components/charts/EChartWrapper';
import { EmptyState, MetricCard, MetricGrid, ProgressBar, Section, SectionBoundary, SubHeading } from '../../components/ui';
import { useThemeColors } from '../../hooks/useThemeColors';
import { escapeHtml, fmtAxis, fmtDateLong, fmtEUR, fmtMonthShort, fmtNum, fmtPct } from '../../utils/format';
import { Loaded, Statement, arr, lineLegend, num, plural, rec, toneOf, useJobTrigger, useLoad } from './shared';

type Benchmark = Awaited<ReturnType<typeof api.getBenchmarkComparison>>;
type Inflation = Awaited<ReturnType<typeof api.getInflation>>;

const BENCHMARK_MIN_PRICES = 30;

export default function PerformanceTab() {
  return (
    <>
      <SectionBoundary><BenchmarkComparison /></SectionBoundary>
      <SectionBoundary><PurchasingPower /></SectionBoundary>
      <SectionBoundary><ExchangeRates /></SectionBoundary>
    </>
  );
}

/** Tooltip for charts whose x axis holds "YYYY-MM" months and whose values are EUR. */
function monthMoneyTooltip(params: unknown): string {
  const ps = params as { seriesName: string; value: number; marker: string; axisValue: string }[];
  if (!Array.isArray(ps) || ps.length === 0) return '';
  return [escapeHtml(fmtMonthShort(ps[0].axisValue)), ...ps.map(p => `${p.marker} ${escapeHtml(p.seriesName)}: ${fmtEUR(p.value)}`)].join('<br/>');
}

/* ───────────── What if you had bought the benchmark ───────────── */

function BenchmarkComparison() {
  const tc = useThemeColors();
  // This request belongs to this tab only; no other tab or page fires it.
  const bench = useLoad<Benchmark>(() => api.getBenchmarkComparison());
  const backfill = useJobTrigger('/api/settings/refresh-historical-prices', bench.reload);
  const name = bench.data?.benchmark_name?.trim();
  return (
    <Section first title={name && bench.data?.available !== false ? `What If You Had Bought ${name}?` : 'What If You Had Bought the Benchmark?'}
      subtitle="Replays every deposit and withdrawal you made through a single benchmark fund.">
      <Loaded state={bench} lines={6}>
        {data => {
          const points = arr(data?.comparison);
          if (data?.available === false) {
            const need = num(data.need) || BENCHMARK_MIN_PRICES;
            const have = Math.min(num(data.have), need);
            return (
              <EmptyState
                title="Not enough benchmark history"
                message={`${name || 'The benchmark'} needs ${need} daily prices before your deposits can be replayed through it.`}
                action={{ label: 'Load historical prices', onClick: backfill.run }}
              >
                <div className="w-full max-w-xs">
                  <p className="mb-1.5 text-[13px] font-medium text-ink">{have} of {plural(need, 'price', 'prices')}</p>
                  <ProgressBar value={have} max={need} label="Benchmark prices loaded" />
                </div>
                {backfill.message && <p role="status" className="text-[13px] text-ink-muted">{backfill.message}</p>}
              </EmptyState>
            );
          }
          if (points.length <= 3) {
            return (
              <EmptyState
                title="Not enough history to compare"
                message="The comparison needs at least four months of transactions and net worth history."
                action={{ label: 'Import transactions', href: '/transactions' }}
              />
            );
          }
          const benchName = name || 'Benchmark';
          return (
            <>
              <MetricGrid>
                <MetricCard label="Your portfolio" value={fmtEUR(data.actual_value)} sublabel="Net worth today" />
                <MetricCard label={benchName} value={fmtEUR(data.benchmark_value)} sublabel="Same deposits, benchmark only" />
                <MetricCard label="Difference" value={fmtEUR(data.difference, { sign: true })} tone={toneOf(num(data.difference))}
                  sublabel={num(data.difference) >= 0 ? 'You are ahead of the benchmark' : 'You are behind the benchmark'} />
              </MetricGrid>
              <SubHeading className="mt-6 mb-2">Value Over Time</SubHeading>
              <EChartWrapper option={{
                tooltip: { trigger: 'axis' as const, formatter: monthMoneyTooltip },
                legend: lineLegend([{ name: 'Your portfolio', style: 'solid' }, { name: benchName, style: 'dashed' }]),
                xAxis: { type: 'category' as const, data: points.map(c => c.date), boundaryGap: false, axisLabel: { formatter: (v: string) => fmtMonthShort(v) } },
                yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
                series: [
                  { name: 'Your portfolio', type: 'line' as const, data: points.map(c => c.actual), smooth: 0.3, showSymbol: false, color: tc.forest, itemStyle: { color: tc.forest }, lineStyle: { color: tc.forest, width: 2 } },
                  { name: benchName, type: 'line' as const, data: points.map(c => c.benchmark), smooth: 0.3, showSymbol: false, color: tc.walnut, itemStyle: { color: tc.walnut }, lineStyle: { color: tc.walnut, width: 2, type: 'dashed' as const } },
                ],
                grid: { left: 52, right: 16, top: 12, bottom: 44 },
              }} height="280px" />
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Purchasing power ───────────── */

function PurchasingPower() {
  const tc = useThemeColors();
  const inflation = useLoad<Inflation>(() => api.getInflation());
  return (
    <Section title="Purchasing Power" subtitle="Net worth as recorded (nominal) and in today’s euros (real), adjusted with the German HVPI consumer price index.">
      <Loaded state={inflation} lines={6}>
        {data => {
          const history = arr(data?.history);
          if (history.length <= 3) {
            return (
              <EmptyState
                title="Not enough net worth history"
                message="Purchasing power compares at least four months of net worth with consumer prices."
                action={{ label: 'Import transactions', href: '/transactions' }}
              />
            );
          }
          const hasReturns = data.nominal_return != null && data.real_return != null;
          return (
            <>
              {hasReturns && (
                <MetricGrid>
                  <MetricCard money={false} label="Nominal change" value={fmtPct(data.nominal_return, { sign: true })} tone={toneOf(num(data.nominal_return))} sublabel="As recorded" />
                  <MetricCard money={false} label="Real change" value={fmtPct(data.real_return, { sign: true })} tone={toneOf(num(data.real_return))} sublabel="After inflation" />
                  <MetricCard label="Lost to inflation" value={fmtEUR(Math.max(0, num(data.purchasing_power_lost)))} sublabel="In today’s euros" />
                </MetricGrid>
              )}
              <SubHeading className={`${hasReturns ? 'mt-6' : ''} mb-2`}>Nominal and Real Net Worth</SubHeading>
              <EChartWrapper option={{
                tooltip: { trigger: 'axis' as const, formatter: monthMoneyTooltip },
                legend: lineLegend([{ name: 'Nominal', style: 'solid' }, { name: 'Real (today’s euros)', style: 'dashed' }]),
                xAxis: { type: 'category' as const, data: history.map(h => h.date), boundaryGap: false, axisLabel: { formatter: (v: string) => fmtMonthShort(v) } },
                yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
                series: [
                  { name: 'Nominal', type: 'line' as const, data: history.map(h => h.nominal), smooth: 0.3, showSymbol: false, color: tc.forest, itemStyle: { color: tc.forest }, lineStyle: { color: tc.forest, width: 2 } },
                  { name: 'Real (today’s euros)', type: 'line' as const, data: history.map(h => h.real), smooth: 0.3, showSymbol: false, color: tc.gold, itemStyle: { color: tc.gold }, lineStyle: { color: tc.gold, width: 2, type: 'dashed' as const } },
                ],
                grid: { left: 52, right: 16, top: 12, bottom: 44 },
              }} height="260px" />
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Exchange rates ───────────── */

function ExchangeRates() {
  const tc = useThemeColors();
  const fx = useLoad(() => api.getFXHistory());
  const backfill = useJobTrigger('/api/settings/refresh-historical-prices', fx.reload);
  return (
    <Section title="Exchange Rates" subtitle="How much foreign currency 1 € buys. A falling line means the euro has weakened, which lifts the euro value of holdings in that currency.">
      <Loaded state={fx} lines={5}>
        {data => {
          const rates = rec(data?.rates);
          const pairs = Object.keys(rates)
            .map(currency => ({
              currency,
              points: arr(rates[currency])
                .filter(p => num(p.rate) > 0)
                .map(p => [new Date(p.date).getTime(), p.rate] as [number, number])
                .filter(p => Number.isFinite(p[0]))
                .sort((a, b) => a[0] - b[0]),
            }))
            .filter(p => p.points.length > 10)
            .sort((a, b) => a.currency.localeCompare(b.currency));
          if (pairs.length === 0) {
            return (
              <EmptyState
                title="No exchange rate history yet"
                message="Rates are loaded for each foreign currency you hold. With only euro holdings there is nothing to show."
                action={{ label: 'Load historical prices', onClick: backfill.run }}
              >
                {backfill.message && <p role="status" className="text-[13px] text-ink-muted">{backfill.message}</p>}
              </EmptyState>
            );
          }
          // One small chart per currency, each on its own scale. A shared plot needed one y axis
          // per currency (their names overprinted) and a shared date axis, which left every line
          // as isolated points whenever the currencies were quoted on different days.
          return (
            <div className={`grid gap-x-8 gap-y-6 ${pairs.length > 1 ? 'md:grid-cols-2' : ''}`}>
              {pairs.map(pair => {
                const first = pair.points[0];
                const last = pair.points[pair.points.length - 1];
                const change = first[1] > 0 ? (last[1] / first[1] - 1) * 100 : 0;
                const digits = last[1] >= 20 ? 2 : 4;
                if (first[1] === last[1] && pair.points.every(p => p[1] === first[1])) {
                  return (
                    <div key={pair.currency}>
                      <SubHeading className="mb-2">EUR/{pair.currency}</SubHeading>
                      <Statement>1 € has bought {fmtNum(last[1], digits)} {pair.currency} throughout, with no change since {fmtDateLong(first[0])}.</Statement>
                    </div>
                  );
                }
                return (
                  <div key={pair.currency} className="min-w-0">
                    <SubHeading>EUR/{pair.currency}</SubHeading>
                    <p className="mb-1 text-[13px] text-ink-muted">
                      1 € = {fmtNum(last[1], digits)} {pair.currency} on {fmtDateLong(last[0])} · {fmtPct(change, { sign: true })} since {fmtDateLong(first[0])}
                    </p>
                    <EChartWrapper option={{
                      tooltip: {
                        trigger: 'axis' as const,
                        formatter: (params: unknown) => {
                          const ps = params as { data: [number, number] }[];
                          if (!Array.isArray(ps) || ps.length === 0) return '';
                          return `${escapeHtml(fmtDateLong(ps[0].data[0]))}<br/>1 € = ${fmtNum(ps[0].data[1], digits)} ${escapeHtml(pair.currency)}`;
                        },
                      },
                      xAxis: { type: 'time' as const, splitNumber: 4, axisLabel: { hideOverlap: true, formatter: (v: number) => fmtMonthShort(v) } },
                      yAxis: { type: 'value' as const, scale: true, splitNumber: 3, axisLabel: { formatter: (v: number) => fmtNum(v, last[1] >= 20 ? 0 : 2) } },
                      series: [{
                        name: `EUR/${pair.currency}`, type: 'line' as const, data: pair.points,
                        showSymbol: false, color: tc.forest, itemStyle: { color: tc.forest }, lineStyle: { color: tc.forest, width: 1.5 },
                      }],
                      grid: { left: 52, right: 16, top: 12, bottom: 28 },
                    }} height="180px" />
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
