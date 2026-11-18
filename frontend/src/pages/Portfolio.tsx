import { useState, useEffect, useCallback, useRef } from 'react';
import type { ReactNode } from 'react';
import { api, type HoldingRow, type PerformanceData, type DividendData, type PerformanceHistoryPoint, type AllocationData, type RebalanceTrade, type Account, type Security, type GoalProgress, type PriceAlertEntry, type NotificationEntry } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import HoldingsTable, { holdingKey } from '../components/HoldingsTable';
import {
  PageHeader, TabBar, TabPanel, useTabParam, Section, SubHeading, MetricCard, MetricGrid,
  Field, NumberField, SelectField, PeriodSelector, filterByPeriod,
  LoadingState, ErrorState, EmptyState, SectionBoundary, useConfirm, Chip, RankedBars, ProgressBar,
} from '../components/ui';
import type { Period } from '../components/ui';
import { useThemeColors } from '../hooks/useThemeColors';
import { useMarketRefresh } from '../hooks/useMarketRefresh';
import UnvestedPanel from '../components/UnvestedPanel';
import {
  escapeHtml, fmtEUR, fmtPct, fmtQty, fmtNum, fmtDate, fmtDateLong, fmtMonth, fmtMonthShort, fmtDayMonth, fmtAxis, labelFor,
} from '../utils/format';

type PortfolioTab = 'overview' | 'dividends' | 'allocation' | 'holdings' | 'goals';
const TAB_IDS: readonly PortfolioTab[] = ['overview', 'dividends', 'allocation', 'holdings', 'goals'];
const PORTFOLIO_TABS: { id: PortfolioTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'dividends', label: 'Dividends' },
  { id: 'allocation', label: 'Allocation' },
  { id: 'holdings', label: 'Holdings' },
  { id: 'goals', label: 'Goals & Alerts' },
];

const IMPORT_HREF = '/settings?tab=data';
/** Alert types a person sets on this page. Everything else (digests, tax reminders) lives under the bell. */
const PRICE_ALERT_TYPES = new Set(['price_above', 'price_below', 'daily_change', 'portfolio_milestone']);

type Tone = 'neutral' | 'positive' | 'negative';
const toneOf = (n: number): Tone => (n > 0 ? 'positive' : n < 0 ? 'negative' : 'neutral');
const toneText = (n: number) => (n > 0 ? 'text-sage' : n < 0 ? 'text-claret' : 'text-ink');

type SecurityDetail = Awaited<ReturnType<typeof api.getSecurityDetail>>;
type HealthScore = Awaited<ReturnType<typeof api.getHealthScore>>;
type SavingsPlans = Awaited<ReturnType<typeof api.getSavingsPlans>>;
type AllocationHistory = Awaited<ReturnType<typeof api.getAllocationHistory>>;

interface SwitchResult {
  current_name: string; total_invested: number; current_value: number; alternative_value: number;
  difference_eur: number; difference_pct: number; correlation: number; low_correlation_warning: boolean;
  unrealized_gain: number; teilfreistellung: number; taxable_gain: number; freibetrag_used: number;
  tax_on_switch: number; net_proceeds: number; break_even_years: number; is_equity: boolean;
}

interface TimeMachineData {
  date: string;
  holdings: { isin: string; name: string; quantity: number; value: number; weight_pct: number }[];
  net_worth: number;
  current: { net_worth: number };
  change: { net_worth_change: number; net_worth_pct: number; contributions: number; market_return: number; dividends: number; assets?: number; purchase_costs?: number; loan_interest?: number };
}

/** One independently loaded resource with loading / error / retry. */
function useLoad<T>(fn: () => Promise<T>) {
  const [state, setState] = useState<{ data: T | null; error: string | null; loading: boolean }>({ data: null, error: null, loading: true });
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const reload = useCallback(() => {
    setState(s => ({ ...s, loading: true, error: null }));
    return fnRef.current()
      .then(data => setState({ data, error: null, loading: false }))
      .catch((e: unknown) => setState(s => ({ data: s.data, error: e instanceof Error ? e.message : 'Request failed', loading: false })));
  }, []);
  useEffect(() => { reload(); }, [reload]);
  return { ...state, reload };
}

function Money({ children, className }: { children: ReactNode; className?: string }) {
  return <span data-privacy="blur" className={className}>{children}</span>;
}

/** "2026-11" → "Nov ’26"; anything else is passed through. */
function monthLabel(m: string, long = false): string {
  if (/^\d{4}-\d{2}$/.test(m)) return long ? fmtMonth(`${m}-01`) : fmtMonthShort(`${m}-01`);
  return m;
}

/**
 * Category time axis whose labels never repeat: short ranges label days, longer ranges label the
 * first point of each month, multi-year ranges the first point of each year.
 */
function timeAxis(dates: string[], period: Period) {
  const shortRange = period === '1M' || period === '3M' || period === '6M' || period === 'YTD';
  const spanDays = dates.length > 1 ? (new Date(dates[dates.length - 1]).getTime() - new Date(dates[0]).getTime()) / 86_400_000 : 0;
  const byYear = spanDays > 900;
  const bucket = (d: string) => (byYear ? d.slice(0, 4) : d.slice(0, 7));
  const useDays = shortRange || new Set(dates.map(bucket)).size <= 2;
  return {
    type: 'category' as const,
    data: dates,
    boundaryGap: false,
    axisLabel: useDays
      ? { formatter: (v: string) => fmtDayMonth(v), hideOverlap: true }
      : {
          interval: (i: number) => i > 0 && bucket(dates[i]) !== bucket(dates[i - 1]),
          formatter: (v: string) => (byYear ? v.slice(0, 4) : fmtMonthShort(v)),
          hideOverlap: true,
        },
  };
}

interface TipParam { seriesName: string; data: number | null | [string, number]; axisValue: string; marker: string }
function axisTooltip(fmtValue: (v: number, series: string) => string, fmtHead: (axisValue: string) => string = fmtDateLong) {
  return {
    trigger: 'axis' as const,
    formatter: (params: unknown) => {
      const ps = params as TipParam[];
      if (!Array.isArray(ps) || ps.length === 0) return '';
      const rows = ps
        .filter(p => p.data != null)
        .map(p => {
          const v = Array.isArray(p.data) ? p.data[1] : (p.data as number);
          return `${p.marker} ${escapeHtml(p.seriesName)}: <strong>${escapeHtml(fmtValue(v, p.seriesName))}</strong>`;
        });
      return `${escapeHtml(fmtHead(ps[0].axisValue))}<br/>${rows.join('<br/>')}`;
    },
  };
}

const LEGEND = { bottom: 0, itemGap: 16, itemWidth: 28, itemHeight: 10, textStyle: { fontSize: 12 } };
/** Legend swatches that show the line style, so two lines are never told apart by colour alone. */
const LINE_ICON = {
  solid: 'path://M0 4h28v2.5H0z',
  dashed: 'path://M0 4h7v2.5H0zM10.5 4h7v2.5h-7zM21 4h7v2.5h-7z',
  dotted: 'path://M0 4h3v2.5H0zM6.25 4h3v2.5h-3zM12.5 4h3v2.5h-3zM18.75 4h3v2.5h-3zM25 4h3v2.5h-3z',
};
const lineLegend = (items: [string, keyof typeof LINE_ICON][]) => ({ ...LEGEND, data: items.map(([name, style]) => ({ name, icon: LINE_ICON[style] })) });
const GRID = { left: 4, right: 16, top: 16, bottom: 44, containLabel: true };

/** Two-or-more-way view switch with pressed state (not tabs: it changes one chart). */
function ViewToggle<T extends string>({ value, options, onChange, label }: { value: T; options: { id: T; label: string }[]; onChange: (v: T) => void; label: string }) {
  return (
    <div className="inline-flex flex-wrap gap-1.5" role="group" aria-label={label}>
      {options.map(o => {
        const active = o.id === value;
        return (
          <button
            key={o.id}
            type="button"
            aria-pressed={active}
            onClick={() => onChange(o.id)}
            className={`min-h-[44px] md:min-h-[32px] px-3 rounded-[4px] border text-[13px] transition-colors ${
              active ? 'bg-forest border-forest text-parchment font-semibold' : 'border-field text-ink-body font-medium hover:border-forest hover:text-ink'
            }`}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

function Rows({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <ul className={`border-y border-divider divide-y divide-divider ${className}`}>{children}</ul>;
}

/* ───────────────────────────── Security detail ───────────────────────────── */

function SecurityDetailPanel({ detail }: { detail: SecurityDetail }) {
  const tc = useThemeColors();
  const [altISIN, setAltISIN] = useState('');
  const [switchResult, setSwitchResult] = useState<SwitchResult | null>(null);
  const [switchLoading, setSwitchLoading] = useState(false);
  const [switchError, setSwitchError] = useState<string | null>(null);

  const compare = async () => {
    if (!altISIN) return;
    setSwitchLoading(true);
    setSwitchResult(null);
    setSwitchError(null);
    try {
      const res = await fetch(`/api/portfolio/switch-compare?current=${encodeURIComponent(detail.isin)}&alternative=${encodeURIComponent(altISIN)}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setSwitchResult(await res.json());
    } catch (e) {
      setSwitchError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setSwitchLoading(false);
    }
  };

  // Buy/sell markers aligned to sparkline dates. The x-axis is category-based, so a marker only
  // renders when its x-value matches an existing category; transactions on weekends/holidays are
  // snapped to the first price date on or after the transaction date.
  const sparkDates = detail.sparkline.map(s => s.date);
  const sparkSet = new Set(sparkDates);
  const priceMap = new Map(detail.sparkline.map(s => [s.date, s.price]));
  const snap = (d: string): string | null => {
    if (sparkSet.has(d)) return d;
    if (sparkDates.length === 0 || d < sparkDates[0] || d > sparkDates[sparkDates.length - 1]) return null;
    return sparkDates.find(x => x >= d) ?? null;
  };
  const buyMarkers: [string, number][] = [];
  const sellMarkers: [string, number][] = [];
  detail.transactions.forEach(t => {
    const s = snap(t.date);
    if (!s) return;
    const price = priceMap.get(s) || 0;
    if (t.type === 'buy' || t.type === 'savings_plan') buyMarkers.push([s, price]);
    else if (t.type === 'sell') sellMarkers.push([s, price]);
  });
  const avgCost = detail.total_quantity > 0 ? detail.total_cost / detail.total_quantity : 0;

  return (
    <div className="flex flex-col gap-6">
      <p className="text-[13px] text-ink-muted">
        {[detail.symbol, labelFor(detail.asset_class), detail.ter > 0 ? `Ongoing charge (TER) ${fmtPct(detail.ter, { digits: 2 })} p.a.` : null,
          detail.first_buy ? `First bought ${fmtDateLong(detail.first_buy)}` : null].filter(Boolean).join(' · ')}
      </p>

      <MetricGrid>
        <MetricCard label="Market value" value={fmtEUR(detail.total_value)} sublabel={`Cost basis ${fmtEUR(detail.total_cost)}`} />
        <MetricCard label="Unrealised gain" value={fmtEUR(detail.unrealized_pl, { sign: true })} tone={toneOf(detail.unrealized_pl)} sublabel="Market value minus cost, in EUR" />
        <MetricCard label="Weight" value={fmtPct(detail.weight_pct)} money={false} sublabel="Share of all holdings" />
        <MetricCard label="Units" value={fmtQty(detail.total_quantity)} money={false} sublabel="Across all accounts" />
      </MetricGrid>

      {detail.sparkline.length > 2 && (
        <div>
          <SubHeading>Price, last {detail.sparkline.length} trading days · EUR</SubHeading>
          <p className="text-[12px] text-ink-muted mt-1 mb-2">
            {[avgCost > 0 ? `Dashed line: average cost ${fmtEUR(avgCost, { cents: true })}` : null,
              buyMarkers.length > 0 ? '● purchases' : null,
              sellMarkers.length > 0 ? '◆ sales' : null].filter(Boolean).join(' · ') || 'Converted at the latest ECB reference rate.'}
          </p>
          <EChartWrapper option={{
            grid: { left: 4, right: 16, top: 12, bottom: 4, containLabel: true },
            xAxis: { type: 'category' as const, data: sparkDates, boundaryGap: false, axisLabel: { formatter: (v: string) => fmtDayMonth(v), hideOverlap: true } },
            yAxis: { type: 'value' as const, scale: true, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
            series: [
              {
                type: 'line' as const, name: 'Price', data: detail.sparkline.map(s => s.price), showSymbol: false,
                color: tc.forest, lineStyle: { color: tc.forest, width: 1.75 }, itemStyle: { color: tc.forest },
                ...(avgCost > 0 ? { markLine: { silent: true, symbol: 'none', data: [{ yAxis: avgCost }], lineStyle: { color: tc.gold, type: 'dashed' as const, width: 1 }, label: { show: false } } } : {}),
              },
              ...(buyMarkers.length > 0 ? [{ type: 'scatter' as const, name: 'Purchase', data: buyMarkers, symbol: 'circle', symbolSize: 8, color: tc.sage, itemStyle: { color: tc.sage } }] : []),
              ...(sellMarkers.length > 0 ? [{ type: 'scatter' as const, name: 'Sale', data: sellMarkers, symbol: 'diamond', symbolSize: 10, color: tc.claret, itemStyle: { color: tc.claret } }] : []),
            ],
            tooltip: axisTooltip(v => fmtEUR(v, { cents: true })),
          }} height="200px" />
        </div>
      )}

      {detail.positions.length > 1 && (
        <div>
          <SubHeading className="mb-2">By account</SubHeading>
          <Rows>
            {detail.positions.map((p, i) => (
              <li key={i} className="flex items-center justify-between gap-3 py-2.5 text-[14px]">
                <span className="text-ink min-w-0 truncate">{p.account}</span>
                <span className="text-ink-body shrink-0">{fmtQty(p.quantity)} units · <Money className="text-ink">{fmtEUR(p.value)}</Money></span>
              </li>
            ))}
          </Rows>
        </div>
      )}

      {detail.transactions.length > 0 && (
        <div>
          <SubHeading className="mb-2">
            Recent transactions{detail.transactions.length > 10 ? ` · last 10 of ${detail.transactions.length}` : ''}
          </SubHeading>
          <div className="overflow-x-auto rounded-apple border border-divider" role="region" tabIndex={0} aria-label={`Recent transactions, ${detail.name}`}>
            <table className="apple-table min-w-[420px]">
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col">Type</th>
                  <th scope="col" className="num">Units</th>
                  <th scope="col" className="num">Amount</th>
                  <th scope="col" className="num">Units held after</th>
                </tr>
              </thead>
              <tbody>
                {detail.transactions.slice(-10).reverse().map((t, i) => (
                  <tr key={i}>
                    <td className="whitespace-nowrap">{fmtDate(t.date)}</td>
                    <td>{labelFor(t.type)}</td>
                    <td className="num">{t.quantity > 0 ? fmtQty(t.quantity) : '—'}</td>
                    <td className="num whitespace-nowrap"><Money>{fmtEUR(t.amount, { cents: true })}</Money></td>
                    <td className="num">{fmtQty(t.running_qty)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Product switch comparison */}
      <div>
        <SubHeading>What if I had bought a different product?</SubHeading>
        <p className="text-[12px] text-ink-muted mt-1 mb-3">Replays your purchases of this holding into another security and estimates the tax cost of switching today.</p>
        <form className="flex flex-wrap items-end gap-3" onSubmit={e => { e.preventDefault(); compare(); }}>
          <Field
            label="Alternative ISIN"
            className="flex-1 basis-56 max-w-sm"
            value={altISIN}
            onChange={e => setAltISIN(e.target.value.toUpperCase().trim())}
            placeholder="e.g. IE00B4L5Y983"
            autoCapitalize="characters"
            spellCheck={false}
          />
          <button type="submit" disabled={switchLoading || !altISIN} className="apple-btn-secondary min-h-[44px]">
            {switchLoading ? 'Comparing…' : 'Compare'}
          </button>
        </form>
        {switchError && <div className="mt-3"><ErrorState message="The comparison couldn’t be computed. Check the ISIN and try again." details={switchError} onRetry={compare} /></div>}

        {switchResult && (
          <div className="mt-4 flex flex-col gap-4">
            <MetricGrid>
              <MetricCard label="This holding" value={fmtEUR(switchResult.current_value)} sublabel="Value today" />
              <MetricCard label="Alternative" value={fmtEUR(switchResult.alternative_value)} sublabel="Same purchases, value today" />
              <MetricCard
                label="Difference"
                value={fmtEUR(switchResult.difference_eur, { sign: true })}
                tone={toneOf(switchResult.difference_eur)}
                sublabel={`${fmtPct(switchResult.difference_pct, { sign: true })} · correlation ${fmtNum(switchResult.correlation, 2)}`}
              />
            </MetricGrid>
            {switchResult.low_correlation_warning && (
              <p className="text-[13px] text-ink-body flex flex-wrap items-center gap-2">
                <Chip tone="warning">Low correlation</Chip>
                These two products track different markets, so the comparison may not be meaningful.
              </p>
            )}

            {switchResult.unrealized_gain !== 0 && (
              <div>
                <SubHeading className="mb-2">Tax cost of switching</SubHeading>
                <dl className="grid grid-cols-[1fr_auto] gap-x-6 gap-y-1.5 text-[14px] max-w-md">
                  <dt className="text-ink-body">Unrealised gain</dt>
                  <dd className={`text-right ${toneText(switchResult.unrealized_gain)}`}><Money>{fmtEUR(switchResult.unrealized_gain, { sign: true })}</Money></dd>
                  {switchResult.is_equity && switchResult.teilfreistellung > 0 && (<>
                    <dt className="text-ink-body">Teilfreistellung (partial exemption, 30 %)</dt>
                    <dd className="text-right text-ink"><Money>{fmtEUR(-switchResult.teilfreistellung)}</Money></dd>
                  </>)}
                  {switchResult.freibetrag_used > 0 && (<>
                    <dt className="text-ink-body">Freibetrag (tax-free allowance used)</dt>
                    <dd className="text-right text-ink"><Money>{fmtEUR(-switchResult.freibetrag_used)}</Money></dd>
                  </>)}
                  <dt className="text-ink font-medium">Tax due on switching</dt>
                  <dd className="text-right text-ink font-medium"><Money>{fmtEUR(switchResult.tax_on_switch)}</Money></dd>
                  <dt className="text-ink-body">Net proceeds</dt>
                  <dd className="text-right text-ink"><Money>{fmtEUR(switchResult.net_proceeds)}</Money></dd>
                </dl>
                {switchResult.break_even_years > 0 ? (
                  <p className="text-[13px] text-ink-muted mt-2">Break-even: about {fmtNum(switchResult.break_even_years, switchResult.break_even_years % 1 === 0 ? 0 : 1)} {switchResult.break_even_years === 1 ? 'year' : 'years'} for the alternative to recover the tax cost.</p>
                ) : switchResult.difference_eur < 0 && switchResult.tax_on_switch > 0 ? (
                  <p className="text-[13px] text-ink-body mt-2 flex flex-wrap items-center gap-2"><Chip tone="danger">No break-even</Chip> The alternative has historically underperformed, so the tax cost would not be recovered.</p>
                ) : null}
              </div>
            )}

            <div>
              <SubHeading className="mb-2">Ways to switch</SubHeading>
              {switchResult.difference_eur < 0 && switchResult.unrealized_gain >= 0 ? (
                <p className="text-[13px] text-ink-body">No switch suggested: the alternative has historically underperformed by <Money>{fmtEUR(Math.abs(switchResult.difference_eur))}</Money>.</p>
              ) : (
                <ol className="list-decimal pl-5 flex flex-col gap-2 text-[13px] text-ink-body">
                  <li>
                    <span className="font-medium text-ink">Redirect future contributions.</span>{' '}
                    Stop buying the current product and invest new money in the alternative. No tax event; the transition happens gradually.
                  </li>
                  {switchResult.tax_on_switch > 1000 && (
                    <li>
                      <span className="font-medium text-ink">Split the sale across two tax years.</span>{' '}
                      Sell about <Money>{fmtEUR(switchResult.current_value / 2)}</Money> per year to use the Sparer-Pauschbetrag (saver’s allowance) twice, saving roughly <Money>{fmtEUR(Math.min(1000, switchResult.taxable_gain) * 0.26375)}</Money>.
                    </li>
                  )}
                  {switchResult.unrealized_gain < 0 && (
                    <li>
                      <span className="font-medium text-ink">Harvest the loss.</span>{' '}
                      The holding carries <Money>{fmtEUR(Math.abs(switchResult.unrealized_gain))}</Money> of unrealised losses. Selling now realises them, which offsets future gains (about <Money>{fmtEUR(Math.abs(switchResult.unrealized_gain) * 0.26375)}</Money> of tax).
                    </li>
                  )}
                  {switchResult.unrealized_gain >= 0 && switchResult.tax_on_switch <= 1000 && switchResult.tax_on_switch > 0 && (
                    <li>
                      <span className="font-medium text-ink">Switch in one go.</span>{' '}
                      A tax cost of <Money>{fmtEUR(switchResult.tax_on_switch)}</Money> is modest; a single switch may be simpler than a gradual one.
                    </li>
                  )}
                </ol>
              )}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/* ───────────────────────────────── Page ───────────────────────────────── */

export default function Portfolio() {
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
  const [activeTab, setActiveTab] = useTabParam<PortfolioTab>(TAB_IDS, 'overview');

  // Core data: holdings, performance, dividends
  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [performance, setPerformance] = useState<PerformanceData | null>(null);
  const [dividends, setDividends] = useState<DividendData | null>(null);
  const [priceAsOf, setPriceAsOf] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [filterAccount, setFilterAccount] = useState<string>('');

  const [dividendView, setDividendView] = useState<'monthly' | 'yearly' | 'cumulative'>('monthly');
  const [growthPeriod, setGrowthPeriod] = useState<Period>('All');
  const [allocPeriod, setAllocPeriod] = useState<Period>('All');

  // Allocation
  const [editingTargets, setEditingTargets] = useState(false);
  const [targetDrafts, setTargetDrafts] = useState<Record<string, number>>({});
  const [targetError, setTargetError] = useState<string | null>(null);
  const [rebalanceTrades, setRebalanceTrades] = useState<RebalanceTrade[]>([]);
  const [rebalanceMsg, setRebalanceMsg] = useState('');
  const [rebalanceError, setRebalanceError] = useState<string | null>(null);
  const [rebalanceDone, setRebalanceDone] = useState(false);
  const [depositAmount, setDepositAmount] = useState(0);

  // Holdings: expanded security detail, time machine
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const [securityDetail, setSecurityDetail] = useState<SecurityDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);
  const detailRequest = useRef<string | null>(null);
  const [tmDate, setTmDate] = useState('');
  const [tmData, setTmData] = useState<TimeMachineData | null>(null);
  const [tmLoading, setTmLoading] = useState(false);
  const [tmError, setTmError] = useState<string | null>(null);

  // Goals & alerts forms
  const [goalName, setGoalName] = useState('');
  const [goalTarget, setGoalTarget] = useState(0);
  const [goalDate, setGoalDate] = useState('');
  const [goalContrib, setGoalContrib] = useState(0);
  const [goalReturn, setGoalReturn] = useState(7);
  const [goalError, setGoalError] = useState<string | null>(null);
  const [alertType, setAlertType] = useState('price_above');
  const [alertISIN, setAlertISIN] = useState('');
  const [alertThreshold, setAlertThreshold] = useState(0);
  const [alertError, setAlertError] = useState<string | null>(null);

  const loadPortfolio = useCallback(() => {
    const acctId = filterAccount || undefined;
    Promise.all([
      api.listHoldings(acctId),
      api.getPerformance(acctId),
      api.getDividends(),
    ])
      .then(([holdRes, perfRes, divRes]) => {
        setHoldings(holdRes.holdings || []);
        setPriceAsOf(holdRes.price_as_of || null);
        setPerformance(perfRes);
        setDividends(divRes);
        setError(null);
      })
      .catch(e => setError(e.message || 'Failed to load portfolio data'))
      .finally(() => setLoading(false));
  }, [filterAccount]);

  useEffect(() => { loadPortfolio(); }, [loadPortfolio]);

  useEffect(() => {
    api.listAccounts().then(res => setAccounts((res.accounts || []).filter(a => ['brokerage', 'crypto', 'pension', 'precious_metals'].includes(a.type)))).catch(() => {});
  }, []);

  const perfHistory = useLoad<PerformanceHistoryPoint[]>(() => api.getPerformanceHistory().then(r => r.history || []));
  const allocation = useLoad<AllocationData>(() => api.getAllocation());
  const allocHistory = useLoad<AllocationHistory>(() => api.getAllocationHistory());
  const healthScore = useLoad<HealthScore>(() => api.getHealthScore());
  const savingsPlans = useLoad<SavingsPlans>(() => api.getSavingsPlans());
  const securities = useLoad<Security[]>(() => api.listSecurities().then(r => r.securities || []));
  // /portfolio/goals carries progress, status and projection; /settings/goals is the bare list.
  const goals = useLoad<GoalProgress[]>(() => api.getGoalsProgress().then(r => r.goals || []));
  // Every account (cash included) can fund a goal.
  const goalAccounts = useLoad<Account[]>(() => api.listAccounts().then(r => r.accounts || []));
  const [goalFunding, setGoalFunding] = useState('');
  const [goalPriority, setGoalPriority] = useState(50);
  const alerts = useLoad<PriceAlertEntry[]>(() => api.listAlerts().then(r => r.alerts || []));
  const notifications = useLoad<NotificationEntry[]>(() => api.listNotifications().then(r => r.notifications || []));

  // Auto-refresh during market hours (every 60s)
  useMarketRefresh(loadPortfolio, 60_000);

  const retryCore = () => { setError(null); setLoading(true); loadPortfolio(); };

  // ── Totals. `market_value`, `unrealized_pl` and `value_eur` are EUR (converted server-side);
  // `avg_cost_basis` is per share in the trading currency and is never summed here. An unpriced
  // holding counts at `value_eur`, which is its EUR cost. Cost basis = value − gain, so both sides
  // of every percentage are EUR.
  const hasMarketPrices = holdings.some(h => h.market_value != null);
  const fullyPriced = holdings.length > 0 && holdings.every(h => h.market_value != null);
  const pricedCount = holdings.filter(h => h.market_value != null).length;
  const totalMarketValue = holdings.reduce((sum, h) => sum + (h.market_value ?? h.value_eur ?? 0), 0);
  const totalPL = holdings.reduce((sum, h) => sum + (h.unrealized_pl ?? 0), 0);
  const totalCostBasis = totalMarketValue - totalPL;
  const totalPLPct = totalCostBasis > 0 ? (totalPL / totalCostBasis) * 100 : null;
  // Dividends per holding come in that holding's own currency.
  const totalDividends = holdings.reduce((sum, h) => sum + h.total_dividends, 0);
  const dividendsMixed = holdings.some(h => h.total_dividends > 0 && (h.currency || 'EUR').toUpperCase() !== 'EUR');

  const accountFilter = accounts.length > 1 ? (
    <SelectField label="Account" value={filterAccount} onChange={e => setFilterAccount(e.target.value)} className="max-w-xs mt-6">
      <option value="">All accounts</option>
      {accounts.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
    </SelectField>
  ) : null;

  const kpiRow = (
    <MetricGrid className="mt-6">
      <MetricCard
        label={fullyPriced ? 'Market value' : hasMarketPrices ? 'Value · partly at cost' : 'Value · at cost'}
        value={fmtEUR(totalMarketValue)}
        sublabel={`${holdings.length} ${holdings.length === 1 ? 'holding' : 'holdings'}, converted to EUR${!fullyPriced && hasMarketPrices ? ` · ${pricedCount} of ${holdings.length} priced` : ''}`}
      />
      <MetricCard label="Cost basis" value={fmtEUR(totalCostBasis)} sublabel="Purchase cost of current holdings, in EUR" />
      {hasMarketPrices && (
        <MetricCard
          label="Unrealised gain"
          value={fmtEUR(totalPL, { sign: true })}
          tone={toneOf(totalPL)}
          sublabel={totalPLPct != null ? `${fmtPct(totalPLPct, { sign: true })} on cost basis` : 'Value minus cost basis'}
        />
      )}
      <MetricCard
        label="Dividends received"
        value={fmtEUR(totalDividends)}
        sublabel={dividendsMixed ? 'Current holdings · sums payouts in their own currencies, unconverted' : 'On current holdings, since purchase'}
      />
    </MetricGrid>
  );

  const coreGate = (render: () => ReactNode): ReactNode => {
    if (loading) return <div className="mt-6"><LoadingState message="Loading portfolio" lines={6} /></div>;
    if (error) return <div className="mt-6"><ErrorState message="Portfolio data couldn’t be loaded." details={error} onRetry={retryCore} /></div>;
    return render();
  };

  /* ───────────── Overview ───────────── */

  const renderPerformance = () => {
    if (!performance) return null;
    const p = performance;
    // The API's total gain is "current value − net capital invested", where net capital also
    // counts in-kind transfers. Deriving it back keeps the two figures consistent with each other.
    const netCapital = p.current_value - p.total_return;
    const ok = (n: number) => Number.isFinite(n);
    const capitalOk = ok(netCapital) && ok(p.current_value) && !(netCapital <= 0 && p.current_value > 0);
    const twrOk = ok(p.twr) && p.twr > -100;
    const irrOk = ok(p.irr) && p.irr > -100 && p.irr < 1000;
    const gainsOk = capitalOk && ok(p.total_return);
    const dash = (caption: string) => ({ value: '—' as ReactNode, tone: 'neutral' as Tone, sublabel: caption, money: false });
    return (
      <Section title="Performance" subtitle="All figures in EUR. “Since inception” covers every transaction on record for the selected account.">
        <MetricGrid className="md:!grid-cols-3">
          <MetricCard
            label="Net capital invested"
            {...(capitalOk
              ? { value: fmtEUR(netCapital), sublabel: 'Deposits and in-kind transfers (such as RSU vests) minus withdrawals, since inception' }
              : dash('Not enough history: recorded withdrawals exceed recorded deposits'))}
          />
          <MetricCard
            label="Total gain · since inception"
            {...(gainsOk
              ? {
                  value: fmtEUR(p.total_return, { sign: true }),
                  tone: toneOf(p.total_return),
                  sublabel: ok(p.total_return_pct) ? `${fmtPct(p.total_return_pct, { sign: true })} of net capital invested · current value minus net capital` : 'Current value minus net capital invested',
                }
              : dash('Not enough history: needs complete deposit records'))}
          />
          <MetricCard
            label="Time-weighted return · 12 months"
            {...(twrOk
              ? { value: fmtPct(p.twr, { sign: true }), tone: toneOf(p.twr), sublabel: 'TWR over the trailing 12 months, whole portfolio; ignores the timing of deposits', money: false }
              : dash('Not enough history for a 12-month return'))}
          />
          <MetricCard
            label="Money-weighted return · p.a."
            {...(irrOk
              ? { value: fmtPct(p.irr, { sign: true }), tone: toneOf(p.irr), sublabel: 'IRR per year since inception; reflects when money went in and out', money: false }
              : dash('Not enough history for an annual rate'))}
          />
          <MetricCard
            label="Realised gain · since inception"
            {...(ok(p.realized_pl)
              ? { value: fmtEUR(p.realized_pl, { sign: true }), tone: toneOf(p.realized_pl), sublabel: 'On positions sold, average-cost method' }
              : dash('Not available'))}
          />
          <MetricCard
            label="Remaining gain · since inception"
            {...(gainsOk && ok(p.unrealized_pl)
              ? { value: fmtEUR(p.unrealized_pl, { sign: true }), tone: toneOf(p.unrealized_pl), sublabel: 'Total gain minus realised gain; besides price gains it contains dividends, interest and fees' }
              : dash('Not enough history: needs complete deposit records'))}
          />
        </MetricGrid>
      </Section>
    );
  };

  const renderGrowth = () => {
    if (perfHistory.loading && !perfHistory.data) return <Section title="Growth Over Time"><LoadingState lines={5} /></Section>;
    if (perfHistory.error && !perfHistory.data) return <Section title="Growth Over Time"><ErrorState message="The growth history couldn’t be loaded." details={perfHistory.error} onRetry={perfHistory.reload} /></Section>;
    const all = perfHistory.data || [];
    const filtered = filterByPeriod(all, growthPeriod);
    const selector = <PeriodSelector value={growthPeriod} onChange={setGrowthPeriod} />;
    if (all.length < 2) {
      return (
        <Section title="Growth Over Time">
          <p className="text-[14px] text-ink-body">Not enough history yet. The chart appears once the portfolio has been valued on at least two days.</p>
        </Section>
      );
    }
    if (filtered.length < 2) {
      return (
        <Section title="Growth Over Time" actions={selector}>
          <p className="text-[14px] text-ink-body">No data in this period. Choose a longer one.</p>
        </Section>
      );
    }
    const dates = filtered.map(p => p.date);
    const hasInKind = filtered.some(p => p.in_kind_invested > 0);
    const hasBenchmark = filtered.some(p => p.benchmark_pct != null);
    const line = (name: string, color: string, data: (number | null)[], extra: Record<string, unknown> = {}) => ({
      name, type: 'line' as const, data, symbol: 'none', smooth: 0.3,
      color, itemStyle: { color }, lineStyle: { color, width: 2 }, ...extra,
    });
    return (
      <Section
        title="Growth Over Time"
        actions={selector}
        subtitle={`Capital invested is cash deposits${hasInKind ? ' plus RSU vests at their fair-market value on the vest date' : ''}, net of withdrawals.`}
      >
        <SubHeading>Portfolio value and capital invested · EUR</SubHeading>
        <EChartWrapper option={{
          tooltip: axisTooltip(v => fmtEUR(v)),
          legend: lineLegend([['Portfolio value', 'solid'], ['Capital invested', 'dashed'], ...(hasInKind ? [['Of which cash deposits', 'dotted'] as [string, 'dotted']] : [])]),
          xAxis: timeAxis(dates, growthPeriod),
          yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
          series: [
            line('Portfolio value', tc.forest, filtered.map(p => Math.round(p.portfolio_value)), { lineStyle: { color: tc.forest, width: 2.5 }, z: 3 }),
            line('Capital invested', tc.inkMuted, filtered.map(p => Math.round(p.cash_invested + p.in_kind_invested)), {
              lineStyle: { color: tc.inkMuted, width: 1.5, type: 'dashed' as const }, areaStyle: { color: tc.inkMuted, opacity: 0.08 },
            }),
            ...(hasInKind ? [line('Of which cash deposits', tc.walnut, filtered.map(p => Math.round(p.cash_invested)), {
              lineStyle: { color: tc.walnut, width: 1.5, type: 'dotted' as const },
            })] : []),
          ],
          grid: GRID,
        }} height="300px" />

        <SubHeading className="mt-6">Cumulative return{hasBenchmark ? ' against MSCI World' : ''} · %</SubHeading>
        <EChartWrapper option={{
          tooltip: axisTooltip(v => fmtPct(v, { sign: true })),
          legend: lineLegend([['Portfolio return', 'solid'], ...(hasBenchmark ? [['MSCI World', 'dashed'] as [string, 'dashed']] : [])]),
          xAxis: timeAxis(dates, growthPeriod),
          yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtPct(v, { digits: 0 }) } },
          series: [
            line('Portfolio return', tc.forest, filtered.map(p => Math.round(p.return_pct * 10) / 10)),
            ...(hasBenchmark ? [line('MSCI World', tc.walnut, filtered.map(p => (p.benchmark_pct != null ? Math.round(p.benchmark_pct * 10) / 10 : null)), {
              lineStyle: { color: tc.walnut, width: 1.5, type: 'dashed' as const },
            })] : []),
          ],
          grid: GRID,
        }} height="220px" />
      </Section>
    );
  };

  const renderHealth = () => {
    const hs = healthScore.data;
    if (healthScore.error && !hs) return <Section title="Portfolio Health"><ErrorState message="The health score couldn’t be loaded." details={healthScore.error} onRetry={healthScore.reload} /></Section>;
    if (!hs || hs.score <= 0) return null;
    const verdict = hs.score >= 80 ? { tone: 'success' as const, text: 'Good' } : hs.score >= 60 ? { tone: 'warning' as const, text: 'Fair' } : { tone: 'danger' as const, text: 'Needs attention' };
    const statusTone = (s: string) => (s === 'good' ? 'success' as const : s === 'fair' ? 'warning' as const : 'danger' as const);
    return (
      <Section title="Portfolio Health" subtitle="A 0–100 score built from the parts below; higher is better.">
        <div className="flex flex-wrap items-center gap-3 mb-4">
          <span className="font-serif text-[32px] leading-9 text-ink">{fmtNum(hs.score)}<span className="text-[18px] text-ink-muted"> / 100</span></span>
          <Chip tone={verdict.tone}>{verdict.text}</Chip>
        </div>
        <Rows>
          {hs.subscores.map(s => (
            <li key={s.name} className="py-2.5 grid grid-cols-[1fr_auto] md:grid-cols-[minmax(0,1.2fr)_minmax(80px,1fr)_auto_auto] items-center gap-x-4 gap-y-1 text-[14px]">
              <span className="min-w-0">
                <span className="text-ink">{s.name}</span>
                {s.detail && <span className="block text-[12px] text-ink-muted">{s.detail}</span>}
              </span>
              <ProgressBar value={s.score} label={`${s.name} score`} className="hidden md:block" />
              <span className="text-ink text-right">{fmtNum(s.score)}<span className="text-ink-muted"> / 100</span></span>
              <span className="col-span-2 md:col-span-1 md:text-right"><Chip tone={statusTone(s.status)}>{labelFor(s.status)}</Chip></span>
            </li>
          ))}
        </Rows>
      </Section>
    );
  };

  const renderOverview = () => coreGate(() => {
    if (holdings.length === 0 && !(performance && performance.current_value > 0)) {
      return (
        <div className="mt-6">
          {accountFilter}
          <div className="mt-6">
            <EmptyState title="No holdings yet" message="Import your broker’s transactions to see value, gains and performance here." action={{ label: 'Import transactions', href: IMPORT_HREF }} />
          </div>
        </div>
      );
    }
    return (
      <>
        {accountFilter}
        <SectionBoundary>{kpiRow}</SectionBoundary>
        <SectionBoundary>{renderHealth()}</SectionBoundary>
        <SectionBoundary>{renderPerformance()}</SectionBoundary>
        <SectionBoundary>{renderGrowth()}</SectionBoundary>
      </>
    );
  });

  /* ───────────── Dividends ───────────── */

  const renderDividends = () => coreGate(() => {
    if (!dividends || !(dividends.total > 0)) {
      return (
        <div className="mt-6">
          <EmptyState
            title="No dividends yet"
            message="Dividends and distributions appear here once a holding pays out and the transaction has been imported."
            action={{ label: 'Import transactions', href: IMPORT_HREF }}
          />
        </div>
      );
    }
    const d = dividends;
    const growthOk = Number.isFinite(d.dividend_growth) && Math.abs(d.dividend_growth) <= 500;
    const monthly = d.monthly || [];
    const yearly = d.yearly || [];
    const cumulative = d.cumulative || [];
    const views = [
      { id: 'monthly' as const, label: 'By month' },
      { id: 'yearly' as const, label: 'By year' },
      { id: 'cumulative' as const, label: 'Cumulative' },
    ];
    const bars = dividendView === 'yearly'
      ? { labels: yearly.map(x => x.year), values: yearly.map(x => Math.round(x.amount * 100) / 100) }
      : { labels: monthly.map(x => x.month), values: monthly.map(x => Math.round(x.amount * 100) / 100) };
    const head = (v: string) => monthLabel(v, true);
    const bySecurityTotal = (d.by_security || []).reduce((s, x) => s + x.amount, 0);

    // Group expected payouts by month
    const byMonth: Record<string, { name: string; expected: number }[]> = {};
    (d.calendar || []).forEach(c => { (byMonth[c.month] ??= []).push({ name: c.name, expected: c.expected }); });
    const upcoming = Object.entries(byMonth).slice(0, 6);

    let chart: ReactNode;
    if (dividendView === 'cumulative') {
      chart = cumulative.length > 1 ? (
        <EChartWrapper option={{
          tooltip: axisTooltip(v => fmtEUR(v, { cents: true }), head),
          xAxis: { type: 'category' as const, data: cumulative.map(c => c.month), boundaryGap: false, axisLabel: { formatter: (v: string) => monthLabel(v), hideOverlap: true } },
          yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
          series: [{
            name: 'Dividends received to date', type: 'line' as const, data: cumulative.map(c => c.cumulative), symbol: 'none',
            color: tc.forest, itemStyle: { color: tc.forest }, lineStyle: { color: tc.forest, width: 2 }, areaStyle: { color: tc.forest, opacity: 0.08 },
          }],
          grid: { ...GRID, bottom: 4 },
        }} height="260px" />
      ) : <p className="text-[14px] text-ink-body">Not enough history for a cumulative view yet.</p>;
    } else {
      chart = bars.labels.length > 0 ? (
        <EChartWrapper option={{
          tooltip: axisTooltip(v => fmtEUR(v, { cents: true }), head),
          xAxis: { type: 'category' as const, data: bars.labels, axisLabel: { formatter: (v: string) => monthLabel(v), hideOverlap: true } },
          yAxis: { type: 'value' as const, min: 0, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
          series: [{
            name: dividendView === 'yearly' ? 'Dividends in the year' : 'Dividends in the month', type: 'bar' as const, data: bars.values,
            color: tc.forest, itemStyle: { color: tc.forest, borderRadius: [2, 2, 0, 0] }, barMaxWidth: dividendView === 'yearly' ? 48 : 28,
          }],
          grid: { ...GRID, bottom: 4 },
        }} height="260px" />
      ) : <p className="text-[14px] text-ink-body">No dividends recorded for this view.</p>;
    }

    return (
      <>
        <SectionBoundary>
          <Section title="Dividend Income" first subtitle="Dividends and distributions received across all accounts, in EUR.">
            <MetricGrid>
              <MetricCard label="Total received" value={fmtEUR(d.total)} sublabel="Since the first payout" />
              <MetricCard label="Trailing 12 months" value={fmtEUR(d.trailing_12m)} sublabel="Received in the last 12 months" />
              <MetricCard label="Yield on cost" value={fmtPct(d.yield_on_cost, { digits: 2 })} money={false} sublabel="Trailing 12 months ÷ cost basis" />
              <MetricCard
                label="Growth · year on year"
                value={growthOk ? fmtPct(d.dividend_growth, { sign: true }) : '—'}
                tone={growthOk ? toneOf(d.dividend_growth) : 'neutral'}
                money={false}
                sublabel={growthOk ? 'Trailing 12 months against the 12 before' : 'Not enough history'}
              />
            </MetricGrid>
          </Section>
        </SectionBoundary>

        <SectionBoundary>
          <Section title="Payout History" actions={<ViewToggle value={dividendView} options={views} onChange={setDividendView} label="Dividend view" />} subtitle="In EUR.">
            {chart}
          </Section>
        </SectionBoundary>

        {d.by_security && d.by_security.length > 0 && (
          <SectionBoundary>
            <Section title="By Security" subtitle="Total received per security, in EUR.">
              <RankedBars items={d.by_security.map(s => ({
                label: s.name,
                value: s.amount,
                display: fmtEUR(s.amount, { cents: true }),
                share: bySecurityTotal > 0 ? fmtPct((s.amount / bySecurityTotal) * 100) : undefined,
              }))} />
            </Section>
          </SectionBoundary>
        )}

        {upcoming.length > 0 && (
          <SectionBoundary>
            <Section title="Expected Upcoming Dividends" subtitle="Estimates based on past payouts of your current holdings; not announced amounts.">
              <div className="flex flex-col gap-4">
                {upcoming.map(([month, entries]) => (
                  <div key={month}>
                    <SubHeading className="mb-1">{monthLabel(month, true)}</SubHeading>
                    <Rows>
                      {entries.map((e, i) => (
                        <li key={i} className="flex items-center justify-between gap-3 py-2 text-[14px]">
                          <span className="text-ink-body min-w-0 truncate">{e.name}</span>
                          <Money className="text-ink shrink-0">≈ {fmtEUR(e.expected, { cents: true })}</Money>
                        </li>
                      ))}
                    </Rows>
                  </div>
                ))}
              </div>
            </Section>
          </SectionBoundary>
        )}
      </>
    );
  });

  /* ───────────── Allocation ───────────── */

  const startEditingTargets = (a: AllocationData) => {
    const drafts: Record<string, number> = {};
    for (const x of a.allocations) drafts[x.isin] = x.target_pct;
    setTargetDrafts(drafts);
    setTargetError(null);
    setEditingTargets(true);
  };
  const saveTargets = () => {
    const allocs = Object.entries(targetDrafts).map(([isin, val]) => ({ isin, target_pct: Number.isFinite(val) ? val : 0 }));
    setTargetError(null);
    api.setAllocation(allocs)
      .then(() => { setEditingTargets(false); allocation.reload(); })
      .catch((e: Error) => setTargetError(e.message || 'Request failed'));
  };
  const computeRebalance = () => {
    setRebalanceError(null);
    api.getRebalance(depositAmount > 0 ? depositAmount : undefined)
      .then(r => { setRebalanceTrades(r.trades || []); setRebalanceMsg(r.message || ''); setRebalanceDone(true); })
      .catch((e: Error) => setRebalanceError(e.message || 'Request failed'));
  };

  const renderTargets = () => {
    const a = allocation.data;
    if (allocation.loading && !a) return <Section title="Target Allocation" first><LoadingState lines={5} /></Section>;
    if (allocation.error && !a) return <Section title="Target Allocation" first><ErrorState message="The allocation couldn’t be loaded." details={allocation.error} onRetry={allocation.reload} /></Section>;
    if (!a || a.allocations.length === 0) {
      return (
        <Section title="Target Allocation" first>
          <EmptyState title="Nothing to allocate yet" message="Once you hold securities you can set a target weight for each and see how far the portfolio has drifted." action={{ label: 'Import transactions', href: IMPORT_HREF }} />
        </Section>
      );
    }
    const drift = Math.abs(a.max_drift);
    const driftChip = drift > 5 ? { tone: 'danger' as const, text: 'Rebalancing due' } : drift > 2 ? { tone: 'warning' as const, text: 'Drifting' } : { tone: 'success' as const, text: 'On target' };
    const statusTone = (s: string) => (s === 'on_target' ? 'success' as const : s === 'overweight' ? 'danger' as const : 'warning' as const);
    const draftSum = Object.values(targetDrafts).reduce((s, v) => s + (Number.isFinite(v) ? v : 0), 0);
    return (
      <Section
        title="Target Allocation"
        first
        subtitle={a.has_targets
          ? <span className="inline-flex flex-wrap items-center gap-2">Largest drift from target: {fmtPct(a.max_drift, { sign: true })} <Chip tone={driftChip.tone}>{driftChip.text}</Chip></span>
          : 'Actual weight of each holding. Set targets to see drift and rebalancing suggestions.'}
        actions={editingTargets ? (
          <>
            <button type="button" className="apple-btn-secondary min-h-[44px] md:min-h-[40px]" onClick={() => setEditingTargets(false)}>Cancel</button>
            <button type="button" className="apple-btn-primary min-h-[44px] md:min-h-[40px]" onClick={saveTargets}>Save targets</button>
          </>
        ) : (
          <button type="button" className="apple-btn-secondary min-h-[44px] md:min-h-[40px]" onClick={() => startEditingTargets(a)}>Edit targets</button>
        )}
      >
        {targetError && <div className="mb-4"><ErrorState message="The targets couldn’t be saved." details={targetError} /></div>}
        {editingTargets ? (
          <>
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
              {a.allocations.map(x => (
                <NumberField
                  key={x.isin}
                  label={x.name}
                  unit="%"
                  value={targetDrafts[x.isin] ?? 0}
                  onValueChange={v => setTargetDrafts(d => ({ ...d, [x.isin]: Math.max(0, Math.min(100, v)) }))}
                  hint={`Currently ${fmtPct(x.actual_pct)}`}
                />
              ))}
            </div>
            <p className="mt-4 text-[13px] text-ink-body flex flex-wrap items-center gap-2">
              Targets add up to {fmtPct(draftSum)}.
              {Math.abs(draftSum - 100) > 0.05 && <Chip tone="warning">Should total 100 %</Chip>}
            </p>
          </>
        ) : (
          <ul className="flex flex-col gap-4">
            {a.allocations.map(x => (
              <li key={x.isin}>
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 mb-1.5">
                  <span className="text-[14px] font-medium text-ink min-w-0 truncate">{x.name}</span>
                  <span className="flex items-center gap-2 text-[13px] text-ink-body shrink-0">
                    {fmtPct(x.actual_pct)}{x.target_pct > 0 ? ` of ${fmtPct(x.target_pct)} target` : ''}
                    {x.target_pct > 0 && <Chip tone={statusTone(x.status)}>{labelFor(x.status)} {fmtPct(x.drift_pct, { sign: true })}</Chip>}
                  </span>
                </div>
                {/* Bar shows the actual weight; the ink tick marks the target. */}
                <div
                  className="relative h-2"
                  role="img"
                  aria-label={`${x.name}: ${fmtPct(x.actual_pct)} actual${x.target_pct > 0 ? `, ${fmtPct(x.target_pct)} target` : ''}`}
                >
                  <div className="h-2 rounded-[2px] bg-divider overflow-hidden">
                    <div className="h-full bg-forest" style={{ width: `${Math.max(0, Math.min(x.actual_pct, 100))}%` }} />
                  </div>
                  {x.target_pct > 0 && x.target_pct <= 100 && (
                    <span className="absolute -top-1 h-4 w-[2px] bg-ink outline outline-1 outline-parchment" style={{ left: `calc(${x.target_pct}% - 1px)` }} />
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Section>
    );
  };

  const renderRebalancing = () => {
    const a = allocation.data;
    if (!a || a.allocations.length === 0) return null;
    if (!a.has_targets) {
      return (
        <Section title="Rebalancing">
          <p className="text-[14px] text-ink-body">Set target weights above to get buy and sell suggestions.</p>
        </Section>
      );
    }
    return (
      <Section title="Rebalancing" subtitle="Suggested trades to bring the portfolio back to its targets, in EUR.">
        <form className="flex flex-wrap items-end gap-3" onSubmit={e => { e.preventDefault(); computeRebalance(); }}>
          <NumberField
            label="New deposit to allocate"
            unit="€"
            value={depositAmount}
            onValueChange={v => setDepositAmount(Math.max(0, v))}
            className="flex-1 basis-56 max-w-xs"
          />
          <button type="submit" className="apple-btn-primary min-h-[44px]">
            {depositAmount > 0 ? 'Allocate deposit' : 'Compute trades'}
          </button>
        </form>
        <p className="mt-2 text-[12px] text-ink-muted">Leave the deposit at 0 to rebalance the existing holdings by buying and selling.</p>

        {rebalanceError && <div className="mt-4"><ErrorState message="The trades couldn’t be computed." details={rebalanceError} onRetry={computeRebalance} /></div>}
        {rebalanceMsg && <p className="text-[13px] text-ink-body mt-4">{rebalanceMsg}</p>}

        {rebalanceTrades.length > 0 && (
          <Rows className="mt-4">
            {rebalanceTrades.map(t => (
              <li key={t.isin} className="flex items-center justify-between gap-3 py-3">
                <div className="min-w-0 flex-1">
                  <p className="text-[14px] font-medium text-ink truncate">{t.name}</p>
                  <p className="text-[12px] text-ink-muted">{fmtPct(t.current_pct)} now → {fmtPct(t.target_pct)} target</p>
                </div>
                <div className="text-right shrink-0">
                  <p className="text-[14px] text-ink flex items-center justify-end gap-2">
                    <Chip tone={t.action === 'buy' ? 'success' : 'danger'}>{t.action === 'buy' ? 'Buy' : 'Sell'}</Chip>
                    <Money className="font-medium">{fmtEUR(t.amount)}</Money>
                  </p>
                  {t.shares != null && t.shares > 0 && <p className="text-[12px] text-ink-muted">about {fmtQty(t.shares)} units</p>}
                </div>
              </li>
            ))}
          </Rows>
        )}
        {rebalanceDone && rebalanceTrades.length === 0 && !rebalanceError && (
          <p className="text-[14px] text-ink-body mt-4">No trades needed: every holding is within tolerance of its target.</p>
        )}
      </Section>
    );
  };

  const renderAllocHistory = () => {
    const title = 'Allocation History';
    const ah = allocHistory.data;
    if (allocHistory.loading && !ah) return <Section title={title}><LoadingState lines={5} /></Section>;
    if (allocHistory.error && !ah) return <Section title={title}><ErrorState message="The allocation history couldn’t be loaded." details={allocHistory.error} onRetry={allocHistory.reload} /></Section>;
    const history = ah?.history || [];
    const names = ah?.holdings || [];
    if (history.length <= 3 || names.length === 0) {
      return (
        <Section title={title}>
          <p className="text-[14px] text-ink-body">Not enough history yet. The chart appears after a few days of portfolio snapshots.</p>
        </Section>
      );
    }
    if (names.length === 1) {
      return (
        <Section title={title}>
          <p className="text-[14px] text-ink-body">
            {names[0]} has been the only holding since {fmtDateLong(history[0].date)}, so it has made up 100 % of the portfolio throughout. A history appears once there is a second holding.
          </p>
        </Section>
      );
    }
    const filtered = filterByPeriod(history, allocPeriod);
    const selector = <PeriodSelector value={allocPeriod} onChange={setAllocPeriod} />;
    if (filtered.length < 2) {
      return <Section title={title} actions={selector}><p className="text-[14px] text-ink-body">No data in this period. Choose a longer one.</p></Section>;
    }
    const weightOf = (h: { weights: Record<string, number> }, name: string): number => {
      if (h.weights[name] != null) return h.weights[name];
      for (const [isin, w] of Object.entries(h.weights)) {
        if (name === isin || holdings.some(hold => hold.security_isin === isin && hold.name === name)) return w;
      }
      return 0;
    };
    return (
      <Section title={title} actions={selector} subtitle="Share of each holding in the portfolio over time, in % of market value.">
        <EChartWrapper option={{
          tooltip: axisTooltip(v => fmtPct(v)),
          legend: { type: 'scroll' as const, bottom: 0, textStyle: { fontSize: 12 } },
          xAxis: timeAxis(filtered.map(h => h.date), allocPeriod),
          yAxis: { type: 'value' as const, min: 0, max: 100, axisLabel: { formatter: (v: number) => fmtPct(v, { digits: 0 }) } },
          series: names.map(name => ({
            name, type: 'line' as const, stack: 'total', areaStyle: { opacity: 0.7 }, lineStyle: { width: 1 },
            emphasis: { focus: 'series' as const }, symbol: 'none',
            data: filtered.map(h => weightOf(h, name)),
          })),
          grid: GRID,
        }} height="340px" />
      </Section>
    );
  };

  const renderSavingsPlans = () => {
    const sp = savingsPlans.data;
    if (savingsPlans.error && !sp) return <Section title="Savings Plans"><ErrorState message="Savings plans couldn’t be loaded." details={savingsPlans.error} onRetry={savingsPlans.reload} /></Section>;
    if (savingsPlans.loading && !sp) return <Section title="Savings Plans"><LoadingState lines={3} /></Section>;
    if (!sp || sp.plans.length === 0) {
      return (
        <Section title="Savings Plans">
          <p className="text-[14px] text-ink-body">No savings plans detected. Regular monthly purchases of the same security show up here.</p>
        </Section>
      );
    }
    return (
      <Section
        title="Savings Plans"
        subtitle={<>{sp.plan_count} detected {sp.plan_count === 1 ? 'plan' : 'plans'} · <Money>{fmtEUR(sp.total_monthly)}</Money> per month in total</>}
      >
        <Rows>
          {sp.plans.map(p => (
            <li key={p.isin} className="py-3">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <p className="text-[14px] font-medium text-ink truncate">{p.name}</p>
                  <p className="text-[12px] text-ink-muted">
                    about <Money>{fmtEUR(p.monthly_amount)}</Money> per month · {fmtNum(p.executions)} purchases over {fmtNum(p.months_active)} months
                  </p>
                </div>
                <div className="text-right shrink-0">
                  <p className="text-[14px] font-medium text-ink"><Money>{fmtEUR(p.current_value)}</Money></p>
                  <p className={`text-[12px] ${toneText(p.dca_return_pct)}`}>{fmtPct(p.dca_return_pct, { sign: true })} return</p>
                </div>
              </div>
              {p.lump_sum_value > 0 && (
                <p className="mt-1.5 text-[12px] text-ink-muted">
                  Had the same total been invested at once at the start, it would be worth <Money>{fmtEUR(p.lump_sum_value)}</Money>:{' '}
                  <span className="text-ink-body">
                    investing monthly is {p.dca_advantage_eur >= 0 ? 'ahead' : 'behind'} by <Money>{fmtEUR(Math.abs(p.dca_advantage_eur))}</Money>.
                  </span>
                </p>
              )}
            </li>
          ))}
        </Rows>
      </Section>
    );
  };

  /* ───────────── Holdings ───────────── */

  const loadDetail = (isin: string) => {
    detailRequest.current = isin;
    setSecurityDetail(null);
    setDetailError(null);
    api.getSecurityDetail(isin)
      .then(d => { if (detailRequest.current === isin) setSecurityDetail(d); })
      .catch((e: Error) => { if (detailRequest.current === isin) setDetailError(e.message || 'Request failed'); });
  };
  const toggleHolding = (h: HoldingRow) => {
    const key = holdingKey(h);
    if (expandedKey === key) { setExpandedKey(null); detailRequest.current = null; return; }
    setExpandedKey(key);
    loadDetail(h.security_isin);
  };
  const expandedHolding = holdings.find(h => holdingKey(h) === expandedKey);
  const detailNode = expandedHolding ? (
    <SectionBoundary key={expandedKey}>
      {detailError
        ? <ErrorState message="The details for this security couldn’t be loaded." details={detailError} onRetry={() => loadDetail(expandedHolding.security_isin)} />
        : securityDetail && securityDetail.isin === expandedHolding.security_isin
          ? <SecurityDetailPanel key={securityDetail.isin} detail={securityDetail} />
          : <LoadingState message="Loading security details" lines={4} />}
    </SectionBoundary>
  ) : null;

  const runTimeMachine = async () => {
    if (!tmDate) return;
    setTmLoading(true);
    setTmError(null);
    try {
      const res = await fetch(`/api/portfolio/time-machine?date=${encodeURIComponent(tmDate)}`);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      setTmData(await res.json());
    } catch (e) {
      setTmError(e instanceof Error ? e.message : 'Request failed');
    } finally {
      setTmLoading(false);
    }
  };

  const renderTimeMachine = () => {
    const c = tmData?.change;
    const attribution: { label: string; value: number; show: boolean }[] = c ? [
      { label: 'Contributions', value: c.contributions, show: true },
      { label: 'Market return', value: c.market_return, show: true },
      { label: 'Dividends', value: c.dividends, show: true },
      { label: 'Physical assets', value: c.assets ?? 0, show: Math.abs(c.assets ?? 0) >= 1 },
      { label: 'Purchase costs', value: c.purchase_costs ?? 0, show: Math.abs(c.purchase_costs ?? 0) >= 1 },
      { label: 'Loan interest', value: c.loan_interest ?? 0, show: Math.abs(c.loan_interest ?? 0) >= 1 },
    ] : [];
    return (
      <Section title="Time Machine" subtitle="Reconstruct net worth and holdings on any past date, in EUR.">
        <form className="flex flex-wrap items-end gap-3" onSubmit={e => { e.preventDefault(); runTimeMachine(); }}>
          <Field label="Date" type="date" value={tmDate} max={new Date().toISOString().slice(0, 10)} onChange={e => setTmDate(e.target.value)} className="flex-1 basis-48 max-w-xs" />
          <button type="submit" disabled={!tmDate || tmLoading} className="apple-btn-primary min-h-[44px]">
            {tmLoading ? 'Loading…' : 'Show that day'}
          </button>
        </form>
        {tmError && <div className="mt-4"><ErrorState message="That date couldn’t be reconstructed." details={tmError} onRetry={runTimeMachine} /></div>}

        {tmData && c && (
          <div className="mt-5 flex flex-col gap-5">
            <MetricGrid>
              <MetricCard label={`Net worth · ${fmtDate(tmData.date)}`} value={fmtEUR(tmData.net_worth)} />
              <MetricCard label="Net worth · today" value={fmtEUR(tmData.current.net_worth)} />
              <MetricCard
                label="Change since"
                value={fmtEUR(c.net_worth_change, { sign: true })}
                tone={toneOf(c.net_worth_change)}
                sublabel={fmtPct(c.net_worth_pct, { sign: true })}
              />
            </MetricGrid>

            <div>
              <SubHeading className="mb-2">Where the change came from</SubHeading>
              <dl className="grid grid-cols-[1fr_auto] gap-x-6 gap-y-1.5 text-[14px] max-w-md">
                {attribution.filter(r => r.show).map(r => (
                  <div key={r.label} className="contents">
                    <dt className="text-ink-body">{r.label}</dt>
                    <dd className={`text-right ${toneText(r.value)}`}><Money>{fmtEUR(r.value, { sign: true })}</Money></dd>
                  </div>
                ))}
              </dl>
            </div>

            {tmData.holdings.length > 0 && (
              <div>
                <SubHeading className="mb-3">Holdings on {fmtDateLong(tmData.date)}</SubHeading>
                <RankedBars
                  scale="total"
                  items={tmData.holdings.map(h => ({ label: h.name, value: h.value, display: fmtEUR(h.value), share: fmtPct(h.weight_pct) }))}
                />
              </div>
            )}
          </div>
        )}
      </Section>
    );
  };

  const renderHoldings = () => (
    <>
      {coreGate(() => (
        <>
          {accountFilter}
          {holdings.length > 0 && <SectionBoundary>{kpiRow}</SectionBoundary>}
          <SectionBoundary>
            <Section
              title="Holdings"
              first={holdings.length === 0}
              actions={holdings.length > 0 ? <span className="text-[13px] text-ink-muted">Prices in the trading currency. Value and gain in EUR.</span> : undefined}
            >
              <HoldingsTable holdings={holdings} expandedKey={expandedKey} onToggle={toggleHolding} detail={detailNode} />
            </Section>
          </SectionBoundary>
        </>
      ))}
      <SectionBoundary><UnvestedPanel mode="detail" /></SectionBoundary>
      <SectionBoundary>{renderTimeMachine()}</SectionBoundary>
    </>
  );

  /* ───────────── Goals & Alerts ───────────── */

  const addGoal = () => {
    if (!goalName || !(goalTarget > 0) || !goalDate) return;
    setGoalError(null);
    api.createGoal({
      name: goalName, target_amount: goalTarget, target_date: goalDate,
      monthly_contribution: goalContrib || 0, assumed_return_pct: goalReturn || 7,
      funding_account_id: goalFunding || undefined, priority: goalPriority,
    })
      .then(() => { setGoalName(''); setGoalTarget(0); setGoalDate(''); setGoalContrib(0); setGoalFunding(''); setGoalPriority(50); goals.reload(); })
      .catch((e: Error) => setGoalError(e.message || 'Request failed'));
  };
  const deleteGoal = async (g: GoalProgress) => {
    if (!(await confirm({ title: `Delete the goal “${g.name}”?`, body: 'The goal and its progress tracking are removed. Your accounts and transactions are not affected.', confirmLabel: 'Delete goal' }))) return;
    api.deleteGoal(g.id).then(() => goals.reload()).catch((e: Error) => setGoalError(e.message || 'Request failed'));
  };
  const addAlert = () => {
    if (!(alertThreshold > 0)) return;
    setAlertError(null);
    api.createAlert({
      alert_type: alertType,
      security_isin: alertType !== 'portfolio_milestone' ? alertISIN : undefined,
      threshold: alertThreshold,
    })
      .then(() => { setAlertThreshold(0); alerts.reload(); })
      .catch((e: Error) => setAlertError(e.message || 'Request failed'));
  };
  const deleteAlert = async (a: PriceAlertEntry, name: string) => {
    if (!(await confirm({ title: `Delete the alert for ${name}?`, body: 'You will no longer be notified when this level is reached.', confirmLabel: 'Delete alert' }))) return;
    api.deleteAlert(a.id).then(() => alerts.reload()).catch((e: Error) => setAlertError(e.message || 'Request failed'));
  };

  const goalStatusTone = (s: GoalProgress['status']) => (s === 'behind' ? 'danger' as const : s === 'at_risk' ? 'warning' as const : s === 'on_track' ? 'info' as const : 'success' as const);
  const alertName = (a: PriceAlertEntry) => (a.alert_type === 'portfolio_milestone' ? 'Net worth' : (a.security_name || a.security_isin || 'Unknown security'));
  const alertCondition = (a: PriceAlertEntry): ReactNode => {
    switch (a.alert_type) {
      case 'price_above': return <>Price at or above <Money>{fmtEUR(a.threshold, { cents: true })}</Money></>;
      case 'price_below': return <>Price at or below <Money>{fmtEUR(a.threshold, { cents: true })}</Money></>;
      case 'portfolio_milestone': return <>Crosses <Money>{fmtEUR(a.threshold)}</Money></>;
      case 'daily_change': return <>Moves by {fmtPct(a.threshold)} or more in a day</>;
      default: return <>{labelFor(a.alert_type)} <Money>{fmtNum(a.threshold, 2)}</Money></>;
    }
  };

  const renderGoals = () => {
    const list = goals.data || [];
    return (
      <Section title="Financial Goals" first subtitle="A target amount and date. Progress counts the balance of the goal’s funding account, or else a share of liquid wealth (cash and securities outside pension accounts) handed out in priority order, so no euro counts twice.">
        {goals.loading && !goals.data ? <LoadingState lines={3} />
          : goals.error && !goals.data ? <ErrorState message="Goals couldn’t be loaded." details={goals.error} onRetry={goals.reload} />
          : list.length === 0 ? <EmptyState title="No goals yet" message="Add a target amount and date below to track your progress towards it." />
          : (
            <Rows>
              {list.map(g => (
                <li key={g.id} className="py-3">
                  <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
                    <div className="min-w-0 flex-1 basis-56">
                      <p className="text-[15px] font-medium text-ink flex flex-wrap items-center gap-2">
                        {g.name}
                        <Chip tone={goalStatusTone(g.status)}>{labelFor(g.status)}</Chip>
                      </p>
                      <p className="text-[13px] text-ink-body mt-0.5">
                        <Money>{fmtEUR(g.target_amount)}</Money> by {fmtDateLong(g.target_date)}
                        {g.monthly_contribution > 0 && <> · <Money>{fmtEUR(g.monthly_contribution)}</Money> per month</>}
                        {' · from '}{g.funding_source === 'account'
                          ? ((goalAccounts.data || []).find(a => a.id === g.funding_account_id)?.name ?? 'its funding account')
                          : 'liquid wealth'}
                      </p>
                    </div>
                    <button type="button" className="apple-btn-danger apple-btn-sm min-h-[44px] md:min-h-[36px]" onClick={() => deleteGoal(g)} aria-label={`Delete goal ${g.name}…`}>Delete…</button>
                  </div>
                  <div className="mt-2 flex items-center gap-3" data-privacy="blur">
                    <ProgressBar value={g.progress_pct} label={`Progress towards ${g.name}`} className="flex-1" />
                    <span className="text-[13px] text-ink shrink-0">{fmtPct(g.progress_pct, { digits: 0 })} · {fmtEUR(g.current_value)}</span>
                  </div>
                </li>
              ))}
            </Rows>
          )}

        <form className="mt-6" onSubmit={e => { e.preventDefault(); addGoal(); }}>
          <SubHeading className="mb-3">Add a goal</SubHeading>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <Field label="Goal name" value={goalName} onChange={e => setGoalName(e.target.value)} placeholder="e.g. Retirement" />
            <NumberField label="Target amount" unit="€" value={goalTarget} onValueChange={v => setGoalTarget(Math.max(0, v))} />
            <Field label="Target date" type="date" value={goalDate} onChange={e => setGoalDate(e.target.value)} />
            <NumberField label="Monthly contribution" unit="€ / month" value={goalContrib} onValueChange={v => setGoalContrib(Math.max(0, v))} hint="Optional." />
            <NumberField label="Assumed return" unit="% / year" value={goalReturn} onValueChange={setGoalReturn} hint="Used to project whether you are on track." />
            <SelectField label="Funded from" value={goalFunding} onChange={e => setGoalFunding(e.target.value)} hint="Progress counts this account’s balance.">
              <option value="">Liquid wealth (no single account)</option>
              {(goalAccounts.data || []).map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
            </SelectField>
            <SelectField label="Priority" value={String(goalPriority)} onChange={e => setGoalPriority(parseInt(e.target.value, 10) || 50)} hint="Higher priorities are funded first.">
              <option value="10">Emergency</option>
              <option value="20">Employer match</option>
              <option value="30">Debt payoff</option>
              <option value="40">Time-sensitive</option>
              <option value="50">Long-term</option>
            </SelectField>
          </div>
          {goalError && <div className="mt-4"><ErrorState message="That didn’t work. Nothing was changed." details={goalError} /></div>}
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <button type="submit" className="apple-btn-primary min-h-[44px]" disabled={!goalName || !(goalTarget > 0) || !goalDate}>Add goal</button>
            {(!goalName || !(goalTarget > 0) || !goalDate) && <span className="text-[12px] text-ink-muted">Needs a name, a target amount and a date.</span>}
          </div>
        </form>
      </Section>
    );
  };

  const renderAlerts = () => {
    const list = (alerts.data || []).filter(a => PRICE_ALERT_TYPES.has(a.alert_type));
    const triggered = (notifications.data || []).filter(n => PRICE_ALERT_TYPES.has(n.alert_type)).slice(0, 10);
    const needsSecurity = alertType !== 'portfolio_milestone';
    const canAdd = alertThreshold > 0 && (!needsSecurity || !!alertISIN);
    return (
      <Section title="Price Alerts" subtitle="Be notified when a security reaches a price, or your net worth crosses a level. System digests and reminders are under the bell.">
        {alerts.loading && !alerts.data ? <LoadingState lines={3} />
          : alerts.error && !alerts.data ? <ErrorState message="Alerts couldn’t be loaded." details={alerts.error} onRetry={alerts.reload} />
          : list.length === 0 ? <EmptyState title="No price alerts yet" message="Set a price level or a net-worth milestone below and you’ll be notified when it is reached." />
          : (
            <Rows>
              {list.map(a => (
                <li key={a.id} className="py-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
                  <div className="min-w-0 flex-1 basis-56">
                    <p className="text-[15px] font-medium text-ink flex flex-wrap items-center gap-2">
                      {alertName(a)}
                      <Chip tone={a.is_active ? 'success' : 'neutral'}>{a.is_active ? 'Active' : 'Paused'}</Chip>
                    </p>
                    <p className="text-[13px] text-ink-body mt-0.5">{alertCondition(a)}</p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <button
                      type="button"
                      className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]"
                      aria-label={`${a.is_active ? 'Pause' : 'Resume'} alert for ${alertName(a)}`}
                      onClick={() => api.toggleAlert(a.id).then(() => alerts.reload()).catch((e: Error) => setAlertError(e.message || 'Request failed'))}
                    >
                      {a.is_active ? 'Pause' : 'Resume'}
                    </button>
                    <button type="button" className="apple-btn-danger apple-btn-sm min-h-[44px] md:min-h-[36px]" aria-label={`Delete alert for ${alertName(a)}…`} onClick={() => deleteAlert(a, alertName(a))}>Delete…</button>
                  </div>
                </li>
              ))}
            </Rows>
          )}

        <form className="mt-6" onSubmit={e => { e.preventDefault(); if (canAdd) addAlert(); }}>
          <SubHeading className="mb-3">Add an alert</SubHeading>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            <SelectField label="Alert when" value={alertType} onChange={e => setAlertType(e.target.value)}>
              <option value="price_above">Price rises to or above</option>
              <option value="price_below">Price falls to or below</option>
              <option value="portfolio_milestone">Net worth crosses</option>
            </SelectField>
            {needsSecurity && (
              <SelectField label="Security" value={alertISIN} onChange={e => setAlertISIN(e.target.value)} error={securities.error && !securities.data ? 'Securities couldn’t be loaded.' : undefined}>
                <option value="">Select a security…</option>
                {(securities.data || []).map(s => <option key={s.isin} value={s.isin}>{s.name}</option>)}
              </SelectField>
            )}
            <NumberField label={needsSecurity ? 'Price level' : 'Net worth level'} unit="€" value={alertThreshold} onValueChange={v => setAlertThreshold(Math.max(0, v))} />
          </div>
          {alertError && <div className="mt-4"><ErrorState message="That didn’t work. Nothing was changed." details={alertError} /></div>}
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <button type="submit" className="apple-btn-primary min-h-[44px]" disabled={!canAdd}>Add alert</button>
            {!canAdd && <span className="text-[12px] text-ink-muted">{needsSecurity ? 'Needs a security and a price level.' : 'Needs a net worth level.'}</span>}
          </div>
        </form>

        {triggered.length > 0 && (
          <div className="mt-6">
            <SubHeading className="mb-2">Recently triggered</SubHeading>
            <Rows>
              {triggered.map(n => (
                <li key={n.id} className="py-2.5 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
                  <p className="text-[14px] text-ink min-w-0 flex-1 basis-56" data-privacy={n.alert_type === 'portfolio_milestone' ? 'blur' : undefined}>{n.message}</p>
                  <p className="text-[12px] text-ink-muted shrink-0 flex items-center gap-2">
                    {!n.is_read && <Chip tone="info">New</Chip>}
                    {fmtDate(n.triggered_at)}
                  </p>
                </li>
              ))}
            </Rows>
          </div>
        )}
      </Section>
    );
  };

  /* ───────────── Shell ───────────── */

  const staleDays = priceAsOf ? Math.floor((Date.now() - new Date(priceAsOf).getTime()) / 86_400_000) : 0;
  const meta = priceAsOf ? (
    <span className="inline-flex flex-wrap items-center gap-2">
      Prices as of {fmtDateLong(priceAsOf)}
      {staleDays > 3 && <Chip tone="warning">Stale · {staleDays} days old</Chip>}
    </span>
  ) : undefined;

  let panel: ReactNode;
  switch (activeTab) {
    case 'dividends': panel = renderDividends(); break;
    case 'allocation':
      panel = (
        <>
          <SectionBoundary>{renderTargets()}</SectionBoundary>
          <SectionBoundary>{renderRebalancing()}</SectionBoundary>
          <SectionBoundary>{renderAllocHistory()}</SectionBoundary>
          <SectionBoundary>{renderSavingsPlans()}</SectionBoundary>
        </>
      );
      break;
    case 'holdings': panel = renderHoldings(); break;
    case 'goals':
      panel = (
        <>
          <SectionBoundary>{renderGoals()}</SectionBoundary>
          <SectionBoundary>{renderAlerts()}</SectionBoundary>
        </>
      );
      break;
    default: panel = renderOverview();
  }

  return (
    <div>
      <PageHeader title="Portfolio" meta={meta} />
      <div className="mt-2 md:mt-4">
        <TabBar tabs={PORTFOLIO_TABS} activeTab={activeTab} onTabChange={setActiveTab} ariaLabel="Portfolio views" />
      </div>
      <TabPanel id={activeTab}>
        <SectionBoundary key={activeTab}>{panel}</SectionBoundary>
      </TabPanel>
      {confirmDialog}
    </div>
  );
}
