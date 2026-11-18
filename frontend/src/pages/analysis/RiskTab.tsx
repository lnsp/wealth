import { useState } from 'react';
import { api, type ConcentrationAlert, type RiskData } from '../../api/client';
import EChartWrapper from '../../components/charts/EChartWrapper';
import {
  Chip, EmptyState, Field, MetricCard, MetricGrid, PeriodSelector, Section, SectionBoundary, SelectField,
  filterByPeriod, formatDateForPeriod, periodCutoff,
} from '../../components/ui';
import type { Period } from '../../components/ui';
import { useThemeColors } from '../../hooks/useThemeColors';
import { escapeHtml, fmtDate, fmtDateLong, fmtDayMonth, fmtEUR, fmtMonthShort, fmtNum, fmtPct, labelFor } from '../../utils/format';
import {
  Callout, KeyValues, Loaded, Money, Statement, SubHeadingRow, TableRegion,
  arr, getJson, lineLegend, mixHex, num, plural, postJson, rec, useLoad,
} from './shared';

interface VolContext {
  elevated: boolean;
  /** Why the flag is set: 'deep_drawdown' | 'drawdown' | 'volatility' | ''. */
  reason?: string;
  drawdown_pct?: number;
  peak_date?: string;
  vol_30d?: number;
  avg_recovery_days?: number;
  message?: string;
  past_drawdowns?: { from_date: string; trough_date: string; recovery_date: string; max_drawdown_pct: number; recovery_days: number }[] | null;
}

interface StressTest {
  scenarios?: { name: string; period: string; description: string }[] | null;
  portfolio_impact?: { name: string; drawdown_pct: number; drawdown_eur: number; recovery_months: number; worst_month_pct: number; worst_month_eur: number }[] | null;
  holding_impacts?: { name: string; isin: string; value: number; is_equity: boolean; equity_pct?: number; is_crypto?: boolean; drawdown_eur: number; drawdown_pct: number }[] | null;
  dca_recovery?: { name: string; without_dca_months: number; with_dca_months: number; acceleration_months: number }[] | null;
  /** Net contributions per month over the last 12 months, the rate used for "with monthly investing". */
  monthly_contribution?: number;
  portfolio_value?: number;
  equity_pct?: number;
  crypto_pct?: number;
  cash_buffer?: number;
  cash_buffer_months?: number;
  monthly_spending?: number;
  /** 'recorded': average checking withdrawals; 'assumed': 5 % of securities a year. */
  spending_basis?: 'recorded' | 'assumed';
  spending_months?: number;
}

interface SparplanStreak {
  current_streak: number; longest_streak: number; total_months: number; active_months: number; missed_months: number;
  consistency_pct: number; avg_monthly: number; missed_cost: number; total_invested: number; assumed_return_pct?: number;
}

/**
 * Largest peak-to-trough fall inside a window of return-index levels, in % (positive).
 * Example: levels 100, 120, 90, 130, 117 → the fall from 120 to 90 is 25 %; the later
 * 130 → 117 (10 %) is smaller, so { pct: 25, start: date of 120, end: date of 90 }.
 */
function periodMaxDrawdown(points: { date: string; value?: number }[]): { pct: number; start: string; end: string; days: number } {
  let peak = 0;
  let peakDate = '';
  let best = { pct: 0, start: '', end: '', days: 0 };
  for (const p of points) {
    const v = num(p.value);
    if (v <= 0) continue;
    if (v >= peak) { peak = v; peakDate = p.date; continue; }
    const pct = (1 - v / peak) * 100;
    if (pct > best.pct) {
      const days = Math.max(0, Math.round((new Date(p.date).getTime() - new Date(peakDate).getTime()) / 86_400_000));
      best = { pct, start: peakDate, end: p.date, days };
    }
  }
  return best;
}

/** The elevated-volatility sentence, worded from the backend's reason so numbers use the page's formatting. */
function volMessage(v: VolContext): string {
  const dd = Math.abs(num(v.drawdown_pct));
  const episodes = arr(v.past_drawdowns).length;
  switch (v.reason) {
    case 'deep_drawdown':
      return `Returns are ${fmtPct(dd)} below their peak of ${fmtDateLong(v.peak_date)}.`
        + (episodes > 0 ? ` The portfolio has recovered from ${plural(episodes, 'drawdown', 'drawdowns')} of more than 5 % before, in ${plural(num(v.avg_recovery_days), 'day', 'days')} on average.` : '');
    case 'drawdown':
      return `Returns are ${fmtPct(dd)} below their peak. This is within normal market fluctuation.`;
    case 'volatility':
      return `30-day volatility is elevated at ${fmtPct(v.vol_30d, { digits: 0 })} (annualised). Markets are choppy, but returns are near their peak.`;
    default:
      return v.message ?? '';
  }
}

interface Journal {
  entries?: { id: string; date: string; action_type: string; reason: string; outcome?: string; days_ago: number }[] | null;
}

export default function RiskTab() {
  return (
    <>
      <SectionBoundary><RiskAnalytics /></SectionBoundary>
      <SectionBoundary><Concentration /></SectionBoundary>
      <SectionBoundary><StressTestSection /></SectionBoundary>
      <SectionBoundary><SparplanConsistency /></SectionBoundary>
      <SectionBoundary><DecisionJournal /></SectionBoundary>
    </>
  );
}

/* ───────────── Risk analytics: KPIs, drawdown, rolling volatility ───────────── */

function RiskAnalytics() {
  const risk = useLoad(() => api.getRisk());
  return (
    <Section first title="Risk Analytics" subtitle="Volatility, drawdown and risk-adjusted return of your cash and securities, from daily valuations with deposits, withdrawals and share grants taken out. Property, vehicles and their loans are not included.">
      <Loaded state={risk} lines={6}>
        {data => (num(data?.risk?.annualized_volatility) > 0
          ? <RiskBody data={data} />
          : <EmptyState
              title="Not enough history yet"
              message="Risk figures need a few weeks of daily valuations of your cash and securities. They appear once enough history has built up."
              action={{ label: 'Import transactions', href: '/transactions' }}
            />)}
      </Loaded>
    </Section>
  );
}

function RiskBody({ data }: { data: RiskData }) {
  const tc = useThemeColors();
  const [drawdownPeriod, setDrawdownPeriod] = useState<Period>('All');
  const [rollingPeriod, setRollingPeriod] = useState<Period>('All');
  const vol = useLoad(() => getJson<VolContext>('/api/analysis/volatility-context'));
  const r = data.risk;
  const bench = data.benchmark_risk;

  const series = filterByPeriod(arr(r.drawdown_series), drawdownPeriod);
  // The Max Drawdown tile matches the chart's selected period: the largest peak-to-trough
  // fall inside the window, measured on the index levels the backend sends with each point
  // (the backend's downsampling keeps every bucket's peak and trough).
  const dd = drawdownPeriod === 'All'
    ? { pct: num(r.max_drawdown), start: r.max_drawdown_start, end: r.max_drawdown_end, days: num(r.max_drawdown_days) }
    : periodMaxDrawdown(series);

  const ratioTone = (v: number) => (v < 0 ? 'negative' : 'neutral') as 'negative' | 'neutral';
  const benchLine = (text: string) => (bench ? `Benchmark ${text}` : undefined);

  const drawdownOption = series.length > 1 ? {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const p = params as { data: [number, number] }[];
        if (!Array.isArray(p) || p.length === 0) return '';
        return `${escapeHtml(fmtDateLong(p[0].data[0]))}<br/>Below peak: ${fmtPct(p[0].data[1], { digits: 2 })}`;
      },
    },
    // Time axis: one tick per month or day, so labels never repeat or collide.
    xAxis: {
      type: 'time' as const,
      axisLabel: { hideOverlap: true, formatter: (v: number) => formatDateForPeriod(new Date(v).toISOString(), drawdownPeriod) },
    },
    yAxis: { type: 'value' as const, max: 0, axisLabel: { formatter: (v: number) => fmtPct(v, { digits: 0 }) } },
    series: [{
      type: 'line' as const,
      name: 'Drawdown',
      data: series.map(d => [new Date(d.date).getTime(), d.drawdown] as [number, number]),
      color: tc.claret,
      areaStyle: { color: mixHex(tc.parchment, tc.claret, 0.18) },
      lineStyle: { color: tc.claret, width: 1.5 },
      itemStyle: { color: tc.claret },
      symbol: 'none',
    }],
    grid: { left: 52, right: 16, top: 16, bottom: 32 },
  } : null;

  // Each rolling window carries its own [timestamp, volatility] pairs on a time axis. A shared
  // category axis would leave gaps: the backend samples each window at a different step.
  const cutoff = periodCutoff(rollingPeriod);
  const rolling = rec(data.rolling);
  const windows = (['30d', '90d', '365d'] as const)
    .map((w, i) => ({
      name: `${w.replace('d', '')}-day`,
      style: (['solid', 'dashed', 'dotted'] as const)[i],
      colour: [tc.forest, tc.gold, tc.slate][i],
      width: [2, 2, 2.5][i],
      points: arr(rolling[w])
        .filter(p => !cutoff || new Date(p.date) >= cutoff)
        .map(p => [new Date(p.date).getTime(), p.volatility] as [number, number]),
    }))
    .filter(w => w.points.length > 1);
  const shortRange = rollingPeriod === '1M' || rollingPeriod === '3M';

  const past = arr(vol.data?.past_drawdowns);

  return (
    <>
      {num(r.current_drawdown) > 0 && (
        <p className="mb-4 text-[14px] text-ink-body">
          Returns are currently {fmtPct(r.current_drawdown)} below their peak of {fmtDateLong(r.peak_date || r.ath_date)}. Deposits and withdrawals do not count towards this.
        </p>
      )}

      <MetricGrid>
        <MetricCard money={false} label="Volatility (annualised)" value={fmtPct(r.annualized_volatility, { digits: 2 })}
          sublabel={benchLine(fmtPct(bench?.annualized_volatility, { digits: 2 }))} />
        <MetricCard money={false} label="Sharpe ratio" value={fmtNum(r.sharpe_ratio, 2)} tone={ratioTone(r.sharpe_ratio)}
          sublabel={benchLine(fmtNum(bench?.sharpe_ratio, 2))} />
        <MetricCard money={false} label="Sortino ratio" value={fmtNum(r.sortino_ratio, 2)} tone={ratioTone(r.sortino_ratio)}
          sublabel={benchLine(fmtNum(bench?.sortino_ratio, 2))} />
        <MetricCard money={false} label={`Max drawdown${drawdownPeriod !== 'All' ? ` (${drawdownPeriod})` : ''}`}
          value={fmtPct(-Math.abs(dd.pct), { digits: 2 })} tone={dd.pct > 0 ? 'negative' : 'neutral'}
          sublabel={dd.pct > 0 && dd.start && dd.end
            ? `${fmtDate(dd.start)} to ${fmtDate(dd.end)}${dd.days > 0 ? ` (${plural(dd.days, 'day', 'days')})` : ''}`
            : 'No drawdown in this period'} />
        <MetricCard label="Value at risk (95 %, 1 day)" value={fmtEUR(r.value_at_risk_95)} sublabel="Loss exceeded on 1 trading day in 20" />
      </MetricGrid>
      {data.risk_free_rate != null && (
        <p className="mt-3 text-[13px] text-ink-muted">
          Sharpe and Sortino are measured against the ECB deposit rate, which averaged {fmtPct(data.risk_free_rate, { digits: 2 })} over this period.
        </p>
      )}

      {vol.data?.elevated && volMessage(vol.data) && (
        <div className="mt-5">
          <Callout tone="warning" label="Elevated volatility" title="Market context">
            <p>{volMessage(vol.data)}</p>
            <p className="mt-1 text-ink-muted">
              Now {fmtPct(vol.data.drawdown_pct)} from the peak · 30-day volatility {fmtPct(vol.data.vol_30d, { digits: 0 })} · {plural(past.length, 'earlier drawdown', 'earlier drawdowns')} recovered
            </p>
          </Callout>
        </div>
      )}

      <SubHeadingRow actions={<PeriodSelector value={drawdownPeriod} onChange={setDrawdownPeriod} />}>Drawdown Over Time</SubHeadingRow>
      {drawdownOption
        ? <EChartWrapper option={drawdownOption} height="220px" />
        : <Statement>No drawdown data in this period. Choose a longer period.</Statement>}

      <SubHeadingRow actions={<PeriodSelector value={rollingPeriod} onChange={setRollingPeriod} />}>Rolling Volatility</SubHeadingRow>
      {windows.length > 0 ? (
        <EChartWrapper option={{
          tooltip: {
            trigger: 'axis' as const,
            formatter: (params: unknown) => {
              const ps = params as { seriesName: string; data: [number, number]; marker: string }[];
              if (!Array.isArray(ps) || ps.length === 0) return '';
              return [escapeHtml(fmtDateLong(ps[0].data[0])), ...ps.map(p => `${p.marker} ${escapeHtml(p.seriesName)}: ${fmtPct(p.data[1])}`)].join('<br/>');
            },
          },
          legend: lineLegend(windows.map(w => ({ name: w.name, style: w.style }))),
          xAxis: {
            type: 'time' as const,
            axisLabel: { hideOverlap: true, formatter: (v: number) => (shortRange ? fmtDayMonth(v) : fmtMonthShort(v)) },
          },
          yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtPct(v, { digits: 0 }) } },
          series: windows.map(w => ({
            name: w.name,
            type: 'line' as const,
            data: w.points,
            smooth: 0.3,
            showSymbol: false,
            connectNulls: false,
            color: w.colour,
            itemStyle: { color: w.colour },
            lineStyle: { color: w.colour, width: w.width, type: w.style },
          })),
          grid: { left: 48, right: 16, top: 12, bottom: 44 },
        }} height="220px" />
      ) : (
        <Statement>Rolling volatility needs at least 30 days of valuations in the selected period.</Statement>
      )}

      {past.length > 0 && (
        <>
          <SubHeadingRow>Past Drawdown Recoveries</SubHeadingRow>
          <ul className="divide-y divide-divider border-y border-divider text-[13px]">
            {past.slice(-5).map(ep => (
              <li key={`${ep.from_date}-${ep.trough_date}`} className="flex flex-wrap items-baseline justify-between gap-x-4 py-2">
                <span className="text-ink">{fmtMonthShort(ep.from_date)} to {fmtMonthShort(ep.trough_date)}</span>
                <span className="text-ink-body">
                  {fmtPct(-Math.abs(num(ep.max_drawdown_pct)))} at the low · recovered in {plural(num(ep.recovery_days), 'day', 'days')}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </>
  );
}

/* ───────────── Concentration ───────────── */

function Concentration() {
  const alerts = useLoad(() => api.getAlerts());
  return (
    <Section title="Concentration" subtitle="Exposure to single companies across all your securities, held directly or inside ETFs, and overlap between your ETFs, checked against the thresholds in Settings.">
      <Loaded state={alerts} lines={2}>
        {data => {
          const list = arr<ConcentrationAlert>(data?.alerts);
          if (list.length === 0) return <Statement>No concentration warnings. No single holding or ETF pair is above your thresholds.</Statement>;
          return (
            <div className="flex flex-col gap-4">
              {list.map((a, i) => (
                <Callout key={i} tone={a.level === 'critical' ? 'danger' : 'warning'} label={a.level === 'critical' ? 'Critical' : 'Warning'} title={a.message}>
                  {a.type === 'overlap' ? 'High overlap between ETFs can mean you hold the same stocks twice.' : 'Share of all your securities, counting direct holdings and ETF look-through. A large share depends on one company or asset.'}
                </Callout>
              ))}
            </div>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Crisis stress test ───────────── */

function StressTestSection() {
  const stress = useLoad(() => getJson<StressTest>('/api/analysis/crisis-stress-test'));
  return (
    <Section title="Historical Crisis Stress Test" subtitle="What past market crises would do to today’s securities, scaled by how much of each holding moves with equities.">
      <Loaded state={stress} lines={6}>
        {data => (arr(data?.portfolio_impact).length > 0 && num(data?.portfolio_value) > 0
          ? <StressBody data={data} />
          : <EmptyState
              title="No holdings to test"
              message="The stress test applies past crises to your current holdings. Import your broker transactions to see it."
              action={{ label: 'Import transactions', href: '/transactions' }}
            />)}
      </Loaded>
    </Section>
  );
}

/** Label for how a holding is shocked: equity share, crypto, or not at all. */
function exposureLabel(h: { is_equity: boolean; equity_pct?: number; is_crypto?: boolean }): string {
  if (h.is_crypto) return 'Crypto';
  const eq = h.equity_pct ?? (h.is_equity ? 100 : 0);
  if (eq >= 100) return 'Equity';
  if (eq <= 0) return 'Not equity';
  return `${fmtPct(eq, { digits: 0 })} equity`;
}

function StressBody({ data }: { data: StressTest }) {
  const impacts = [...arr(data.portfolio_impact)].sort((a, b) => a.drawdown_pct - b.drawdown_pct);
  const scenarios = arr(data.scenarios);
  const dca = arr(data.dca_recovery);
  const holdings = arr(data.holding_impacts).filter(h => h.value > 0).sort((a, b) => a.drawdown_eur - b.drawdown_eur);
  const equityPct = num(data.equity_pct);
  const cryptoPct = num(data.crypto_pct);
  const cashMonths = num(data.cash_buffer_months);
  const monthlyContribution = num(data.monthly_contribution);
  const showDca = monthlyContribution > 0;
  const worstEur = [...impacts].sort((a, b) => a.drawdown_eur - b.drawdown_eur)[0];
  const worstPct = impacts[0];
  const avgRecovery = impacts.reduce((s, i) => s + num(i.recovery_months), 0) / impacts.length;
  const months = (n: number) => plural(n, 'month', 'months');
  const cashMonthsText = `${fmtNum(cashMonths, 1)} ${cashMonths === 1 ? 'month' : 'months'}`;

  const rows = impacts.map(impact => {
    const d = dca.find(x => x.name === impact.name);
    return {
      ...impact,
      period: scenarios.find(s => s.name === impact.name)?.period ?? '',
      withDca: d ? `${months(d.with_dca_months)}${d.acceleration_months > 0 ? ` (${d.acceleration_months} sooner)` : ''}` : '—',
    };
  });

  // Months of recorded spending (average checking withdrawals) when there is any; otherwise
  // the 5 % rule of thumb, and the copy says which one it is.
  const recorded = data.spending_basis === 'recorded';
  const bufferBasis = recorded
    ? `your average spending of ${fmtEUR(data.monthly_spending)} a month (checking withdrawals over the last ${plural(Math.round(num(data.spending_months)), 'month', 'months')})`
    : 'withdrawals at 5 % of securities value a year, a rule of thumb because no spending is recorded';
  const bufferAdvice = recorded ? '' : ' Compare it with your monthly spend on the Cashflow page.';
  const suggestions: { title: string; detail: string; severity: 'high' | 'medium' | 'low' }[] = [];
  if (equityPct > 90) {
    suggestions.push({ severity: 'high', title: 'High equity concentration', detail: `${fmtPct(equityPct, { digits: 0 })} in equities means nearly full exposure to every crisis. Adding 10 to 20 % in bonds or gold could reduce the worst-case drawdown by 5 to 10 percentage points without much effect on long-term returns.` });
  } else if (equityPct > 80) {
    suggestions.push({ severity: 'medium', title: 'Consider diversifiers', detail: `At ${fmtPct(equityPct, { digits: 0 })} equity the portfolio tracks the major indices closely. A 5 to 10 % allocation to bonds, gold or property funds could smooth volatility.` });
  }
  if (cashMonths < 3) {
    suggestions.push({ severity: 'high', title: 'Build a cash buffer', detail: `Cash covers ${cashMonthsText} of ${bufferBasis}.${bufferAdvice} A buffer of 3 to 6 months of real expenses prevents forced selling in a drawdown.` });
  } else if (cashMonths < 6) {
    suggestions.push({ severity: 'low', title: 'Strengthen the cash buffer', detail: `Cash covers ${cashMonthsText} of ${bufferBasis}.${bufferAdvice}` });
  }
  if (worstPct && worstPct.drawdown_pct < -40) {
    suggestions.push({ severity: 'medium', title: 'Severe drawdown exposure', detail: `Worst case: a drawdown of ${fmtPct(worstPct.drawdown_pct, { digits: 0 })} (${fmtEUR(worstPct.drawdown_eur)}). If that would force you to sell, consider less equity or a larger cash buffer.` });
  }
  if (avgRecovery > 30) {
    suggestions.push({ severity: 'low', title: 'Long average recovery', detail: `In these historical crises the index took ${months(Math.round(avgRecovery))} on average to recover from its low. Continuing to invest through a drawdown shortens it noticeably.` });
  }
  const severity = {
    high: { tone: 'danger' as const, label: 'High priority' },
    medium: { tone: 'warning' as const, label: 'Medium priority' },
    low: { tone: 'success' as const, label: 'Low priority' },
  };

  return (
    <>
      <p className="mb-4 text-[14px] text-ink-body">
        Applied to <Money value={data.portfolio_value} /> of securities with {fmtPct(equityPct, { digits: 0 })} in equities{cryptoPct > 0 ? ` and ${fmtPct(cryptoPct, { digits: 0 })} in crypto` : ''}.
      </p>

      {/* Phones: one card per crisis, nothing cut off. */}
      <ul className="md:hidden flex flex-col gap-3">
        {rows.map(row => (
          <li key={row.name} className="rounded-apple border border-divider px-4 py-3">
            <p className="text-[14px] font-medium text-ink">{row.name}</p>
            <p className="mb-2 text-[12px] text-ink-muted">{row.period}</p>
            <KeyValues rows={[
              { label: 'Drawdown', value: <span className="text-claret">{fmtPct(row.drawdown_pct)}</span> },
              { label: 'Impact', value: <Money value={row.drawdown_eur} tone="negative" /> },
              { label: 'Worst month', value: fmtPct(row.worst_month_pct) },
              { label: 'Index recovery', value: months(row.recovery_months) },
              ...(showDca ? [{ label: 'With monthly investing', value: row.withDca }] : []),
            ]} />
          </li>
        ))}
      </ul>

      <TableRegion label="Crisis stress test" className="hidden md:block">
        <table className="apple-table">
          <thead>
            <tr>
              <th scope="col">Crisis</th>
              <th scope="col" className="num">Drawdown</th>
              <th scope="col" className="num">Impact</th>
              <th scope="col" className="num">Worst month</th>
              <th scope="col" className="num">Index recovery</th>
              {showDca && <th scope="col" className="num">With monthly investing</th>}
            </tr>
          </thead>
          <tbody>
            {rows.map(row => (
              <tr key={row.name}>
                <td>
                  <span className="block font-medium text-ink">{row.name}</span>
                  <span className="block text-[12px] text-ink-muted">{row.period}</span>
                </td>
                <td className="num text-claret">{fmtPct(row.drawdown_pct)}</td>
                <td className="num"><Money value={row.drawdown_eur} tone="negative" /></td>
                <td className="num">{fmtPct(row.worst_month_pct)}</td>
                <td className="num">{months(row.recovery_months)}</td>
                {showDca && <td className="num">{row.withDca}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      </TableRegion>

      <div className="mt-3 flex flex-col gap-1 text-[13px] text-ink-muted">
        <p>
          Drawdowns and recovery times are historical MSCI World price-index figures in US dollars; in euros the falls were different.
          Recovery counts the months from the index’s low back to its previous peak.
          Funds count with their recorded equity share (without one, equity funds in full and mixed funds by half){cryptoPct > 0 ? '; crypto is assumed to fall twice as far as equities, by at most 90 %' : ''}. Bonds and gold are not shocked.
        </p>
        {showDca ? (
          <p>
            With monthly investing assumes your net contributions of the last 12 months, <Money value={monthlyContribution} /> a month, continue through the crisis.
            Recovery then means the portfolio is worth everything you put in.
          </p>
        ) : (
          <p>Your net contributions over the last 12 months were not positive, so there is no monthly-investing scenario.</p>
        )}
      </div>

      <MetricGrid className="mt-6">
        <MetricCard label="Cash buffer" value={fmtEUR(data.cash_buffer)} sublabel={`Checking, savings and broker cash; covers ${cashMonthsText} of ${recorded ? 'average spending' : 'spending at the 5 % rule of thumb'}`} />
        <MetricCard label="Worst-case loss" value={fmtEUR(worstEur?.drawdown_eur)} tone={num(worstEur?.drawdown_eur) < 0 ? 'negative' : 'neutral'} sublabel={worstEur?.name ?? '—'} />
        <MetricCard money={false} label="Equity exposure" value={fmtPct(equityPct, { digits: 0 })} sublabel="Share of securities in stocks and the equity part of funds" />
      </MetricGrid>

      {holdings.length > 0 && (
        <>
          <SubHeadingRow>Impact by Holding (2008 Financial Crisis Scenario)</SubHeadingRow>
          <ul className="divide-y divide-divider border-y border-divider">
            {holdings.map(h => (
              <li key={h.isin} className="flex items-center justify-between gap-4 py-2 text-[13px]">
                <span className="min-w-0">
                  <span className="block truncate text-ink" title={h.name}>{h.name}</span>
                  <span className="block text-[12px] text-ink-muted">{exposureLabel(h)} · <Money value={h.value} /></span>
                </span>
                <span className="shrink-0 text-right">
                  {h.drawdown_eur < 0 ? <Money value={h.drawdown_eur} tone="negative" /> : <span className="text-ink-muted">No impact</span>}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}

      {suggestions.length > 0 && (
        <>
          <SubHeadingRow>Stress-Adjusted Suggestions</SubHeadingRow>
          <div className="flex flex-col gap-4">
            {suggestions.map(s => (
              <Callout key={s.title} tone={severity[s.severity].tone} label={severity[s.severity].label} title={s.title}>{s.detail}</Callout>
            ))}
          </div>
        </>
      )}
    </>
  );
}

/* ───────────── Savings-plan consistency ───────────── */

function SparplanConsistency() {
  const streak = useLoad(() => getJson<SparplanStreak>('/api/portfolio/sparplan-streak'));
  return (
    <Section title="Sparplan Consistency" subtitle="How regularly your Sparplan (monthly savings plan) has actually been invested, from its first execution to the last complete month (this month counts once it has run).">
      <Loaded state={streak} lines={3}>
        {s => (num(s?.total_months) > 0 ? (
          <>
            <MetricGrid>
              <MetricCard money={false} label="Current streak" value={plural(num(s.current_streak), 'month', 'months')} />
              <MetricCard money={false} label="Longest streak" value={plural(num(s.longest_streak), 'month', 'months')} />
              <MetricCard money={false} label="Consistency" value={fmtPct(s.consistency_pct, { digits: 0 })}
                sublabel={`${num(s.active_months)} of ${plural(num(s.total_months), 'month', 'months')} invested, ${num(s.missed_months)} missed`} />
              <MetricCard label="Cost of missed months" value={num(s.missed_cost) > 0 ? fmtEUR(s.missed_cost) : '—'}
                sublabel={`Growth forgone at an assumed ${fmtPct(s.assumed_return_pct ?? 7, { digits: 0 })} a year`} />
            </MetricGrid>
            <p className="mt-3 text-[13px] text-ink-muted">
              Typically <Money value={s.avg_monthly} /> per execution over the last 12 months · <Money value={s.total_invested} /> invested in total
            </p>
          </>
        ) : (
          <EmptyState
            title="No savings-plan history yet"
            message="Once savings-plan purchases have been imported, this shows your streak and any missed months."
            action={{ label: 'Import transactions', href: '/transactions' }}
          />
        ))}
      </Loaded>
    </Section>
  );
}

/* ───────────── Decision journal ───────────── */

const JOURNAL_TYPES: { value: string; label: string }[] = [
  { value: 'buy', label: 'Buy' },
  { value: 'sell', label: 'Sale' },
  { value: 'rebalance', label: 'Rebalance' },
  { value: 'tax_harvest', label: 'Tax-loss harvest' },
  { value: 'allocation_change', label: 'Allocation change' },
  { value: 'other', label: 'Other' },
];

function DecisionJournal() {
  const journal = useLoad(() => getJson<Journal>('/api/analysis/journal'));
  const [type, setType] = useState('buy');
  const [reason, setReason] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  const save = () => {
    if (!reason.trim() || saving) return;
    setSaving(true);
    setSaveError(null);
    postJson<unknown>('/api/analysis/journal', { action_type: type, reason })
      .then(() => { setReason(''); journal.reload(); })
      .catch(() => setSaveError('The entry could not be saved. Try again.'))
      .finally(() => setSaving(false));
  };

  return (
    <Section title="Decision Journal" subtitle="Record why you made an investment decision, and review it later.">
      <form className="grid gap-3 md:grid-cols-[200px_1fr_auto] md:items-end" onSubmit={e => { e.preventDefault(); save(); }}>
        <SelectField label="Decision type" value={type} onChange={e => setType(e.target.value)}>
          {JOURNAL_TYPES.map(t => <option key={t.value} value={t.value}>{t.label}</option>)}
        </SelectField>
        <Field label="Reason" value={reason} onChange={e => setReason(e.target.value)} error={saveError ?? undefined} />
        <button type="submit" disabled={!reason.trim() || saving} className="apple-btn-primary min-h-[44px]">Save entry</button>
      </form>

      <div className="mt-5">
        <Loaded state={journal} lines={3}>
          {data => {
            const entries = arr(data?.entries);
            if (entries.length === 0) return <Statement>No entries yet. Your first entry appears here once saved.</Statement>;
            return (
              <ul className="divide-y divide-divider border-y border-divider">
                {entries.slice(0, 10).map(e => {
                  const due = e.days_ago > 90 && !e.outcome;
                  return (
                    <li key={e.id} className="py-3">
                      <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                        <span className="flex flex-wrap items-center gap-2 text-[14px] font-medium text-ink">
                          {JOURNAL_TYPES.find(t => t.value === e.action_type)?.label ?? labelFor(e.action_type)}
                          {due && <Chip tone="warning">Review due</Chip>}
                        </span>
                        <span className="text-[12px] text-ink-muted">{fmtDate(e.date)} · {plural(num(e.days_ago), 'day', 'days')} ago</span>
                      </div>
                      <p className="mt-1 text-[13px] text-ink-body">{e.reason}</p>
                      {e.outcome && <p className="mt-1 text-[13px] text-ink-body"><span className="font-medium text-ink">Outcome:</span> {e.outcome}</p>}
                      {due && <p className="mt-1 text-[12px] text-ink-muted">More than 90 days old. How did this decision play out?</p>}
                    </li>
                  );
                })}
              </ul>
            );
          }}
        </Loaded>
      </div>
    </Section>
  );
}
