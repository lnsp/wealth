import { useState } from 'react';
import { api, type ETFHoldingEntry, type HoldingRow, type TreemapNode } from '../../api/client';
import EChartWrapper from '../../components/charts/EChartWrapper';
import { Chip, EmptyState, ErrorState, LoadingState, MetricCard, MetricGrid, PeriodSelector, Section, SectionBoundary, filterByPeriod, formatDateForPeriod } from '../../components/ui';
import type { Period } from '../../components/ui';
import { useThemeColors } from '../../hooks/useThemeColors';
import { escapeHtml, fmtEUR, fmtNum, fmtPct } from '../../utils/format';
import { Loaded, Money, ShareBars, Statement, SubHeadingRow, TableRegion, arr, mixHex, num, plural, rec, useJobTrigger, useLoad } from './shared';
import type { LoadState, ShareBarItem } from './shared';

type Summary = Awaited<ReturnType<typeof api.getAllocationSummary>>;
type Matrix = { labels: string[]; matrix: (number | null)[][] };

const FUND_DATA_HINT = 'Sector, country and constituent data is fetched in the background for each ETF and fund you hold.';
const STOCK_SECTOR_HINT = 'Sector data comes from fund profiles, so it covers ETFs and funds only. Single shares have no sector source yet and are counted as unknown.';
/** Bucket the backend uses for value without sector, country or currency data. */
const UNKNOWN = 'Unknown';
/** Asset classes whose constituents and fund data may be on record. */
const FUND_CLASSES = new Set(['etf', 'fund', 'bond']);
/** Treemap tile for the part of a fund outside its listed holdings (backend analytics.TreemapRestOfFund). */
const REST_OF_FUND = 'Rest of fund';

export default function ExposureTab() {
  const summary = useLoad(() => api.getAllocationSummary());
  const etfs = useLoad(() => api.listHoldings().then(r => arr(r?.holdings).filter(h => FUND_CLASSES.has(h.asset_class))));
  // Constituents of the ETF opened in the breakdown; the sustainability check reads them too.
  const [constituents, setConstituents] = useState<ETFHoldingEntry[]>([]);
  return (
    <>
      <SectionBoundary><SectorAllocation summary={summary} /></SectionBoundary>
      <SectionBoundary><SectorDrift /></SectionBoundary>
      <SectionBoundary><CountryAllocation summary={summary} /></SectionBoundary>
      <SectionBoundary><CurrencyExposure summary={summary} /></SectionBoundary>
      <SectionBoundary><AllocationTreemap /></SectionBoundary>
      <SectionBoundary><TopSharedHoldings /></SectionBoundary>
      <SectionBoundary><OverlapMatrix /></SectionBoundary>
      <SectionBoundary><CorrelationMatrix /></SectionBoundary>
      <SectionBoundary><EtfBreakdown etfs={etfs} onConstituents={setConstituents} /></SectionBoundary>
      <SectionBoundary><Sustainability etfs={etfs} constituents={constituents} /></SectionBoundary>
    </>
  );
}

/** Empty state shared by the look-through sections, with the fund-data refresh wired in. */
function NoFundData({ title, message, onDone }: { title: string; message: string; onDone: () => void }) {
  const refresh = useJobTrigger('/api/settings/refresh-etf-metadata', onDone);
  return (
    <EmptyState title={title} message={message} action={{ label: 'Refresh fund data', onClick: refresh.run }}>
      {refresh.message && <p role="status" className="text-[13px] text-ink-muted">{refresh.message}</p>}
    </EmptyState>
  );
}

/** Share bars largest first; the backend's "Unknown" bucket goes last under `unknownLabel`. */
function shareItems(entries: [string, number][], limit?: number, otherLabel = 'Other', unknownLabel = UNKNOWN): { items: ShareBarItem[]; total: number } {
  const positive = entries.filter(([, v]) => v > 0);
  const unknown = positive.find(([k]) => k === UNKNOWN);
  const sorted = positive.filter(([k]) => k !== UNKNOWN).sort(([, a], [, b]) => b - a);
  const total = positive.reduce((s, [, v]) => s + v, 0);
  let shown = sorted;
  if (limit && sorted.length > limit) {
    const rest = sorted.slice(limit);
    shown = [...sorted.slice(0, limit), [`${otherLabel} (${rest.length})`, rest.reduce((s, [, v]) => s + v, 0)]];
  }
  if (unknown) shown = [...shown, [unknownLabel, unknown[1]]];
  return {
    total,
    items: shown.map(([label, value]) => ({
      label, value, money: true,
      display: fmtEUR(value),
      share: fmtPct(total > 0 ? (value / total) * 100 : 0),
    })),
  };
}

/* ───────────── Sector / country / currency ───────────── */

function SectorAllocation({ summary }: { summary: LoadState<Summary> }) {
  return (
    <Section first title="Sector Allocation" subtitle="Look-through exposure by sector, across all funds and single stocks.">
      <Loaded state={summary} lines={6}>
        {data => {
          const entries = Object.entries(rec(data?.sectors));
          const known = entries.filter(([k, v]) => k !== UNKNOWN && v > 0);
          // Only unknown value (e.g. single shares only): nothing to break down.
          if (known.length === 0) return <NoFundData title="No sector data yet" message={`${FUND_DATA_HINT} ${STOCK_SECTOR_HINT}`} onDone={summary.reload} />;
          const { items } = shareItems(entries, undefined, 'Other', 'No sector data');
          if (items.length === 1) return <Statement>All of your exposure is in 1 sector: {items[0].label} (<Money value={items[0].value} />).</Statement>;
          return (
            <>
              <p className="mb-4 text-[13px] text-ink-muted">
                {plural(known.length, 'sector', 'sectors')}
                {entries.some(([k, v]) => k === UNKNOWN && v > 0) && '. Single shares and funds without data are shown as “No sector data”.'}
              </p>
              <ShareBars items={items} />
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

function CountryAllocation({ summary }: { summary: LoadState<Summary> }) {
  return (
    <Section title="Country Allocation" subtitle="Where the companies you own are based: fund data for ETFs, the issuer’s country (from the ISIN) for single shares.">
      <Loaded state={summary} lines={6}>
        {data => {
          const all = Object.entries(rec(data?.countries)).filter(([, v]) => v > 0);
          const known = all.filter(([k]) => k !== UNKNOWN);
          if (known.length === 0) return <NoFundData title="No country data yet" message={FUND_DATA_HINT} onDone={summary.reload} />;
          const { items } = shareItems(all, 15, 'Other countries', 'No country data');
          if (items.length === 1) return <Statement>All of your exposure is in 1 country: {items[0].label} (<Money value={items[0].value} />).</Statement>;
          return (
            <>
              <p className="mb-4 text-[13px] text-ink-muted">{plural(known.length, 'country', 'countries')}</p>
              <ShareBars items={items} />
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

function CurrencyExposure({ summary }: { summary: LoadState<Summary> }) {
  return (
    <Section title="Currency Exposure" subtitle="Underlying currency exposure, based on where each holding’s assets are. Funds without country data count as unknown, not as their trading currency.">
      <Loaded state={summary} lines={4}>
        {data => {
          const all = arr(data?.currencies).filter(c => num(c.value) > 0);
          // Largest first, the unknown bucket last.
          const list = [...all.filter(c => c.currency !== UNKNOWN).sort((a, b) => b.value - a.value), ...all.filter(c => c.currency === UNKNOWN)];
          const known = list.filter(c => c.currency !== UNKNOWN);
          if (known.length === 0) return <NoFundData title="No currency data yet" message={FUND_DATA_HINT} onDone={summary.reload} />;
          if (list.length === 1) return <Statement>All of your exposure is in 1 currency: {list[0].currency} (<Money value={list[0].value} />).</Statement>;
          return (
            <>
              <p className="mb-4 text-[13px] text-ink-muted">{plural(known.length, 'currency', 'currencies')}</p>
              <ShareBars scale={100} items={list.map(c => ({
                label: c.currency === UNKNOWN ? 'Unknown (fund without country data)' : c.currency,
                value: num(c.pct), money: true, display: fmtEUR(c.value), share: fmtPct(c.pct),
              }))} />
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Sector drift ───────────── */

function SectorDrift() {
  const tc = useThemeColors();
  const [period, setPeriod] = useState<Period>('All');
  const history = useLoad(() => api.getSectorHistory());
  return (
    <Section title="Sector Drift Over Time" subtitle="Sector shares of the portfolio’s market value at the end of each month, with today as the last point. Current sector weights are applied to past holdings."
      actions={<PeriodSelector value={period} onChange={setPeriod} />}>
      <Loaded state={history} lines={6}>
        {data => {
          const all = arr(data?.history);
          const points = filterByPeriod(all, period);
          // Only sectors that actually carry weight in the window: row count alone says nothing.
          // The unknown share is stacked last.
          const sectors = arr(data?.sectors).filter(s => points.some(p => num(rec(p.sectors)[s]) > 0.05))
            .sort((a, b) => Number(a === UNKNOWN) - Number(b === UNKNOWN));
          const hasAny = arr(data?.sectors).some(s => s !== UNKNOWN && all.some(p => num(rec(p.sectors)[s]) > 0.05));
          if (!hasAny) return <NoFundData title="No sector history yet" message={`Sector history is rebuilt from your transactions once fund data is available. ${STOCK_SECTOR_HINT}`} onDone={history.reload} />;
          if (sectors.length === 0 || points.length < 2) return <Statement>No sector history in this period. Choose a longer period.</Statement>;
          if (sectors.length === 1 && sectors[0] === UNKNOWN) return <Statement>None of the holdings in this period has sector data. Choose a longer period.</Statement>;
          if (sectors.length === 1) return <Statement>The portfolio sat in 1 sector, {sectors[0]}, throughout this period, so there is no drift to show.</Statement>;
          return (
            <EChartWrapper option={{
              tooltip: {
                trigger: 'axis' as const,
                formatter: (params: unknown) => {
                  const ps = params as { seriesName: string; value: number; marker: string }[];
                  if (!Array.isArray(ps) || ps.length === 0) return '';
                  return ps.filter(p => p.value > 0.1).sort((a, b) => b.value - a.value)
                    .map(p => `${p.marker} ${escapeHtml(p.seriesName)}: ${fmtPct(p.value)}`).join('<br/>');
                },
              },
              legend: { type: 'scroll' as const, bottom: 0 },
              xAxis: { type: 'category' as const, data: points.map(s => formatDateForPeriod(s.date, period)), boundaryGap: false },
              yAxis: { type: 'value' as const, min: 0, max: 100, axisLabel: { formatter: (v: number) => fmtPct(v, { digits: 0 }) } },
              series: sectors.map(sector => ({
                name: sector === UNKNOWN ? 'No sector data' : sector,
                type: 'line' as const,
                stack: 'total',
                areaStyle: { opacity: 0.75 },
                lineStyle: { width: 1 },
                emphasis: { focus: 'series' as const },
                symbol: 'none',
                // The unknown share is neutral grey so it never reads as a sector.
                ...(sector === UNKNOWN ? { color: mixHex(tc.parchment, tc.slate, 0.35) } : {}),
                data: points.map(s => Math.round(num(rec(s.sectors)[sector]) * 100) / 100),
              })),
              grid: { left: 52, right: 16, top: 12, bottom: 56 },
            }} height="360px" />
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Treemap ───────────── */

function AllocationTreemap() {
  const tc = useThemeColors();
  const treemap = useLoad(() => api.getTreemap());
  return (
    <Section title="Portfolio Allocation" subtitle="Every holding at its market value, each ETF broken down into its largest holdings; the rest of the fund is one grey tile. Select a tile to zoom in; use the trail below the chart to go back.">
      <Loaded state={treemap} lines={6}>
        {data => {
          // The part of a fund outside its listed holdings is neutral, not a company.
          type Datum = Omit<TreemapNode, 'children'> & { children?: Datum[]; itemStyle?: { color: string }; label?: { color: string } };
          const decorate = (n: TreemapNode): Datum => (n.name === REST_OF_FUND
            ? { ...n, itemStyle: { color: mixHex(tc.parchment, tc.slate, 0.35) }, label: { color: tc.ink } }
            : { ...n, children: n.children?.map(decorate) });
          const nodes = arr<TreemapNode>(data?.children).map(decorate);
          if (nodes.length === 0) return <NoFundData title="No allocation data yet" message={FUND_DATA_HINT} onDone={treemap.reload} />;
          return (
            <EChartWrapper option={{
              tooltip: {
                formatter: (params: unknown) => {
                  const p = params as { name: string; value: number; treePathInfo?: { name: string }[] };
                  const path = arr(p.treePathInfo).map(n => escapeHtml(n.name)).filter(Boolean).join(' → ') || escapeHtml(p.name);
                  return `${path}<br/>${fmtEUR(p.value)}`;
                },
              },
              series: [{
                type: 'treemap' as const,
                data: nodes,
                // Leave a band under the tiles so the breadcrumb never sits on top of them.
                left: 0, right: 0, top: 0, bottom: 40,
                roam: false,
                nodeClick: 'zoomToNode' as const,
                breadcrumb: {
                  show: true, left: 0, bottom: 4, height: 24,
                  itemStyle: { color: tc.parchmentDeep, borderColor: tc.divider, borderWidth: 1, shadowBlur: 0, textStyle: { color: tc.inkBody, fontSize: 12 } },
                  emphasis: { itemStyle: { color: tc.divider, textStyle: { color: tc.ink } } },
                },
                levels: [
                  { itemStyle: { borderColor: tc.parchment, borderWidth: 3, gapWidth: 3 }, upperLabel: { show: true, height: 24, fontSize: 13, fontWeight: 600, color: tc.ink } },
                  { itemStyle: { borderColor: tc.parchment, borderWidth: 2, gapWidth: 2 }, upperLabel: { show: true, height: 20, fontSize: 11, color: tc.inkBody } },
                  { itemStyle: { borderColor: tc.parchment, borderWidth: 1 }, label: { show: true, fontSize: 11, color: tc.parchment, overflow: 'truncate' as const } },
                ],
                label: { show: true, fontSize: 11, color: tc.parchment, overflow: 'truncate' as const },
                // Group headings sit on the parchment-coloured frame, so they use ink.
                upperLabel: { show: true, color: tc.ink },
              }],
            }} height="440px" />
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Top shared holdings ───────────── */

function TopSharedHoldings() {
  const top = useLoad(() => api.getTopHoldings());
  return (
    <Section title="Top Holdings" subtitle="The companies with the largest share of your whole portfolio: shares you hold directly plus the largest holdings inside each ETF.">
      <Loaded state={top} lines={6}>
        {data => {
          const list = arr(data?.holdings).slice(0, 15);
          if (list.length === 0) return <NoFundData title="No constituent data yet" message={FUND_DATA_HINT} onDone={top.reload} />;
          const coverage = num(data?.coverage_pct);
          return (
            <>
              <ShareBars items={list.map(h => ({
                label: h.name,
                value: num(h.exposure_pct),
                display: fmtPct(h.exposure_pct, { digits: 2 }),
                note: h.exposure_pct >= 10 ? 'Above 10 %' : h.exposure_pct >= 5 ? 'Above 5 %' : undefined,
              }))} />
              {coverage > 0 && coverage < 99.5 && (
                <p className="mt-3 text-[13px] text-ink-muted">
                  Shares of the whole portfolio. {fmtPct(coverage)} of it is attributed to named companies; fund data lists only each ETF’s largest holdings (usually the top 10), so smaller positions inside funds are not counted.
                </p>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Overlap and correlation heatmaps ───────────── */

/** Heatmap of a symmetric matrix; null cells (no value) stay blank and say why in the tooltip. */
function Heatmap({ data, min, max, colours, digits, unit, regionLabel, missing = 'no data' }: { data: Matrix; min: number; max: number; colours: string[]; digits: number; unit: string; regionLabel: string; missing?: string }) {
  const tc = useThemeColors();
  const labels = arr(data.labels);
  const short = (s: string) => (s.length > 18 ? `${s.slice(0, 17)}…` : s);
  const cell = (v: number | string) => (typeof v === 'number' ? `${fmtNum(v, digits)}${unit}` : '');
  return (
    <div role="region" aria-label={regionLabel} tabIndex={0} className="overflow-x-auto">
      <div style={{ minWidth: `${Math.max(340, 150 + labels.length * 64)}px` }}>
        <EChartWrapper option={{
          tooltip: {
            formatter: (params: unknown) => {
              const [x, y, val] = (params as { data: (number | string)[] }).data;
              return `${escapeHtml(labels[Number(x)] ?? '')} and ${escapeHtml(labels[Number(y)] ?? '')}: ${typeof val === 'number' ? cell(val) : escapeHtml(missing)}`;
            },
          },
          xAxis: { type: 'category' as const, data: labels, axisLabel: { rotate: 30, color: tc.inkBody, hideOverlap: false, formatter: short } },
          yAxis: { type: 'category' as const, data: labels, axisLabel: { color: tc.inkBody, hideOverlap: false, formatter: short } },
          visualMap: {
            min, max, calculable: false, orient: 'horizontal' as const, left: 'center', bottom: 0,
            inRange: { color: colours },
            textStyle: { color: tc.inkMuted, fontSize: 11 },
            text: [cell(max), cell(min)],
          },
          series: [{
            type: 'heatmap' as const,
            // '-' is ECharts' empty value: the cell is drawn blank, not as 0.
            data: arr(data.matrix).flatMap((row, i) => arr(row).map((val, j) => [i, j, val == null ? '-' : Math.round(val * 100) / 100])),
            label: { show: true, fontSize: 11, color: tc.ink, formatter: (p: unknown) => cell((p as { data: (number | string)[] }).data[2]) },
            itemStyle: { borderColor: tc.parchment, borderWidth: 2 },
          }],
          grid: { left: 136, right: 16, top: 8, bottom: 120 },
        }} height={`${Math.max(320, 190 + labels.length * 44)}px`} />
      </div>
    </div>
  );
}

function OverlapMatrix() {
  const tc = useThemeColors();
  const overlap = useLoad(() => api.getOverlap());
  return (
    <Section title="ETF Overlap of Top Holdings" subtitle="How much of each pair of ETFs sits in the same companies, counting only the holdings listed for each fund (usually its top 10).">
      <Loaded state={overlap} lines={5}>
        {data => {
          const labels = arr(data?.labels);
          if (labels.length < 2) return <Statement>Overlap compares pairs of ETFs, so it needs at least 2 ETFs with constituent data.</Statement>;
          const coverage = arr(data?.coverage);
          const counts = arr(data?.constituent_counts);
          const partial = coverage.some(c => num(c) < 99.5);
          return (
            <>
              <Heatmap data={data} min={0} max={100} digits={1} unit=" %" regionLabel="ETF overlap of top holdings"
                colours={[tc.parchmentDeep, mixHex(tc.parchment, tc.sage, 0.6)]} />
              {partial && (
                <p className="mt-3 text-[13px] text-ink-muted">
                  Each figure is a lower bound: the listed holdings cover only part of each fund, so the real overlap is at least this high and, for two broad index funds, usually much higher.
                </p>
              )}
              {coverage.length === labels.length && (
                <ul className="mt-2 flex flex-col gap-1 text-[13px] text-ink-body">
                  {labels.map((label, i) => (
                    <li key={arr(data?.isins)[i] ?? label}>
                      <span className="text-ink">{label}</span>: listed holdings cover {fmtPct(coverage[i])} of the fund{counts[i] ? ` (${plural(counts[i], 'holding', 'holdings')})` : ''}
                    </li>
                  ))}
                </ul>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

function CorrelationMatrix() {
  const tc = useThemeColors();
  const correlation = useLoad(() => api.getCorrelation());
  return (
    <Section title="Price Correlation" subtitle="How closely the holdings’ prices in euros move together, measured over the dates both have a price: from −1 (opposite) to 1 (identical). Lower values mean better diversification.">
      <Loaded state={correlation} lines={5}>
        {data => {
          if (arr(data?.labels).length < 2) return <Statement>Correlation compares pairs of holdings, so it needs at least 2 holdings with enough price history.</Statement>;
          const blank = arr(data?.matrix).some(row => arr(row).some(v => v == null));
          return (
            <>
              <Heatmap data={data} min={-1} max={1} digits={2} unit="" regionLabel="Price correlation matrix" missing="not enough shared price history"
                colours={[mixHex(tc.parchment, tc.claret, 0.55), tc.parchmentDeep, mixHex(tc.parchment, tc.sage, 0.6)]} />
              {blank && (
                <p className="mt-3 text-[13px] text-ink-muted">
                  Blank cells: the two holdings share fewer than {num(data?.min_observations) || 20} price changes, too few for a meaningful figure.
                </p>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── ETF holdings breakdown ───────────── */

/** False for empty and synthetic constituent IDs ("SYNTH:<hash>", legacy "XX_…"), which are not ISINs. */
function isRealISIN(id: string | null | undefined): boolean {
  return !!id && !id.startsWith('SYNTH:') && !id.startsWith('XX_');
}

function EtfBreakdown({ etfs, onConstituents }: { etfs: LoadState<HoldingRow[]>; onConstituents: (list: ETFHoldingEntry[]) => void }) {
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<{ status: 'idle' | 'loading' | 'error' | 'done'; name: string; rows: ETFHoldingEntry[]; coverage?: number; error?: string }>({ status: 'idle', name: '', rows: [] });

  const load = (isin: string) => {
    setSelected(isin);
    setDetail({ status: 'loading', name: '', rows: [] });
    api.getETFHoldings(isin)
      .then(d => {
        const rows = arr(d?.holdings);
        setDetail({ status: 'done', name: d?.etf_name ?? '', rows, coverage: d?.coverage_pct });
        onConstituents(rows);
      })
      .catch((e: unknown) => setDetail({ status: 'error', name: '', rows: [], error: e instanceof Error ? e.message : String(e) }));
  };
  const toggle = (isin: string) => {
    if (selected === isin) {
      setSelected(null);
      setDetail({ status: 'idle', name: '', rows: [] });
      onConstituents([]);
    } else {
      load(isin);
    }
  };

  return (
    <Section title="ETF Holdings Breakdown" subtitle="Select an ETF to see its largest holdings.">
      <Loaded state={etfs} lines={3}>
        {list => {
          // One button per fund, even when it is held in several accounts.
          const funds = [...new Map(list.map(h => [h.security_isin, h])).values()];
          if (funds.length === 0) {
            return <EmptyState title="No ETFs held" message="This breakdown lists the companies inside each ETF. It appears once you hold at least one." action={{ label: 'Import transactions', href: '/transactions' }} />;
          }
          return (
            <>
              <div className="flex flex-wrap gap-2" role="group" aria-label="ETF">
                {funds.map(h => {
                  const on = selected === h.security_isin;
                  return (
                    <button key={h.security_isin} type="button" aria-pressed={on} onClick={() => toggle(h.security_isin)}
                      className={`min-h-[44px] md:min-h-[36px] max-w-full px-3 rounded-[4px] border text-left text-[13px] transition-colors ${
                        on ? 'bg-forest border-forest text-parchment font-semibold' : 'border-field text-ink-body font-medium hover:border-forest hover:text-ink'
                      }`}>
                      {h.name}
                    </button>
                  );
                })}
              </div>

              <div className="mt-4">
                {detail.status === 'idle' && <Statement>No ETF selected.</Statement>}
                {detail.status === 'loading' && <LoadingState message="Loading constituents" />}
                {detail.status === 'error' && <ErrorState details={detail.error} onRetry={selected ? () => load(selected) : undefined} />}
                {detail.status === 'done' && detail.rows.length === 0 && <Statement>No constituent data is available for this ETF yet.</Statement>}
                {detail.status === 'done' && detail.rows.length > 0 && (() => {
                  // The constituent source has no sector or country column: show those only when on record.
                  const hasSector = detail.rows.some(e => e.sector);
                  const hasCountry = detail.rows.some(e => e.country);
                  const covered = num(detail.coverage) || detail.rows.reduce((s, e) => s + num(e.weight), 0);
                  return (
                    <>
                      <SubHeadingRow>{detail.name}: {plural(detail.rows.length, 'largest holding', 'largest holdings')}, {fmtPct(covered)} of the fund</SubHeadingRow>
                      <ul className="md:hidden divide-y divide-divider border-y border-divider">
                        {detail.rows.map((entry, i) => (
                          <li key={`${entry.isin}-${i}`} className="flex items-center gap-3 py-2">
                            <span className="w-6 shrink-0 text-right text-[12px] text-ink-muted">{i + 1}</span>
                            <span className="min-w-0 flex-1">
                              <span className="block truncate text-[13px] font-medium text-ink" title={entry.name}>{entry.name}</span>
                              {(entry.sector || entry.country) && (
                                <span className="block text-[12px] text-ink-muted">{[entry.sector, entry.country].filter(Boolean).join(' · ')}</span>
                              )}
                            </span>
                            <span className="shrink-0 text-[13px] text-ink">{fmtPct(entry.weight, { digits: 2 })}</span>
                          </li>
                        ))}
                      </ul>
                      <TableRegion label={`Constituents of ${detail.name}`} className="hidden md:block max-h-[560px] overflow-y-auto">
                        <table className="apple-table">
                          <thead>
                            <tr>
                              <th scope="col" className="num">No.</th>
                              <th scope="col">Name</th>
                              <th scope="col">ISIN</th>
                              <th scope="col" className="num">Weight</th>
                              {hasSector && <th scope="col">Sector</th>}
                              {hasCountry && <th scope="col">Country</th>}
                            </tr>
                          </thead>
                          <tbody>
                            {detail.rows.map((entry, i) => (
                              <tr key={`${entry.isin}-${i}`}>
                                <td className="num text-ink-muted">{i + 1}</td>
                                <td className="font-medium text-ink">{entry.name}</td>
                                <td className="font-mono text-[12px]">{isRealISIN(entry.isin) ? entry.isin : '—'}</td>
                                <td className="num text-ink">{fmtPct(entry.weight, { digits: 2 })}</td>
                                {hasSector && <td>{entry.sector || '—'}</td>}
                                {hasCountry && <td>{entry.country || '—'}</td>}
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </TableRegion>
                    </>
                  );
                })()}
              </div>
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}

/* ───────────── Sustainability (ESG) ───────────── */

type EsgStrategy = 'SRI' | 'PAB' | 'CTB' | 'Enhanced' | 'Screened' | 'ESG' | 'None';
const ESG_LABEL: Record<EsgStrategy, string> = {
  SRI: 'Socially responsible (SRI)',
  PAB: 'Paris-aligned (PAB)',
  CTB: 'Climate transition (CTB)',
  Enhanced: 'ESG enhanced',
  Screened: 'ESG screened',
  ESG: 'ESG integrated',
  None: 'No ESG strategy',
};

/** Classifies a fund by the sustainability strategy named in its title. */
function classifyESG(name: string): EsgStrategy {
  const n = name.toUpperCase();
  if (n.includes('SRI') || n.includes('SUSTAINABLE')) return 'SRI';
  if (n.includes('PAB') || n.includes('PARIS')) return 'PAB';
  if (n.includes('CTB') || n.includes('CLIMATE')) return 'CTB';
  if (n.includes('ESG') && (n.includes('ENHANCED') || n.includes('LEADERS'))) return 'Enhanced';
  if (n.includes('ESG') && n.includes('SCREENED')) return 'Screened';
  if (n.includes('ESG')) return 'ESG';
  return 'None';
}

const CONTROVERSIAL_KEYWORDS = ['tobacco', 'weapons', 'gambling', 'coal', 'arctic', 'palm oil', 'nuclear', 'cluster munitions', 'thermal coal', 'controversial', 'defense', 'arms'];

// Conventional fund → ESG-screened equivalent.
const ESG_ALTERNATIVES: Record<string, { isin: string; name: string; ter: number; strategy: string }> = {
  IE00BK5BQT80: { isin: 'IE00BNG8L278', name: 'Vanguard ESG Global All Cap', ter: 0.24, strategy: 'ESG screened' },
  IE00B4L5Y983: { isin: 'IE00BYX2JD69', name: 'iShares MSCI World SRI', ter: 0.20, strategy: 'SRI' },
  IE00B4L5YC18: { isin: 'IE00BFNM3P36', name: 'iShares MSCI EM SRI', ter: 0.25, strategy: 'SRI' },
  IE00BJ0KDQ92: { isin: 'IE00BG36TC12', name: 'Xtrackers MSCI World ESG', ter: 0.20, strategy: 'ESG screened' },
  LU0392494562: { isin: 'LU0629459743', name: 'UBS MSCI World SRI', ter: 0.22, strategy: 'SRI' },
  IE00B6R52259: { isin: 'IE00BHZPJ569', name: 'iShares MSCI ACWI SRI', ter: 0.20, strategy: 'SRI' },
  DE0005933931: { isin: 'IE00BZ02LR44', name: 'iShares MSCI Europe SRI', ter: 0.20, strategy: 'SRI' },
};

function Sustainability({ etfs, constituents }: { etfs: LoadState<HoldingRow[]>; constituents: ETFHoldingEntry[] }) {
  return (
    <Section title="Sustainability" subtitle="ESG (environmental, social, governance) strategy read from each fund’s name. Carbon intensity and the score are rough estimates.">
      <Loaded state={etfs} lines={5}>
        {list => {
          if (list.length === 0) return <Statement>No ETFs held, so there is nothing to classify.</Statement>;
          // Merge the same fund held in several accounts.
          const byIsin = new Map<string, { isin: string; name: string; value: number; strategy: EsgStrategy }>();
          for (const h of list) {
            const value = num(h.market_value ?? h.value_eur ?? num(h.avg_cost_basis) * num(h.quantity));
            const existing = byIsin.get(h.security_isin);
            if (existing) existing.value += value;
            else byIsin.set(h.security_isin, { isin: h.security_isin, name: h.name, value, strategy: classifyESG(h.name) });
          }
          const funds = [...byIsin.values()].sort((a, b) => b.value - a.value);
          const total = funds.reduce((s, f) => s + f.value, 0);
          const esgFunds = funds.filter(f => f.strategy !== 'None');
          const esgPct = total > 0 ? (esgFunds.reduce((s, f) => s + f.value, 0) / total) * 100 : 0;

          const hit = (name: string) => CONTROVERSIAL_KEYWORDS.some(k => name.toLowerCase().includes(k));
          const flagged = [...new Set([...constituents.filter(c => hit(c.name)).map(c => c.name), ...funds.filter(f => hit(f.name)).map(f => f.name)])];

          // Rough estimate in tCO2e per million EUR: no strategy 200, ESG 100, SRI / PAB / CTB 50.
          const carbon = total > 0 ? funds.reduce((s, f) => {
            const intensity = f.strategy === 'None' ? 200 : f.strategy === 'SRI' || f.strategy === 'PAB' || f.strategy === 'CTB' ? 50 : 100;
            return s + intensity * (f.value / total);
          }, 0) : 0;
          const score = Math.round(Math.min(50, esgPct / 2) + Math.max(0, 30 - flagged.length * 6) + Math.max(0, Math.min(20, (250 - carbon) / 7.5)));
          const grade = score >= 80 ? 'A' : score >= 60 ? 'B' : score >= 40 ? 'C' : score >= 20 ? 'D' : 'E';

          const withAlternative = funds.filter(f => f.strategy === 'None' && ESG_ALTERNATIVES[f.isin]);

          return (
            <>
              <MetricGrid>
                <MetricCard money={false} label="ESG coverage" value={fmtPct(esgPct, { digits: 0 })} sublabel="Of ETF value" />
                <MetricCard money={false} label="ESG funds" value={`${esgFunds.length} of ${funds.length}`} sublabel="Funds with an ESG strategy" />
                <MetricCard money={false} label="Carbon intensity" value={fmtNum(Math.round(carbon))} sublabel="tCO2e per million €, estimated" />
                <MetricCard money={false} label="Sustainability score" value={`${grade} · ${score} of 100`} sublabel={`${plural(flagged.length, 'flagged holding', 'flagged holdings')}`} />
              </MetricGrid>

              <SubHeadingRow>Strategy by Fund</SubHeadingRow>
              <ul className="divide-y divide-divider border-y border-divider">
                {funds.map(f => (
                  <li key={f.isin} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2 text-[13px]">
                    <span className="min-w-0">
                      <span className="block text-ink">{f.name}</span>
                      <span className="block text-[12px] text-ink-muted">{fmtPct(total > 0 ? (f.value / total) * 100 : 0)} of ETF value · <Money value={f.value} /></span>
                    </span>
                    <Chip tone={f.strategy === 'None' ? 'neutral' : 'success'}>{ESG_LABEL[f.strategy]}</Chip>
                  </li>
                ))}
              </ul>

              {flagged.length > 0 && (
                <>
                  <SubHeadingRow>Flagged Holdings</SubHeadingRow>
                  <p className="mb-2 text-[13px] text-ink-muted">Names that match a controversial-activity keyword. Open an ETF in the breakdown above to include its constituents.</p>
                  <ul className="flex flex-wrap gap-2">
                    {flagged.slice(0, 10).map(name => <li key={name}><Chip tone="danger">{name}</Chip></li>)}
                  </ul>
                </>
              )}

              {withAlternative.length > 0 && (
                <>
                  <SubHeadingRow>ESG Alternatives</SubHeadingRow>
                  <ul className="divide-y divide-divider border-y border-divider">
                    {withAlternative.map(f => {
                      const alt = ESG_ALTERNATIVES[f.isin];
                      return (
                        <li key={f.isin} className="py-2 text-[13px]">
                          <span className="block text-ink">{f.name}</span>
                          <span className="block text-ink-body">
                            ESG equivalent: <span className="font-medium text-ink">{alt.name}</span> · {alt.strategy} · ongoing charge {fmtPct(alt.ter, { digits: 2 })} · <span className="font-mono text-[12px]">{alt.isin}</span>
                          </span>
                        </li>
                      );
                    })}
                  </ul>
                </>
              )}
            </>
          );
        }}
      </Loaded>
    </Section>
  );
}
