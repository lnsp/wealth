import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import type { ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { api, type Account, type HoldingRow, type NetWorthSnapshot, type GoalProgress, type ProjectionData, type DailyAttribution } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import AccountCard, { institutionLabel, depositProtection, DEPOSIT_INSURANCE_LIMIT, DEPOSIT_INSURANCE_NEAR } from '../components/AccountCard';
import {
  PageHeader, TabBar, TabPanel, useTabParam, Section, SubHeading, MetricCard, MetricGrid,
  PeriodSelector, periodCutoff, filterByPeriod, formatDateForPeriod, LoadingState, ErrorState, EmptyState,
  SectionBoundary, useConfirm, Chip, RankedBars, ProgressBar,
} from '../components/ui';
import type { Period } from '../components/ui';
import { periodWindow, dateKeyOf, localDateKey, keyToLocalDate } from '../components/ui/PeriodSelector';
import { useThemeColors } from '../hooks/useThemeColors';
import { useMarketRefresh } from '../hooks/useMarketRefresh';
import UnvestedPanel from '../components/UnvestedPanel';
import AssetsTab, { ASSET_VIEW_PARAMS } from '../components/assets/AssetsTab';
import { escapeHtml, fmtEUR, fmtPct, fmtNum, fmtAxis, fmtDateLong, fmtMonth, fmtMonthShort } from '../utils/format';

type NWTab = 'overview' | 'projection' | 'accounts' | 'assets';
const NW_TAB_IDS: readonly NWTab[] = ['overview', 'projection', 'accounts', 'assets'];
const NW_TABS: { id: NWTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'projection', label: 'Projection' },
  { id: 'accounts', label: 'Accounts' },
  { id: 'assets', label: 'Assets' },
];

const PERIODS: Period[] = ['1M', '3M', '6M', 'YTD', '1Y', 'All'];
const PERIOD_PHRASE: Record<Period, string> = {
  '1M': 'over the past month',
  '3M': 'over the past 3 months',
  '6M': 'over the past 6 months',
  'YTD': 'since 1 January',
  '1Y': 'over the past year',
  '3Y': 'over the past 3 years',
  'All': 'all time',
};

/** Where the full list of alerts lives (Portfolio → Goals & Alerts). */
const ALERTS_HREF = '/portfolio?tab=goals';

interface NextAction { title: string; detail: string; impact_eur: number; impact_kind?: 'saving' | 'cost' | ''; urgency: string; category: string; link: string }
interface WaterfallBar { label: string; value: number; type: string }
interface AttributionMonth { month: string; contributions: number; market_return: number; dividends: number; interest: number; assets?: number; property_costs?: number; fees?: number; taxes?: number; total: number }
interface WaterfallData {
  waterfall: WaterfallBar[];
  time_series: AttributionMonth[];
  crossover_month: string;
  net_contributions: number;
  market_returns: number;
  dividends: number;
  current_nw: number;
}
type LoadStatus = 'loading' | 'ready' | 'error';

const toneCls = (n: number) => (n > 0 ? 'text-sage' : n < 0 ? 'text-claret' : 'text-ink');
const monthDate = (m: string) => (m.length === 7 ? `${m}-01` : m);

/**
 * Liquid wealth: cash and securities in every account except pension
 * accounts and the legacy property / liability account types. The same rule
 * as the backend's isLiquidAccountType, which funds goals without an account.
 */
const isLiquidAccountType = (type: string) => !['pension', 'real_estate', 'liability'].includes(type);

/** Holdings asset classes counted as equities (allocation, glide path). */
const EQUITY_CLASSES = new Set(['etf', 'stock', 'fund']);

/** Mon–Fri for a "YYYY-MM-DD" day, independent of the browser's time zone. */
const isWeekdayKey = (date: string) => {
  const day = new Date(`${dateKeyOf(date)}T00:00:00Z`).getUTCDay();
  return day !== 0 && day !== 6;
};

/** Drops the all-zero snapshots older than the first real one (newest-first input). */
function trimLeadingZeroSnapshots(newestFirst: NetWorthSnapshot[]): NetWorthSnapshot[] {
  const isZero = (s: NetWorthSnapshot) => !s.total && !s.cash_component && !s.investment_component && !s.physical_component && !s.debt_component;
  let end = newestFirst.length;
  while (end > 1 && isZero(newestFirst[end - 1])) end--;
  return end === newestFirst.length ? newestFirst : newestFirst.slice(0, end);
}

/**
 * Extra monthly saving that closes a shortfall by the target date when each
 * payment earns the goal's return: shortfall · r / ((1 + r)^n − 1), with r
 * the monthly rate. 10,000 € over 48 months at 5 % a year → 189.05 € (not
 * 10,000 / 48 = 208.33 €).
 */
function monthlyTopUp(shortfall: number, months: number, annualReturnPct: number): number {
  if (shortfall <= 0 || months <= 0) return 0;
  const r = Math.pow(1 + annualReturnPct / 100, 1 / 12) - 1;
  if (Math.abs(r) < 1e-9) return shortfall / months;
  return (shortfall * r) / (Math.pow(1 + r, months) - 1);
}
/** "1 year 7 months", "7 months", "2 years". */
function fmtDuration(months: number): string {
  const y = Math.floor(months / 12);
  const m = months % 12;
  const parts: string[] = [];
  if (y > 0) parts.push(`${y} ${y === 1 ? 'year' : 'years'}`);
  if (m > 0 || y === 0) parts.push(`${m} ${m === 1 ? 'month' : 'months'}`);
  return parts.join(' ');
}

/** Phones get horizontal bars and fewer labels; re-evaluates on resize/rotation. */
function useIsNarrow(): boolean {
  const query = '(max-width: 767px)';
  const [narrow, setNarrow] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches);
  useEffect(() => {
    const mq = window.matchMedia(query);
    const on = () => setNarrow(mq.matches);
    mq.addEventListener('change', on);
    return () => mq.removeEventListener('change', on);
  }, []);
  return narrow;
}

interface LegendItem { label: string; color: string; style?: 'solid' | 'dashed' | 'dotted' | 'band' | 'box' | 'dot' }

/** Legend whose swatches repeat the series' colour and line style. */
function ChartLegend({ items, className = '' }: { items: LegendItem[]; className?: string }) {
  return (
    <ul className={`flex flex-wrap items-center gap-x-5 gap-y-1.5 text-[12px] text-ink-body ${className}`}>
      {items.map(it => (
        <li key={it.label} className="inline-flex items-center gap-2">
          {it.style === 'box' || it.style === 'band' ? (
            <span aria-hidden="true" className="w-3 h-3 rounded-[2px]" style={{ background: it.color, opacity: it.style === 'band' ? 0.3 : 1 }} />
          ) : it.style === 'dot' ? (
            <span aria-hidden="true" className="w-2.5 h-2.5 rounded-full" style={{ background: it.color }} />
          ) : (
            <svg aria-hidden="true" width="26" height="8" viewBox="0 0 26 8">
              <line x1="1" y1="4" x2="25" y2="4" stroke={it.color} strokeWidth="2"
                strokeDasharray={it.style === 'dashed' ? '6 4' : it.style === 'dotted' ? '2 3' : undefined} />
            </svg>
          )}
          {it.label}
        </li>
      ))}
    </ul>
  );
}

/* ───────────────────────────── Attribution maths ─────────────────────────────
 * The backend reports cumulative monthly figures. A period is the difference
 * between the latest month and the month end closest to the period start, so
 * no figure is recomputed here — only subtracted.
 */

interface Cause { label: string; value: number }
interface PeriodAttribution {
  causes: Cause[];
  /** Last bar: net change over the period, or current net worth when counting from zero. */
  net: number;
  fromZero: boolean;
  startNW: number;
  endNW: number;
  startMonth: string | null;
  endMonth: string;
  firstMonth: string;
  market: number;
  /** Monthly points from the baseline month to the latest month. */
  points: AttributionMonth[];
  startContrib: number;
}

// "Market and FX": the market figure is the residual after every named cause,
// so it also carries currency moves on foreign cash. Fees and taxes are
// separate both for the whole history and for a period.
const WATERFALL_LABELS: Record<string, string> = {
  'Market Returns': 'Market and FX',
  'Physical Assets': 'Property and vehicles',
  'Purchase Costs': 'Purchase costs',
  'Loan Interest': 'Loan interest',
};

function computeAttribution(wf: WaterfallData | null, period: Period): PeriodAttribution | null {
  const ts = wf?.time_series ?? [];
  if (!wf || ts.length === 0) return null;
  const end = ts[ts.length - 1];
  let baseIdx = -1;
  const cutoff = periodCutoff(period);
  if (cutoff) {
    // Month end closest to the cutoff: shift by half a month, then step back one month.
    const shifted = new Date(cutoff);
    shifted.setDate(shifted.getDate() + 15);
    const b = new Date(shifted.getFullYear(), shifted.getMonth() - 1, 1);
    const key = `${b.getFullYear()}-${String(b.getMonth() + 1).padStart(2, '0')}`;
    for (let i = 0; i < ts.length; i++) if (ts[i].month <= key) baseIdx = i;
    if (baseIdx >= ts.length - 1) baseIdx = ts.length - 2;
  }
  if (baseIdx < 0) {
    // Whole history: the backend's own waterfall, which also separates fees and taxes.
    const causes = (wf.waterfall ?? [])
      .filter(w => w.type !== 'total' && Math.abs(w.value) >= 1)
      .map(w => ({ label: WATERFALL_LABELS[w.label] ?? w.label, value: w.value }));
    return {
      causes, net: wf.current_nw, fromZero: true, startNW: 0, endNW: wf.current_nw,
      startMonth: null, endMonth: end.month, firstMonth: ts[0].month, market: wf.market_returns,
      points: ts, startContrib: 0,
    };
  }
  const base = ts[baseIdx];
  const d = (pick: (m: AttributionMonth) => number) => pick(end) - pick(base);
  const market = d(m => m.market_return);
  const causes: Cause[] = [
    { label: 'Contributions', value: d(m => m.contributions) },
    { label: 'Market and FX', value: market },
    { label: 'Dividends', value: d(m => m.dividends) },
    { label: 'Interest', value: d(m => m.interest) },
    { label: 'Fees and taxes', value: d(m => (m.fees ?? 0) + (m.taxes ?? 0)) },
    { label: 'Property and vehicles', value: d(m => m.assets ?? 0) },
    { label: 'Purchase costs and loan interest', value: d(m => m.property_costs ?? 0) },
  ].filter(c => Math.abs(c.value) >= 1);
  return {
    causes, net: end.total - base.total, fromZero: false, startNW: base.total, endNW: end.total,
    startMonth: base.month, endMonth: end.month, firstMonth: ts[0].month, market,
    points: ts.slice(baseIdx), startContrib: base.contributions,
  };
}

/* ─────────────────────────────────── Page ─────────────────────────────────── */

export default function NetWorth() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [allSnapshots, setAllSnapshots] = useState<NetWorthSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [period, setPeriod] = useState<Period>('1Y');
  const [goals, setGoals] = useState<GoalProgress[]>([]);
  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [nextActions, setNextActions] = useState<NextAction[]>([]);
  const [actionsStatus, setActionsStatus] = useState<LoadStatus>('loading');
  const [unreadAlerts, setUnreadAlerts] = useState(0);
  const [jobErrors, setJobErrors] = useState(0);
  // /api/portfolio/attribution: the latest snapshot change split into market,
  // flows, income and the rest, plus the sentence naming the top movers.
  const [attribution, setAttribution] = useState<DailyAttribution | null>(null);
  const [showAttribution, setShowAttribution] = useState(false);
  const [wfData, setWfData] = useState<WaterfallData | null>(null);
  const [wfStatus, setWfStatus] = useState<LoadStatus>('loading');

  // Tab state lives in the URL. Deep links into the Assets tab (?asset=…, ?loan=…) open it directly.
  const [searchParams, setSearchParams] = useSearchParams();
  const [tabParam] = useTabParam<NWTab>(NW_TAB_IDS, 'overview');
  const activeTab: NWTab = searchParams.has('tab')
    ? tabParam
    : ASSET_VIEW_PARAMS.some(k => searchParams.has(k)) ? 'assets' : 'overview';
  const setActiveTab = useCallback((tab: NWTab) => {
    setSearchParams(prev => {
      const next = new URLSearchParams(prev);
      ASSET_VIEW_PARAMS.forEach(k => next.delete(k));
      if (tab === 'overview') next.delete('tab'); else next.set('tab', tab);
      return next;
    }, { replace: true });
  }, [setSearchParams]);

  const loadData = useCallback(async () => {
    try {
      setLoadError(null);
      const [accRes, nwRes] = await Promise.all([
        api.listAccounts(),
        api.getNetWorth(9999),
      ]);
      setAccounts(accRes.accounts || []);
      setAllSnapshots(nwRes.snapshots || []);
      // Holdings drive the allocation and concentration sections; a failure is
      // non-fatal — those sections say so instead of disappearing.
      api.listHoldings().then(r => setHoldings(r.holdings || [])).catch(() => {});
    } catch (e) {
      console.error('Failed to load data:', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  }, []);

  const loadWaterfall = useCallback(() => {
    setWfStatus('loading');
    fetch('/api/portfolio/wealth-waterfall')
      .then(r => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then((d: WaterfallData) => { setWfData(d); setWfStatus('ready'); })
      .catch(() => setWfStatus('error'));
  }, []);

  const loadActions = useCallback(() => {
    setActionsStatus('loading');
    // Tax settings chosen on the Tax page (church tax, joint filing) size the tax actions.
    const qs = new URLSearchParams();
    try {
      const church = parseFloat(localStorage.getItem('church_tax_rate') || '0');
      if (church > 0) qs.set('church_tax', String(church));
      if (localStorage.getItem('fsa_joint') === '1') qs.set('joint', '1');
    } catch { /* storage unavailable: defaults */ }
    fetch(`/api/portfolio/next-actions${qs.toString() ? `?${qs}` : ''}`)
      .then(r => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then(d => { setNextActions(d.actions || []); setActionsStatus('ready'); })
      .catch(() => setActionsStatus('error'));
  }, []);

  // Track previous net worth for the tick animation
  const prevNetWorth = useRef<number | null>(null);
  const [tick, setTick] = useState<'up' | 'down' | null>(null);

  useEffect(() => { loadData(); }, [loadData]);

  // Auto-refresh during market hours (every 60s)
  useMarketRefresh(loadData, 60_000);

  // Tick animation: flash sage/claret when net worth changes
  useEffect(() => {
    if (allSnapshots.length === 0) return;
    const current = allSnapshots[0]?.total ?? 0;
    if (prevNetWorth.current !== null && current !== prevNetWorth.current) {
      setTick(current > prevNetWorth.current ? 'up' : 'down');
      prevNetWorth.current = current;
      const timer = setTimeout(() => setTick(null), 1200);
      return () => clearTimeout(timer);
    }
    prevNetWorth.current = current;
  }, [allSnapshots]);

  useEffect(() => {
    api.getGoalsProgress().then(r => setGoals(r.goals || [])).catch(() => {});
    api.listNotifications().then(r => setUnreadAlerts(r.unread_count)).catch(() => {});
    api.getAttribution().then(setAttribution).catch(() => {});
    fetch('/api/settings/scheduler-status')
      .then(r => (r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`))))
      .then(d => setJobErrors((d.jobs || []).filter((j: { status: string }) => j.status === 'error').length))
      .catch(() => {});
    loadWaterfall();
    loadActions();
  }, [loadWaterfall, loadActions]);

  const totalNetWorth = allSnapshots.length > 0 ? allSnapshots[0].total : 0;
  // Charts and period changes start at the first real snapshot, not at
  // all-zero rows from before anything was tracked.
  const seriesSnapshots = useMemo(() => trimLeadingZeroSnapshots(allSnapshots), [allSnapshots]);

  // The hero follows the Overview period. Tabs without a period control show a fixed year.
  // The change is measured from the last snapshot on or before the period's
  // base day (31 Dec for YTD), compared as calendar days.
  const heroPeriod: Period = activeTab === 'overview' ? period : '1Y';
  const { points: periodSnapshots, base: periodBase, truncated: periodTruncated } = useMemo(
    () => periodWindow(seriesSnapshots, heroPeriod), [seriesSnapshots, heroPeriod]);
  const periodStart = periodSnapshots.length > 1 && periodBase ? periodBase.total : totalNetWorth;
  const change = totalNetWorth - periodStart;
  const changePct = periodStart !== 0 ? (change / periodStart) * 100 : 0;
  const periodPhrase = periodTruncated && periodBase
    ? `since ${fmtDateLong(keyToLocalDate(dateKeyOf(periodBase.date)))}`
    : PERIOD_PHRASE[period];

  // Daily change: against the most recent snapshot from a previous day
  const { dailyChange, dailyPrev, dailyChangePct } = useMemo(() => {
    if (allSnapshots.length < 2) return { dailyChange: 0, dailyPrev: totalNetWorth, dailyChangePct: 0 };
    const todayStr = allSnapshots[0].date.slice(0, 10);
    const prev = allSnapshots.find(s => s.date.slice(0, 10) !== todayStr);
    if (!prev) return { dailyChange: 0, dailyPrev: totalNetWorth, dailyChangePct: 0 };
    const dc = totalNetWorth - prev.total;
    const dp = prev.total;
    return { dailyChange: dc, dailyPrev: dp, dailyChangePct: dp !== 0 ? (dc / dp) * 100 : 0 };
  }, [allSnapshots, totalNetWorth]);

  // Sparkline: weekday snapshots of the hero period plus its base, oldest first
  const sparkSnapshots = useMemo(
    () => periodSnapshots.filter((s, i) => i === periodSnapshots.length - 1 || isWeekdayKey(s.date)).reverse(),
    [periodSnapshots]);

  if (loading) {
    return (
      <div>
        <PageHeader title="Net Worth" />
        <LoadingState message="Loading net worth" lines={6} />
      </div>
    );
  }

  // A backend failure is distinct from "no data yet" — don't show the welcome
  // state when the server is just unreachable.
  if (loadError && allSnapshots.length === 0 && accounts.length === 0) {
    return (
      <div>
        <PageHeader title="Net Worth" />
        <div className="mt-6">
          <ErrorState message="Your data couldn’t be loaded." details={loadError} onRetry={loadData} />
        </div>
      </div>
    );
  }

  // Empty state: nothing imported yet (or accounts exist but no snapshots)
  if (allSnapshots.length === 0) {
    const hasAccounts = accounts.length > 0;
    return (
      <div>
        <PageHeader title="Net Worth" />
        <div className="mt-6">
          <EmptyState
            title={hasAccounts ? 'Import your data' : 'Welcome to Wealth'}
            message={hasAccounts
              ? 'Your accounts are set up. Import a CSV file in Settings to start tracking your net worth.'
              : 'Get started by creating an account and importing your first CSV file in Settings.'}
            action={{ label: 'Go to Settings', href: '/settings' }}
          />
        </div>
      </div>
    );
  }

  const latestKey = dateKeyOf(allSnapshots[0].date);
  const snapshotDate = keyToLocalDate(latestKey);
  const todayMidnight = keyToLocalDate(localDateKey(new Date()));
  const daysOld = Math.max(0, Math.round((todayMidnight.getTime() - snapshotDate.getTime()) / (1000 * 60 * 60 * 24)));
  // The daily change is the latest snapshot against the one before; call it
  // "today" only when the latest snapshot is today's.
  const dayLabel = latestKey === localDateKey(new Date()) ? 'today' : `on ${fmtDateLong(snapshotDate)}`;
  // The backend splits the same snapshot change; every part is a real
  // measurement except "other", which is what remains.
  const dayParts = attribution && attribution.to_date === latestKey
    ? [
      { label: 'Market', value: attribution.market_change ?? attribution.total_change },
      { label: 'Deposits and withdrawals', value: attribution.flows ?? 0 },
      { label: 'Dividends and interest, after fees and taxes', value: attribution.income ?? 0 },
      { label: 'Other (FX, property, loans)', value: attribution.other ?? 0 },
    ].filter(p => Math.abs(p.value) >= 1)
    : [];
  const hasExplanation = !!attribution && (!!attribution.summary || dayParts.length > 0);

  return (
    <div>
      <PageHeader
        title="Net Worth"
        meta={
          <span className="inline-flex flex-wrap items-center justify-end gap-2">
            As of {fmtDateLong(snapshotDate)}
            {daysOld >= 2 && (
              <Chip tone={daysOld < 10 ? 'warning' : 'danger'}>{daysOld < 10 ? `${daysOld} days old` : `Stale, ${daysOld} days old`}</Chip>
            )}
          </span>
        }
      />

      {/* Hero: the total, how it changed, and a small labelled trend */}
      <div className="mt-3 md:mt-4 flex flex-wrap items-end justify-between gap-x-10 gap-y-5">
        <div className="min-w-0">
          <p className="label-caps">Total Net Worth</p>
          <p
            className={`font-serif text-[36px] md:text-[48px] font-normal tracking-tight leading-none mt-2 transition-colors duration-[250ms] ease-out ${tick === 'up' ? 'text-sage' : tick === 'down' ? 'text-claret' : 'text-ink'}`}
            style={{ fontVariantNumeric: 'lining-nums tabular-nums' }}
            data-privacy="blur"
          >
            {fmtEUR(totalNetWorth)}
          </p>

          {/* Period change — only where a period control is on screen */}
          {activeTab === 'overview' && periodSnapshots.length > 1 && (
            <p className={`text-[14px] mt-2.5 ${toneCls(change)}`}>
              {change !== 0 && <span aria-hidden="true" className="mr-1">{change > 0 ? '▲' : '▼'}</span>}
              <span data-privacy="blur">{fmtEUR(change, { sign: true })}</span>
              {periodStart >= 100 && <> ({fmtPct(changePct, { sign: true })})</>}
              {' '}<span>{periodPhrase}</span>
            </p>
          )}

          {/* Today's change, with the optional explanation */}
          {allSnapshots.length > 1 && (
            <p className={`flex flex-wrap items-center gap-x-1.5 text-[13px] text-ink-muted ${activeTab === 'overview' ? 'mt-0.5' : 'mt-2.5'}`}>
              <span className={toneCls(dailyChange)}>
                {dailyChange !== 0 && <span aria-hidden="true" className="mr-1">{dailyChange > 0 ? '▲' : '▼'}</span>}
                <span data-privacy="blur">{fmtEUR(dailyChange, { sign: true })}</span>
                {dailyPrev >= 100 && <> ({fmtPct(dailyChangePct, { sign: true, digits: 2 })})</>}
              </span>
              <span>{dayLabel}</span>
              {hasExplanation && (
                <button
                  type="button"
                  onClick={() => setShowAttribution(prev => !prev)}
                  aria-expanded={showAttribution}
                  className="inline-flex items-center min-h-[44px] md:min-h-[32px] px-1.5 text-[13px] font-medium text-forest hover:underline"
                >
                  {showAttribution ? 'Hide' : 'Why?'}
                </button>
              )}
            </p>
          )}
          {showAttribution && attribution && hasExplanation && (
            <div className="mt-1 max-w-xl space-y-0.5 text-[13px] text-ink-muted">
              {attribution.summary && <p>{attribution.summary}</p>}
              {dayParts.length > 0 && (
                <p>
                  {dayParts.map((p, i) => (
                    <span key={p.label}>
                      {i > 0 && ' · '}
                      {p.label} <span data-privacy="blur">{fmtEUR(p.value, { sign: true })}</span>
                    </span>
                  ))}
                </p>
              )}
            </div>
          )}
        </div>

        {sparkSnapshots.length > 1 && (
          <SectionBoundary>
            <HeroSparkline snapshots={sparkSnapshots} period={heroPeriod} />
          </SectionBoundary>
        )}
      </div>

      <div className="mt-6">
        <TabBar tabs={NW_TABS} activeTab={activeTab} onTabChange={setActiveTab} ariaLabel="Net worth views" />
      </div>

      <TabPanel id={activeTab}>
        <SectionBoundary key={activeTab}>
          {activeTab === 'overview' && (
            <OverviewTab
              period={period}
              onPeriodChange={setPeriod}
              accounts={accounts}
              holdings={holdings}
              goals={goals}
              snapshot={allSnapshots[0]}
              wfData={wfData}
              wfStatus={wfStatus}
              onRetryWaterfall={loadWaterfall}
              nextActions={nextActions}
              actionsStatus={actionsStatus}
              onRetryActions={loadActions}
              unreadAlerts={unreadAlerts}
              jobErrors={jobErrors}
            />
          )}
          {activeTab === 'projection' && <ProjectionTab />}
          {activeTab === 'accounts' && <AccountsTab accounts={accounts} allSnapshots={seriesSnapshots} />}
          {activeTab === 'assets' && <AssetsTab onNetWorthChanged={loadData} snapshotNetWorth={totalNetWorth} />}
        </SectionBoundary>
      </TabPanel>
    </div>
  );
}

/* ─────────────────────────────── Hero sparkline ─────────────────────────────── */

function HeroSparkline({ snapshots, period }: { snapshots: NetWorthSnapshot[]; period: Period }) {
  const tc = useThemeColors();
  const values = snapshots.map(s => s.total);
  const min = Math.min(...values);
  const max = Math.max(...values);
  const flat = max - min < 1;
  // The scale is printed next to the line, so a small move reads as small.
  let minLabel = fmtAxis(min);
  let maxLabel = fmtAxis(max);
  if (minLabel === maxLabel) { minLabel = fmtEUR(min); maxLabel = fmtEUR(max); }
  const first = snapshots[0];
  const last = snapshots[snapshots.length - 1];
  return (
    <div className="w-full md:w-[340px] shrink-0">
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2.5">
        <div className="flex flex-col justify-between py-0.5 text-right text-[11px] leading-4 text-ink-muted" data-privacy="blur">
          <span>{maxLabel}</span>
          {!flat && <span>{minLabel}</span>}
        </div>
        <div role="img" aria-label={`Net worth from ${fmtDateLong(first.date)} to ${fmtDateLong(last.date)}, between ${minLabel} and ${maxLabel}`}>
          <EChartWrapper
            height="64px"
            option={{
              tooltip: {
                trigger: 'axis' as const,
                formatter: (params: unknown) => {
                  const p = params as { dataIndex: number }[];
                  if (!Array.isArray(p) || p.length === 0) return '';
                  const snap = snapshots[p[0].dataIndex];
                  return `${escapeHtml(fmtDateLong(snap.date))}<br/><strong>${fmtEUR(snap.total)}</strong>`;
                },
              },
              grid: { left: 0, right: 6, top: 8, bottom: 8 },
              xAxis: { type: 'category' as const, show: false, boundaryGap: false, data: snapshots.map(s => s.date) },
              yAxis: {
                type: 'value' as const,
                min: flat ? min - 1 : min,
                max: flat ? max + 1 : max,
                interval: flat ? 2 : max - min,
                axisLabel: { show: false },
                splitLine: { lineStyle: { type: 'solid' as const } },
              },
              series: [{
                type: 'line' as const,
                data: values.map((v, i) => (i === values.length - 1 ? { value: v, symbol: 'circle', symbolSize: 6 } : { value: v, symbol: 'none' })),
                showSymbol: true,
                color: tc.forest,
                lineStyle: { width: 1.5 },
              }],
            }}
          />
        </div>
        <span />
        <div className="flex justify-between mt-0.5 text-[11px] leading-4 text-ink-muted">
          <span>{formatDateForPeriod(first.date, period)}</span>
          <span>{formatDateForPeriod(last.date, period)}</span>
        </div>
      </div>
    </div>
  );
}

/* ──────────────────────────────── Overview tab ──────────────────────────────── */

interface OverviewProps {
  period: Period;
  onPeriodChange: (p: Period) => void;
  accounts: Account[];
  holdings: HoldingRow[];
  goals: GoalProgress[];
  snapshot: NetWorthSnapshot;
  wfData: WaterfallData | null;
  wfStatus: LoadStatus;
  onRetryWaterfall: () => void;
  nextActions: NextAction[];
  actionsStatus: LoadStatus;
  onRetryActions: () => void;
  unreadAlerts: number;
  jobErrors: number;
}

function OverviewTab({
  period, onPeriodChange, accounts, holdings, goals, snapshot, wfData, wfStatus, onRetryWaterfall,
  nextActions, actionsStatus, onRetryActions, unreadAlerts, jobErrors,
}: OverviewProps) {
  const totalNetWorth = snapshot.total;
  // Property and vehicles at their modelled value, and the loans on them (both part of the snapshot total)
  const physicalValue = snapshot.physical_component ?? 0;
  const physicalDebt = snapshot.debt_component ?? 0;
  const grossAssets = totalNetWorth + physicalDebt;

  // Liquid wealth (isLiquidAccountType): cash and securities outside pension
  // accounts. Goals without a funding account draw on this same amount. The
  // Projection tab starts from investable wealth instead, which includes pension.
  const liquidAccounts = accounts.filter(a => isLiquidAccountType(a.type));
  const liquidNetWorth = liquidAccounts.reduce((sum, a) => sum + (a.balance ?? 0), 0);
  const illiquidAccounts = accounts.filter(a => !isLiquidAccountType(a.type));
  const illiquidBalance = illiquidAccounts.reduce((sum, a) => sum + (a.balance ?? 0), 0);

  const attr = useMemo(() => computeAttribution(wfData, period), [wfData, period]);

  return (
    <>
      {/* One period control for the whole tab */}
      <div className="mt-4 flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <p className="text-[13px] text-ink-muted">The period applies to the change above and to every chart on this tab.</p>
        <PeriodSelector value={period} onChange={onPeriodChange} periods={PERIODS} />
      </div>

      <MetricGrid className="mt-5">
        <MetricCard label="Liquid wealth" value={fmtEUR(liquidNetWorth)} sublabel="Cash and securities, pension accounts excluded" />
        {physicalValue > 0 && (
          <MetricCard label="Property and vehicles" value={fmtEUR(physicalValue)} sublabel="At current valuation" />
        )}
        {physicalDebt > 0 && (
          <MetricCard label="Loans" value={fmtEUR(-physicalDebt)}
            sublabel={grossAssets > 0 ? `${fmtPct((physicalDebt / grossAssets) * 100)} of gross assets` : 'On property and vehicles'} />
        )}
        {illiquidAccounts.length > 0 && Math.abs(illiquidBalance) >= 1 && (
          <MetricCard label="Illiquid accounts" value={fmtEUR(illiquidBalance)} sublabel="Pension, property and liability accounts" />
        )}
        <MetricCard
          label="Market return"
          value={attr ? fmtEUR(attr.market, { sign: true }) : '—'}
          tone={attr ? (attr.market > 0 ? 'positive' : attr.market < 0 ? 'negative' : 'neutral') : 'neutral'}
          money={!!attr}
          sublabel={attr
            ? `Market and FX, before fees and taxes, ${attr.fromZero ? 'since tracking began' : `since ${fmtMonthShort(monthDate(attr.startMonth as string))}`}`
            : wfStatus === 'loading' ? 'Loading…' : 'Not available'}
        />
      </MetricGrid>

      <SectionBoundary>
        <RecommendedActions
          actions={nextActions}
          status={actionsStatus}
          onRetry={onRetryActions}
          unreadAlerts={unreadAlerts}
          jobErrors={jobErrors}
        />
      </SectionBoundary>

      <SectionBoundary>
        <UnvestedPanel mode="summary" />
      </SectionBoundary>

      <SectionBoundary>
        <AttributionSection attr={attr} status={wfStatus} onRetry={onRetryWaterfall} period={period} />
      </SectionBoundary>

      <SectionBoundary>
        <GrowthSection attr={attr} status={wfStatus} onRetry={onRetryWaterfall} crossoverMonth={wfData?.crossover_month || ''} />
      </SectionBoundary>

      <div className="grid grid-cols-1 lg:grid-cols-2 lg:gap-x-10">
        <SectionBoundary>
          <AllocationSection accounts={accounts} holdings={holdings} physicalValue={physicalValue} />
        </SectionBoundary>
        <SectionBoundary>
          <CounterpartySection accounts={accounts} />
        </SectionBoundary>
      </div>

      <SectionBoundary>
        <ConcentrationSection holdings={holdings} totalNetWorth={totalNetWorth} />
      </SectionBoundary>

      {goals.length > 0 && (
        <SectionBoundary>
          <GoalsSections goals={goals} accounts={accounts} holdings={holdings} />
        </SectionBoundary>
      )}
    </>
  );
}

/* ───────────────────────────── Recommended actions ───────────────────────────── */

function actionLabel(link: string): string {
  if (link.startsWith('/analysis')) return 'Open analysis';
  if (link.startsWith('/portfolio')) return 'Review holdings';
  if (link.startsWith('/tax')) return 'Open tax tools';
  if (link.startsWith('/planning')) return 'Open planning';
  if (link.startsWith('/settings')) return 'Open settings';
  return 'Open';
}

function RecommendedActions({ actions, status, onRetry, unreadAlerts, jobErrors }: {
  actions: NextAction[]; status: LoadStatus; onRetry: () => void; unreadAlerts: number; jobErrors: number;
}) {
  const shown = actions.slice(0, 3);
  return (
    <Section
      title="Recommended Actions"
      actions={(unreadAlerts > 0 || jobErrors > 0) ? (
        <>
          {jobErrors > 0 && (
            <Link to="/settings?tab=data" className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px] border-claret text-claret">
              {jobErrors} failed background job{jobErrors !== 1 ? 's' : ''}
            </Link>
          )}
          {unreadAlerts > 0 && (
            <Link to={ALERTS_HREF} className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px]">
              View all {unreadAlerts} alert{unreadAlerts !== 1 ? 's' : ''}
            </Link>
          )}
        </>
      ) : undefined}
    >
      {status === 'loading' && <LoadingState message="Loading recommendations" lines={2} />}
      {status === 'error' && <ErrorState message="Recommendations couldn’t be loaded." onRetry={onRetry} />}
      {status === 'ready' && shown.length === 0 && (
        <p className="text-[14px] text-ink-body">Nothing needs your attention right now.</p>
      )}
      {status === 'ready' && shown.length > 0 && (
        <ul className="flex flex-col gap-2.5">
          {shown.map((a, i) => {
            const label = actionLabel(a.link);
            const internal = a.link.startsWith('/');
            const btn = 'apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px] shrink-0';
            return (
              <li key={i} className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-apple border border-divider bg-inset px-4 py-3">
                <Chip tone={a.urgency === 'now' ? 'danger' : a.urgency === 'this-month' ? 'warning' : 'info'} className="shrink-0">
                  {a.urgency === 'now' ? 'Act now' : a.urgency === 'this-month' ? 'This month' : 'This quarter'}
                </Chip>
                <div className="flex-1 basis-60 min-w-0">
                  <p className="text-[14px] font-medium text-ink break-words">{a.title}</p>
                  {a.detail && <p className="text-[13px] text-ink-muted break-words">{a.detail}</p>}
                  {a.impact_eur > 0 && a.impact_kind === 'cost' && (
                    <p className="text-[13px] text-amber">Costs about <span data-privacy="blur">{fmtEUR(a.impact_eur)}</span></p>
                  )}
                  {a.impact_eur > 0 && a.impact_kind !== 'cost' && (
                    <p className="text-[13px] text-sage">Worth about <span data-privacy="blur">{fmtEUR(a.impact_eur, { sign: true })}</span></p>
                  )}
                </div>
                {internal
                  ? <Link to={a.link} className={btn} aria-label={`${label}: ${a.title}`}>{label}</Link>
                  : <a href={a.link} className={btn} aria-label={`${label}: ${a.title}`}>{label}</a>}
              </li>
            );
          })}
        </ul>
      )}
      {status === 'ready' && actions.length > 3 && (
        <Link to="/analysis" className="inline-flex items-center min-h-[44px] md:min-h-[32px] mt-2 text-[13px] font-medium text-forest hover:underline">
          View all {actions.length} recommendations
        </Link>
      )}
    </Section>
  );
}

/* ───────────────────────────── Wealth attribution ───────────────────────────── */

function AttributionSection({ attr, status, onRetry, period }: { attr: PeriodAttribution | null; status: LoadStatus; onRetry: () => void; period: Period }) {
  const tc = useThemeColors();
  const narrow = useIsNarrow();

  let subtitle: ReactNode;
  if (attr && attr.fromZero) {
    subtitle = (
      <>
        What built today’s net worth of <span data-privacy="blur">{fmtEUR(attr.endNW)}</span> since tracking began in {fmtMonth(monthDate(attr.firstMonth))}.
        {period !== 'All' && ' The history is shorter than the selected period.'} Each bar is one cause, drawn from zero.
      </>
    );
  } else if (attr) {
    subtitle = (
      <>
        Net worth {attr.net >= 0 ? 'rose' : 'fell'} from <span data-privacy="blur">{fmtEUR(attr.startNW)}</span> to <span data-privacy="blur">{fmtEUR(attr.endNW)}</span> between
        the end of {fmtMonth(monthDate(attr.startMonth as string))} and {fmtMonth(monthDate(attr.endMonth))}. Each bar is one cause, drawn from zero.
        Causes are tracked per month, so the start is the month end closest to the selected period. Market and FX is what remains after the other causes, so it includes currency moves on foreign cash.
      </>
    );
  }

  let body: ReactNode;
  if (status === 'loading') body = <LoadingState message="Loading attribution" />;
  else if (status === 'error') body = <ErrorState message="The attribution couldn’t be loaded." onRetry={onRetry} />;
  else if (!attr || attr.causes.length === 0) {
    body = <p className="text-[14px] text-ink-body">{attr ? 'Net worth did not change in this period.' : 'There is no attribution history yet. It appears after the first monthly snapshot.'}</p>;
  } else {
    const netLabel = attr.fromZero ? 'Net worth' : 'Net change';
    const cats = [...attr.causes.map(c => c.label), '', netLabel];
    const vals: (number | null)[] = [...attr.causes.map(c => c.value), null, attr.net];
    const fmtBar = (v: number, isNet: boolean) => fmtEUR(v, { sign: !(isNet && attr.fromZero) });
    const data = vals.map((v, i) => {
      if (v === null) return { value: '-' as const };
      const isNet = i === vals.length - 1;
      return {
        value: v,
        itemStyle: { color: isNet ? tc.slate : v >= 0 ? tc.forest : tc.claret },
        label: {
          show: true,
          position: narrow ? (v >= 0 ? 'right' as const : 'left' as const) : (v >= 0 ? 'top' as const : 'bottom' as const),
          formatter: () => fmtBar(v, isNet),
          color: tc.ink,
          fontSize: 12,
          fontWeight: isNet ? 600 : 400,
        },
      };
    });
    const nums = vals.filter((v): v is number => v !== null);
    const lo = Math.min(0, ...nums);
    const hi = Math.max(0, ...nums);
    const span = hi - lo || 1;
    const categoryAxis = {
      type: 'category' as const,
      data: cats,
      inverse: narrow,
      axisLabel: { interval: 0, hideOverlap: false, width: narrow ? 104 : 110, overflow: 'break' as const, color: tc.inkBody, fontSize: 12, lineHeight: 15 },
      axisLine: { show: true, lineStyle: { color: tc.divider } },
    };
    const valueAxis = {
      type: 'value' as const,
      // Headroom so value labels are never clipped.
      min: lo < 0 ? lo - span * (narrow ? 0.4 : 0.12) : 0,
      max: hi + span * (narrow ? 0.4 : 0.12),
      axisLabel: { showMinLabel: false, showMaxLabel: false, formatter: (v: number) => (v > 0 ? '+' : '') + fmtAxis(v) },
      splitNumber: narrow ? 3 : 5,
    };
    const sepIndex = attr.causes.length;
    body = (
      <div role="img" aria-label={`${cats.filter(Boolean).map((c, i) => `${c} ${fmtBar(nums[i], i === nums.length - 1)}`).join(', ')}`}>
        <EChartWrapper
          height={narrow ? `${cats.length * 46 + 30}px` : '320px'}
          option={{
            tooltip: {
              trigger: 'axis' as const,
              axisPointer: { type: 'shadow' as const },
              formatter: (params: unknown) => {
                const ps = params as { name: string; dataIndex: number }[];
                if (!Array.isArray(ps) || ps.length === 0) return '';
                const v = vals[ps[0].dataIndex];
                if (v === null || v === undefined) return '';
                return `${escapeHtml(ps[0].name)}<br/><strong>${fmtBar(v, ps[0].dataIndex === vals.length - 1)}</strong>`;
              },
            },
            grid: { top: narrow ? 4 : 20, right: 8, bottom: 4, left: 4, containLabel: true },
            xAxis: narrow ? valueAxis : categoryAxis,
            yAxis: narrow ? categoryAxis : valueAxis,
            series: [{
              type: 'bar' as const,
              barWidth: narrow ? 18 : '56%',
              data,
              // Dotted rule: causes on one side, their sum on the other.
              markLine: {
                silent: true,
                symbol: 'none',
                label: { show: false },
                lineStyle: { color: tc.inkMuted, type: 'dashed' as const, width: 1 },
                data: [narrow ? { yAxis: sepIndex } : { xAxis: sepIndex }],
              },
            }],
          }}
        />
      </div>
    );
  }

  return (
    <Section title="Wealth Attribution" subtitle={subtitle}>
      {body}
    </Section>
  );
}

/* ────────────────────────────── Growth over time ────────────────────────────── */

function GrowthSection({ attr, status, onRetry, crossoverMonth }: { attr: PeriodAttribution | null; status: LoadStatus; onRetry: () => void; crossoverMonth: string }) {
  const tc = useThemeColors();
  const points = attr?.points ?? [];
  const baseLabel = attr?.fromZero ? 'Contributions' : 'Start value plus contributions';
  const baseline = points.map(p => (attr ? attr.startNW + (p.contributions - attr.startContrib) : 0));
  const lastNW = points.length ? points[points.length - 1].total : 0;
  const lastBase = baseline.length ? baseline[baseline.length - 1] : 0;
  const ready = status === 'ready' && points.length > 1;

  return (
    <Section
      title="Growth Over Time"
      subtitle={ready ? 'Net worth at each month end against what was paid in. The gap between the lines is what markets and revaluation added or took away.' : undefined}
      actions={ready ? (
        <ChartLegend items={[
          { label: 'Net worth', color: tc.forest, style: 'solid' },
          { label: baseLabel, color: tc.gold, style: 'dashed' },
        ]} />
      ) : undefined}
    >
      {status === 'loading' && <LoadingState message="Loading growth chart" />}
      {status === 'error' && <ErrorState message="The growth chart couldn’t be loaded." onRetry={onRetry} />}
      {status === 'ready' && points.length <= 1 && (
        <p className="text-[14px] text-ink-body">Growth is tracked per month. The chart appears once there are two month ends to compare.</p>
      )}
      {ready && (
        <>
          <EChartWrapper
            height="300px"
            option={{
              tooltip: {
                trigger: 'axis' as const,
                formatter: (params: unknown) => {
                  const ps = params as { dataIndex: number }[];
                  if (!Array.isArray(ps) || ps.length === 0) return '';
                  const i = ps[0].dataIndex;
                  const p = points[i];
                  return `${escapeHtml(fmtMonth(monthDate(p.month)))}<br/>Net worth: <strong>${fmtEUR(p.total)}</strong><br/>${escapeHtml(baseLabel)}: <strong>${fmtEUR(baseline[i])}</strong><br/>Difference: <strong>${fmtEUR(p.total - baseline[i], { sign: true })}</strong>`;
                },
              },
              grid: { top: 12, right: 30, bottom: 4, left: 4, containLabel: true },
              xAxis: { type: 'category' as const, boundaryGap: false, data: points.map(p => fmtMonthShort(monthDate(p.month))) },
              yAxis: { type: 'value' as const, scale: true, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
              series: [
                { name: 'Net worth', type: 'line' as const, color: tc.forest, data: points.map(p => p.total), showSymbol: points.length <= 3, symbolSize: 6, lineStyle: { width: 2 } },
                { name: baseLabel, type: 'line' as const, color: tc.gold, data: baseline, showSymbol: points.length <= 3, symbolSize: 6, lineStyle: { width: 2, type: 'dashed' as const } },
              ],
            }}
          />
          <p className="text-[13px] text-ink-muted mt-2">
            Latest month: net worth <span data-privacy="blur">{fmtEUR(lastNW)}</span>, {baseLabel.toLowerCase()} <span data-privacy="blur">{fmtEUR(lastBase)}</span>,
            a difference of <span className={toneCls(lastNW - lastBase)} data-privacy="blur">{fmtEUR(lastNW - lastBase, { sign: true })}</span>.
            {crossoverMonth && <> Cumulative returns first exceeded contributions in {fmtMonth(monthDate(crossoverMonth))}.</>}
          </p>
        </>
      )}
    </Section>
  );
}

/* ─────────────────────────────── Asset allocation ─────────────────────────────── */

function AllocationSection({ accounts, holdings, physicalValue }: { accounts: Account[]; holdings: HoldingRow[]; physicalValue: number }) {
  // Holdings-aware bins: every account contributes only its cash (by account
  // type), and every holding — in any account — is binned once by asset
  // class. An account's `balance` already includes its holdings, so adding it
  // as well would count pension, crypto and gold securities twice.
  const cashLabelByType: Record<string, string> = {
    real_estate: 'Real estate', pension: 'Pension', liability: 'Liabilities', credit: 'Credit',
  };
  const assetClassLabels: Record<string, string> = {
    etf: 'Equities', stock: 'Equities', fund: 'Equities',
    bond: 'Bonds', commodity: 'Commodities',
    crypto: 'Crypto', derivative: 'Derivatives',
  };
  const byClass: Record<string, number> = {};
  const add = (label: string, value: number) => {
    if (value === 0) return;
    byClass[label] = (byClass[label] || 0) + value;
  };
  for (const acc of accounts) {
    add(cashLabelByType[acc.type] || 'Cash', acc.cash_balance ?? 0);
  }
  // value_eur falls back to cost basis for unpriced positions, matching account balances.
  for (const h of holdings) {
    add(assetClassLabels[h.asset_class] || 'Other', h.value_eur ?? h.market_value ?? 0);
  }
  // Property and vehicles at full value; their loans are shown under Loans, not netted here.
  add('Real assets', physicalValue);

  const positive = Object.entries(byClass).filter(([, v]) => v > 0).sort(([, a], [, b]) => b - a);
  const negative = Object.entries(byClass).filter(([, v]) => v < 0);
  const gross = positive.reduce((s, [, v]) => s + v, 0);

  return (
    <Section
      title="Asset Allocation"
      subtitle={gross > 0 ? (
        <>
          Share of gross assets, <span data-privacy="blur">{fmtEUR(gross)}</span>.
          {physicalValue > 0 && ' Real assets are property and vehicles at current valuation, before loans.'}
        </>
      ) : undefined}
    >
      {positive.length === 0 ? (
        <p className="text-[14px] text-ink-body">No balances to show yet.</p>
      ) : (
        <RankedBars
          scale="total"
          items={positive.map(([label, value]) => ({
            label,
            value,
            display: fmtEUR(value),
            share: fmtPct((value / gross) * 100),
          }))}
        />
      )}
      {negative.length > 0 && (
        <p className="text-[12px] text-ink-muted mt-3">
          Not included: {negative.map(([label, value], i) => (
            <span key={label}>{i > 0 && ', '}{label} <span data-privacy="blur">{fmtEUR(value)}</span></span>
          ))}.
        </p>
      )}
    </Section>
  );
}

/* ───────────────────────────── Counterparty exposure ───────────────────────────── */

function CounterpartySection({ accounts }: { accounts: Account[] }) {
  const institutions: Record<string, { cash: number; securities: number }> = {};
  accounts.forEach(a => {
    const inst = a.institution;
    if (!institutions[inst]) {
      institutions[inst] = { cash: 0, securities: 0 };
    }
    if (a.type === 'checking' || a.type === 'savings') {
      institutions[inst].cash += a.balance ?? 0;
    } else if (a.type === 'brokerage') {
      institutions[inst].securities += a.holdings_value ?? 0;
      institutions[inst].cash += a.cash_balance ?? 0;
    }
  });
  const rows = Object.entries(institutions)
    .map(([inst, d]) => {
      const p = depositProtection(inst, d.cash);
      return {
        inst,
        name: institutionLabel(inst),
        cash: d.cash,
        securities: d.securities,
        exposure: d.cash + d.securities,
        scheme: p.scheme,
        status: p.status,
      };
    })
    .filter(r => Math.abs(r.exposure) >= 0.005)
    .sort((a, b) => b.exposure - a.exposure);
  const total = rows.reduce((s, r) => s + Math.max(0, r.exposure), 0);
  const share = (r: { exposure: number }) => (total > 0 ? fmtPct((Math.max(0, r.exposure) / total) * 100) : '—');

  const protection = (r: typeof rows[number]) => (
    <span className="flex flex-col items-start gap-1.5">
      {r.securities > 0 && <Chip>Segregated assets</Chip>}
      {r.cash > 0 && r.status === 'other' && <Chip>Cash under {r.scheme} (US)</Chip>}
      {r.cash > 0 && r.status !== 'other' && (
        <Chip tone={r.status === 'over' ? 'danger' : r.status === 'near' ? 'warning' : 'success'}>
          {r.status === 'over' ? 'Deposits over limit' : r.status === 'near' ? 'Deposits near limit' : 'Deposits insured'}
        </Chip>
      )}
      {r.securities <= 0 && r.cash <= 0 && <span className="text-ink-muted">—</span>}
    </span>
  );

  return (
    <Section
      title="Counterparty Exposure"
      subtitle={total > 0 ? <>Where securities and cash of <span data-privacy="blur">{fmtEUR(total)}</span> are held.</> : undefined}
    >
      {rows.length === 0 ? (
        <p className="text-[14px] text-ink-body">No bank or brokerage balances to show yet.</p>
      ) : (
        <>
          {/* Phones: one card per institution, nothing scrolls sideways */}
          <ul className="md:hidden apple-card divide-y divide-divider">
            {rows.map(r => (
              <li key={r.inst} className="px-4 py-3">
                <div className="flex items-baseline justify-between gap-3">
                  <span className="text-[14px] font-medium text-ink min-w-0 break-words">{r.name}</span>
                  <span className="text-[14px] text-ink shrink-0" data-privacy="blur">{fmtEUR(r.exposure)}</span>
                </div>
                <p className="text-[12px] text-ink-muted mt-0.5">
                  {share(r)} of the total · cash <span data-privacy="blur">{fmtEUR(r.cash)}</span>
                  {r.securities > 0 && <> · securities <span data-privacy="blur">{fmtEUR(r.securities)}</span></>}
                </p>
                <div className="mt-2">{protection(r)}</div>
              </li>
            ))}
          </ul>
          <div className="hidden md:block apple-card overflow-x-auto" role="region" tabIndex={0} aria-label="Counterparty exposure by institution">
            <table className="apple-table">
              <thead>
                <tr>
                  <th scope="col">Counterparty</th>
                  <th scope="col" className="num">Exposure</th>
                  <th scope="col" className="num">Share</th>
                  <th scope="col">Protection</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(r => (
                  <tr key={r.inst}>
                    <td className="font-medium text-ink">
                      {r.name}
                      <span className="block text-[12px] font-normal text-ink-muted" data-privacy="blur">
                        {r.securities > 0 ? `Securities ${fmtEUR(r.securities)} · cash ${fmtEUR(r.cash)}` : `Cash ${fmtEUR(r.cash)}`}
                      </span>
                    </td>
                    <td className="num whitespace-nowrap text-ink" data-privacy="blur" title={`Cash ${fmtEUR(r.cash)}, securities ${fmtEUR(r.securities)}`}>{fmtEUR(r.exposure)}</td>
                    <td className="num whitespace-nowrap">{share(r)}</td>
                    <td>{protection(r)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="text-[12px] text-ink-muted mt-2.5">
            Securities in a brokerage account are Sondervermögen (segregated assets) and stay yours if the institution fails.
            Cash is covered by statutory deposit insurance (Einlagensicherung; at savings banks, Institutssicherung) up to {fmtEUR(DEPOSIT_INSURANCE_LIMIT)} per institution;
            “near limit” starts at {fmtEUR(DEPOSIT_INSURANCE_NEAR)}.
            {rows.some(r => r.status === 'other' && r.cash > 0) && ' Cash at a US broker such as Morgan Stanley is protected by SIPC (up to US$ 250,000 against the broker failing), not by EU deposit insurance.'}
          </p>
        </>
      )}
    </Section>
  );
}

/* ────────────────────────────── Concentration risk ────────────────────────────── */

function ConcentrationSection({ holdings, totalNetWorth }: { holdings: HoldingRow[]; totalNetWorth: number }) {
  if (holdings.length === 0 || totalNetWorth <= 0) return null;
  // Any security above 10 % of net worth; critical beyond 25 %. Rows are per
  // account, so the same ISIN held at two brokers is summed first.
  const byISIN = new Map<string, { name: string; value: number }>();
  for (const h of holdings) {
    const value = h.value_eur ?? h.market_value ?? 0;
    const cur = byISIN.get(h.security_isin) ?? { name: h.name || h.security_isin, value: 0 };
    cur.value += value;
    byISIN.set(h.security_isin, cur);
  }
  const concentrated = [...byISIN.values()]
    .filter(s => s.value > 0 && s.value / totalNetWorth > 0.10)
    .map(s => ({
      name: s.name,
      value: s.value,
      pct: (s.value / totalNetWorth) * 100,
      critical: s.value / totalNetWorth > 0.25,
    }))
    .sort((a, b) => b.pct - a.pct);
  if (concentrated.length === 0) return null;
  return (
    <Section title="Concentration Risk" subtitle="Single securities above 10 % of net worth, summed across accounts; critical above 25 %.">
      <ul className="flex flex-col gap-2.5">
        {concentrated.map((c, i) => (
          <li key={i} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 rounded-apple border border-divider bg-inset px-4 py-3">
            <Chip tone={c.critical ? 'danger' : 'warning'} className="shrink-0">{c.critical ? 'Critical' : 'Warning'}</Chip>
            <span className="flex-1 basis-48 min-w-0 text-[14px] font-medium text-ink break-words" data-privacy="blur">{c.name}</span>
            <span className="text-[13px] text-ink-body">
              {fmtPct(c.pct)} of net worth · <span data-privacy="blur">{fmtEUR(c.value)}</span>
            </span>
          </li>
        ))}
      </ul>
    </Section>
  );
}

/* ──────────────────────────────────── Goals ──────────────────────────────────── */

const GOAL_STATUS: Record<GoalProgress['status'], { label: string; tone: 'success' | 'info' | 'warning' | 'danger' }> = {
  complete: { label: 'Complete', tone: 'success' },
  ahead: { label: 'Ahead', tone: 'success' },
  on_track: { label: 'On track', tone: 'info' },
  at_risk: { label: 'At risk', tone: 'warning' },
  behind: { label: 'Behind', tone: 'danger' },
};

function GoalsSections({ goals, accounts, holdings }: { goals: GoalProgress[]; accounts: Account[]; holdings: HoldingRow[] }) {
  const tc = useThemeColors();

  // What funds each goal, as the backend measures it: its account, or the
  // liquid accounts no goal is linked to.
  const linkedIds = new Set(goals.map(g => g.funding_account_id).filter((id): id is string => !!id && accounts.some(a => a.id === id)));
  const liquidPoolIds = new Set(accounts.filter(a => isLiquidAccountType(a.type) && !linkedIds.has(a.id)).map(a => a.id));
  const fundingOf = (g: GoalProgress) => {
    const account = g.funding_account_id ? accounts.find(a => a.id === g.funding_account_id) : undefined;
    return account
      ? { label: account.name, ids: new Set([account.id]) }
      : { label: 'liquid wealth', ids: liquidPoolIds };
  };
  // Share of equities (by value) in those accounts; null when they hold nothing.
  const equityPct = (ids: Set<string>): number | null => {
    const total = accounts.filter(a => ids.has(a.id)).reduce((s, a) => s + (a.balance ?? 0), 0);
    const equity = holdings
      .filter(h => ids.has(h.account_id) && EQUITY_CLASSES.has(h.asset_class))
      .reduce((s, h) => s + (h.value_eur ?? h.market_value ?? 0), 0);
    return total > 0 ? Math.round(Math.min(100, Math.max(0, (equity / total) * 100))) : null;
  };

  // Goal / asset mismatches
  const mismatches: { goal: string; severity: 'critical' | 'moderate' | 'info'; message: ReactNode }[] = [];
  goals.forEach(g => {
    const funding = fundingOf(g);
    const equity = equityPct(funding.ids);
    const yearsLeft = g.months_remaining / 12;
    // Critical: short-horizon goal (<3 years) funded mostly by equities
    if (yearsLeft > 0 && yearsLeft < 3 && equity !== null && equity >= 50) {
      mismatches.push({ goal: g.name, severity: 'critical', message: `Due in ${fmtDuration(g.months_remaining)} but ${equity} % of ${funding.label} is in equities. A market crash could prevent reaching the target. Consider moving to cash or bonds.` });
    }
    // Moderate: long-horizon goal (>10 years) sitting in cash
    if (yearsLeft > 10 && equity !== null && equity <= 10) {
      mismatches.push({ goal: g.name, severity: 'moderate', message: `${Math.floor(yearsLeft)}-year horizon but ${funding.label} is ${equity === 0 ? 'all' : 'almost all'} cash. Cash returns may not keep pace with inflation. Consider equities for long-term growth.` });
    }
    // Over-funded
    if (g.progress_pct > 110) {
      mismatches.push({ goal: g.name, severity: 'info', message: <>{fmtPct(g.progress_pct, { digits: 0 })} funded (<span data-privacy="blur">{fmtEUR(g.current_value - g.target_amount)}</span> above target). Consider reallocating the excess to other goals.</> });
    }
    // Shortfall: behind schedule with less than half funded and under five years left
    if (g.status === 'behind' && g.months_remaining > 0 && g.progress_pct < 50 && yearsLeft < 5) {
      const shortfall = g.target_amount - g.projected_value;
      if (shortfall > 0) {
        const topUp = monthlyTopUp(shortfall, g.months_remaining, g.assumed_return_pct);
        mismatches.push({ goal: g.name, severity: 'critical', message: <>Projected shortfall of <span data-privacy="blur">{fmtEUR(shortfall)}</span>. Increase contributions by <span data-privacy="blur">{fmtEUR(topUp)}</span> a month, at the assumed {fmtPct(g.assumed_return_pct)} return, to close the gap.</> });
      }
    }
  });

  // Glide path: equity % = min(100, max(0, (years − 2) / 18 × 100))
  const glideEquity = (yearsLeft: number) => Math.min(100, Math.max(0, Math.round((yearsLeft - 2) / 18 * 100)));
  const goalPaths = goals.filter(g => g.months_remaining > 0).flatMap(g => {
    const yearsLeft = g.months_remaining / 12;
    const recommended = glideEquity(yearsLeft);
    const actual = equityPct(fundingOf(g).ids);
    return actual === null ? [] : [{ name: g.name, yearsLeft, recommended, actual, drift: actual - recommended }];
  });
  const glideCurve: number[][] = [];
  for (let y = 0; y <= 30; y++) glideCurve.push([y, glideEquity(y)]);

  // Priority and shortfall — the budget is set on the Planning page (Contribution Router)
  const totalNeeded = goals.reduce((s, g) => s + g.monthly_contribution, 0);
  let budget = 0;
  try { budget = parseFloat(localStorage.getItem('monthly_invest_budget') || '') || 0; } catch { budget = 0; }
  const hasShortfall = budget > 0 && totalNeeded > budget;
  const showPriority = goals.length > 1 && (hasShortfall || goals.some(g => g.status === 'behind' || g.status === 'at_risk'));
  const priorityLabels: Record<number, string> = { 10: 'Emergency', 20: 'Employer match', 30: 'Debt payoff', 40: 'Time-sensitive', 50: 'Long-term' };
  let remaining = budget;
  const allocations = [...goals].sort((a, b) => (a.priority || 50) - (b.priority || 50) || a.target_date.localeCompare(b.target_date)).map(g => {
    const needed = g.monthly_contribution;
    const allocated = hasShortfall ? Math.min(needed, Math.max(0, remaining)) : needed;
    remaining -= allocated;
    return { ...g, allocated, shortfall: needed - allocated, priorityLabel: priorityLabels[g.priority] || `Priority ${g.priority}` };
  });

  return (
    <>
      <Section title="Goals" subtitle="Each goal counts the balance of its funding account, or else a share of liquid wealth handed out in priority order, so no euro counts twice. The projection grows only that amount.">
        <ul className="divide-y divide-divider">
          {goals.map(g => {
            // Clamp the bar to [0, 100] — progress can overshoot.
            const clampedPct = Math.max(0, Math.min(g.progress_pct, 100));
            let estimate = '';
            if (g.status === 'complete') {
              estimate = 'Reached';
            } else if (g.months_remaining > 0) {
              // When the projection first reaches the target, not the target date.
              estimate = g.estimated_date
                ? `${fmtDuration(g.months_remaining)} left, reached ${fmtMonth(keyToLocalDate(`${g.estimated_date}-01`))} at this pace`
                : `${fmtDuration(g.months_remaining)} left, not reached at this pace`;
            } else if (g.target_date) {
              estimate = `Target ${fmtMonth(keyToLocalDate(g.target_date))} — past due`;
            } else {
              estimate = 'Past due';
            }
            const st = GOAL_STATUS[g.status] ?? GOAL_STATUS.behind;
            const fundingLabel = fundingOf(g).label;
            return (
              <li key={g.id} className="py-3 first:pt-0">
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 mb-2">
                  <span className="text-[15px] font-medium text-ink min-w-0 break-words">{g.name}</span>
                  <span className="inline-flex flex-wrap items-center gap-2 text-[12px] text-ink-muted">
                    {(g.status === 'behind' || g.status === 'at_risk') && <span>projected <span data-privacy="blur">{fmtEUR(g.projected_value)}</span></span>}
                    <Chip tone={st.tone}>{st.label}</Chip>
                  </span>
                </div>
                <ProgressBar value={clampedPct} label={`${g.name}: ${fmtPct(clampedPct)} of target`} />
                <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-0.5 mt-1.5 text-[12px] text-ink-muted">
                  <span>{fmtPct(g.progress_pct)} · {estimate}</span>
                  <span>
                    <span data-privacy="blur">{fmtEUR(g.current_value)} of {fmtEUR(g.target_amount)}</span> from {fundingLabel}
                    {g.monthly_contribution > 0 && <> · <span data-privacy="blur">{fmtEUR(g.monthly_contribution)}</span> a month</>}
                  </span>
                </div>
              </li>
            );
          })}
        </ul>

        {mismatches.length > 0 && (
          <div className="mt-5">
            <SubHeading className="mb-2">Mismatch alerts</SubHeading>
            <ul className="flex flex-col gap-2.5">
              {mismatches.map((m, i) => (
                <li key={i} className="flex flex-wrap items-start gap-x-3 gap-y-1.5 rounded-apple border border-divider bg-inset px-4 py-3">
                  <Chip tone={m.severity === 'critical' ? 'danger' : m.severity === 'moderate' ? 'warning' : 'info'} className="shrink-0 mt-0.5">
                    {m.severity === 'critical' ? 'Critical' : m.severity === 'moderate' ? 'Warning' : 'Info'}
                  </Chip>
                  <div className="flex-1 basis-56 min-w-0">
                    <p className="text-[14px] font-medium text-ink break-words">{m.goal}</p>
                    <p className="text-[13px] text-ink-muted">{m.message}</p>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        )}
      </Section>

      {goalPaths.length > 0 && (
        <Section title="Glide Path" subtitle="The recommended share of equities falls as each goal approaches its target date. “Actual” is the share of equities in the money funding the goal: its account, or liquid wealth.">
          <EChartWrapper
            height="240px"
            option={{
              tooltip: { trigger: 'axis' as const, valueFormatter: (v: unknown) => `${fmtNum(Number(Array.isArray(v) ? v[1] : v))} %` },
              legend: { data: ['Recommended', 'Your goals'], bottom: 0 },
              grid: { left: 8, right: 20, top: 28, bottom: 52, containLabel: true },
              xAxis: { type: 'value' as const, name: 'Years to goal', nameLocation: 'middle' as const, nameGap: 26, min: 0, max: 30, splitLine: { show: false } },
              yAxis: { type: 'value' as const, name: 'Equities', nameTextStyle: { align: 'left' as const }, min: 0, max: 100, axisLabel: { formatter: (v: number) => `${v} %` } },
              series: [
                { name: 'Recommended', type: 'line' as const, color: tc.forest, data: glideCurve, showSymbol: false, lineStyle: { width: 2 } },
                { name: 'Your goals', type: 'scatter' as const, color: tc.gold, data: goalPaths.map(g => [Math.round(g.yearsLeft * 10) / 10, g.actual]), symbolSize: 12 },
              ],
            }}
          />
          <ul className="mt-3 divide-y divide-divider">
            {goalPaths.map(g => {
              const off = Math.abs(g.drift);
              return (
                <li key={g.name} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2.5">
                  <div className="min-w-0 flex-1 basis-40">
                    <p className="text-[14px] text-ink break-words">{g.name}</p>
                    <p className="text-[12px] text-ink-muted">{fmtDuration(Math.round(g.yearsLeft * 12))} remaining</p>
                  </div>
                  <p className="text-[13px] text-ink-body">
                    Recommended {g.recommended} % equities · actual {g.actual} %
                  </p>
                  <Chip tone={off > 20 ? 'danger' : off > 10 ? 'warning' : 'success'}>
                    {off > 10 ? `${fmtNum(off)} points ${g.drift > 0 ? 'above' : 'below'}` : 'In line'}
                  </Chip>
                </li>
              );
            })}
          </ul>
        </Section>
      )}

      {showPriority && (
        <Section
          title="Goal Priority"
          subtitle={hasShortfall
            ? <>Budget shortfall: <span data-privacy="blur">{fmtEUR(totalNeeded)}</span> a month needed but only <span data-privacy="blur">{fmtEUR(budget)}</span> available. Contributions are allocated by priority.</>
            : 'Goals ranked by priority, which is set when a goal is added on the Portfolio page.'}
        >
          <ol className="divide-y divide-divider">
            {allocations.map((g, i) => (
              <li key={g.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 py-2.5">
                <div className="flex items-baseline gap-2.5 min-w-0 flex-1 basis-48">
                  <span className="text-[13px] text-ink-muted w-5 shrink-0">{i + 1}.</span>
                  <div className="min-w-0">
                    <p className="text-[14px] text-ink break-words">{g.name}</p>
                    <p className="text-[12px] text-ink-muted">{g.priorityLabel} · {g.months_remaining > 0 ? `${fmtDuration(g.months_remaining)} left` : 'past due'}</p>
                  </div>
                </div>
                <div className="text-right text-[13px]">
                  <p className="text-ink"><span data-privacy="blur">{fmtEUR(hasShortfall ? g.allocated : g.monthly_contribution)}</span> a month</p>
                  {hasShortfall && g.shortfall > 0 && (
                    <p className="text-claret"><span data-privacy="blur">{fmtEUR(g.shortfall)}</span> unfunded</p>
                  )}
                  {!hasShortfall && (g.status === 'behind' || g.status === 'at_risk') && <p className="text-amber">{g.status === 'behind' ? 'Behind schedule' : 'At risk'}</p>}
                </div>
              </li>
            ))}
          </ol>
          {hasShortfall && (
            <p className="text-[13px] text-ink-body mt-3">
              Increase the budget by <span data-privacy="blur">{fmtEUR(totalNeeded - budget)}</span> a month to fully fund all goals.
            </p>
          )}
        </Section>
      )}
    </>
  );
}

/* ──────────────────────────────── Projection tab ──────────────────────────────── */

interface Scenario { name: string; contribution: number; return_pct: number; projection: { date: string; value: number }[] }

function readStored(key: string, fallback: string): string {
  try { return localStorage.getItem(key) || fallback; } catch { return fallback; }
}
function writeStored(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* storage unavailable: keep in memory only */ }
}

function Slider({ id, label, display, value, min, max, step, onChange }: {
  id: string; label: string; display: ReactNode; value: string; min: number; max: number; step: number; onChange: (v: string) => void;
}) {
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-3">
        <label htmlFor={id} className="text-[13px] font-medium text-ink">{label}</label>
        <output htmlFor={id} className="text-[14px] text-ink whitespace-nowrap">{display}</output>
      </div>
      <input
        id={id} type="range" min={min} max={max} step={step} value={value}
        onChange={e => onChange(e.target.value)}
        className="block w-full h-[44px] md:h-[32px] accent-forest cursor-pointer"
      />
    </div>
  );
}

function ProjectionTab() {
  const tc = useThemeColors();
  const [confirm, confirmDialog] = useConfirm();
  const [projData, setProjData] = useState<ProjectionData | null>(null);
  const [status, setStatus] = useState<LoadStatus>('loading');
  const [projContrib, setProjContrib] = useState('');
  const [projReturn, setProjReturn] = useState('');
  const [projHorizon, setProjHorizon] = useState(() => readStored('proj_horizon', '30'));
  const [projContribGrowth, setProjContribGrowth] = useState(() => readStored('proj_contrib_growth', '0'));
  const [savedScenarios, setSavedScenarios] = useState<Scenario[]>(() => {
    try { return JSON.parse(localStorage.getItem('proj_scenarios') || '[]'); } catch { return []; }
  });
  const [activeScenarios, setActiveScenarios] = useState<Set<number>>(() => new Set());
  const [showAllMilestones, setShowAllMilestones] = useState(false);

  const loadInitial = useCallback(() => {
    setStatus('loading');
    api.getProjection().then(d => {
      setProjData(d);
      if (d.contribution != null) setProjContrib(String(d.contribution));
      if (d.return_pct != null) setProjReturn(String(d.return_pct));
      setStatus('ready');
    }).catch(() => setStatus('error'));
  }, []);
  useEffect(() => { loadInitial(); }, [loadInitial]);

  // Re-project when a slider moves (debounced). FIRE sizing parameters live on
  // the Planning page; this tab only asks "where does investable wealth go under
  // these contribution / return / horizon assumptions?".
  useEffect(() => {
    if (!projContrib && !projReturn) return;
    const timer = setTimeout(() => {
      const c = parseFloat(projContrib);
      const r = parseFloat(projReturn);
      const horizon = parseInt(projHorizon, 10);
      const contribG = parseFloat(projContribGrowth);
      if (!isNaN(c) && !isNaN(r)) {
        api.getProjection(
          c, r,
          undefined, // expenses (Planning)
          undefined, // swr (Planning)
          undefined, // marginal_rate (Planning)
          undefined, // taxPortion
          !isNaN(horizon) && horizon >= 1 && horizon <= 50 ? horizon : undefined,
          !isNaN(contribG) && contribG >= 0 && contribG <= 20 ? contribG : undefined,
        ).then(setProjData).catch(() => {});
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [projContrib, projReturn, projHorizon, projContribGrowth]);

  if (status === 'loading') return <LoadingState message="Loading projection" lines={6} />;
  if (status === 'error' || !projData) {
    return <div className="mt-6"><ErrorState message="The projection couldn’t be loaded." onRetry={loadInitial} /></div>;
  }
  if (projData.history.length === 0 && projData.projection.length === 0) {
    return (
      <div className="mt-6">
        <EmptyState
          title="Nothing to project yet"
          message="The projection starts from your securities and cash. Import transactions to see where they could go."
          action={{ label: 'Go to Settings', href: '/settings' }}
        />
      </div>
    );
  }

  const contribNum = parseFloat(projContrib) || 0;
  const returnNum = parseFloat(projReturn) || 0;
  const horizonNum = parseInt(projHorizon, 10) || 0;
  const growthNum = parseFloat(projContribGrowth) || 0;
  const scenarioColors = [tc.walnut, tc.claret, tc.sage];
  const shownScenarios = savedScenarios.map((s, i) => ({ s, i })).filter(({ i }) => activeScenarios.has(i));
  const hist = projData.history;
  const proj = projData.projection;
  const milestones = projData.milestones ?? [];
  const shownMilestones = showAllMilestones ? milestones : milestones.slice(0, 2);
  const acc = projData.projection_accuracy;

  const saveScenario = () => {
    if (savedScenarios.length >= 3) return;
    const name = `${fmtEUR(contribNum)} a month at ${fmtPct(returnNum)}`;
    const updated = [...savedScenarios, { name, contribution: contribNum, return_pct: returnNum || 7, projection: proj }];
    setSavedScenarios(updated);
    writeStored('proj_scenarios', JSON.stringify(updated));
    setActiveScenarios(prev => new Set([...prev, updated.length - 1]));
  };
  const deleteScenario = async (i: number) => {
    const ok = await confirm({ title: `Delete scenario “${savedScenarios[i].name}”?`, body: 'The saved comparison line is removed from this browser.', confirmLabel: 'Delete scenario' });
    if (!ok) return;
    const updated = savedScenarios.filter((_, j) => j !== i);
    setSavedScenarios(updated);
    writeStored('proj_scenarios', JSON.stringify(updated));
    setActiveScenarios(prev => {
      const next = new Set<number>();
      prev.forEach(idx => { if (idx < i) next.add(idx); else if (idx > i) next.add(idx - 1); });
      return next;
    });
  };

  const legend: LegendItem[] = [
    { label: 'Historical', color: tc.forest, style: 'solid' },
    { label: 'Projected', color: tc.gold, style: 'dashed' },
    ...(projData.has_confidence ? [{ label: 'Range, 10th to 90th percentile', color: tc.gold, style: 'band' as const }] : []),
    ...(projData.target_amount > 0 ? [{ label: projData.target_goal_name ? `Goal: ${projData.target_goal_name}` : 'Target', color: tc.slate, style: 'dotted' as const }] : []),
    ...shownScenarios.map(({ s }, k) => ({ label: s.name, color: scenarioColors[k % scenarioColors.length], style: 'dashed' as const })),
  ];

  return (
    <>
      <Section
        first
        title="Wealth Projection"
        subtitle={
          <>
            Investable wealth (cash and securities in every account, pension included) so far, and where it goes under the assumptions below. Property, vehicles and their loans are not projected.
            {projData.target_amount > 0 && <> {projData.target_goal_name ? <>Goal “{projData.target_goal_name}”</> : 'Target'}: <span data-privacy="blur">{fmtEUR(projData.target_amount)}</span>.</>}
          </>
        }
      >
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-8 gap-y-3 mb-4">
          <Slider id="proj-contrib" label="Monthly contribution" min={0} max={5000} step={100} value={projContrib} onChange={setProjContrib}
            display={<span data-privacy="blur">{fmtEUR(contribNum)}</span>} />
          <Slider id="proj-return" label="Return" min={0} max={15} step={0.5} value={projReturn} onChange={setProjReturn}
            display={`${fmtPct(returnNum)} per year`} />
          <Slider id="proj-horizon" label="Horizon" min={1} max={50} step={1} value={projHorizon}
            onChange={v => { setProjHorizon(v); writeStored('proj_horizon', v); }}
            display={`${horizonNum} ${horizonNum === 1 ? 'year' : 'years'}`} />
          <Slider id="proj-growth" label="Contribution growth" min={0} max={20} step={0.5} value={projContribGrowth}
            onChange={v => { setProjContribGrowth(v); writeStored('proj_contrib_growth', v); }}
            display={`${fmtPct(growthNum)} per year`} />
        </div>

        {/* Save and compare scenarios */}
        <div className="flex flex-wrap items-center gap-2.5 mb-5">
          <button type="button" onClick={saveScenario} disabled={savedScenarios.length >= 3} className="apple-btn-secondary min-h-[44px] md:min-h-[40px]">
            Save scenario ({savedScenarios.length} of 3)
          </button>
          {savedScenarios.map((s, i) => {
            const on = activeScenarios.has(i);
            return (
              <span key={i} className="inline-flex items-stretch rounded-[4px] border border-field">
                <button
                  type="button"
                  aria-pressed={on}
                  onClick={() => setActiveScenarios(prev => {
                    const next = new Set(prev);
                    if (next.has(i)) next.delete(i); else next.add(i);
                    return next;
                  })}
                  className={`min-h-[44px] md:min-h-[36px] px-3 text-[13px] rounded-l-[3px] transition-colors ${on ? 'bg-forest text-parchment font-semibold' : 'text-ink-body font-medium hover:text-ink'}`}
                >
                  <span data-privacy="blur">{s.name}</span>{on ? ' · shown' : ''}
                </button>
                <button
                  type="button"
                  onClick={() => deleteScenario(i)}
                  aria-label={`Delete scenario ${s.name}…`}
                  className="min-h-[44px] md:min-h-[36px] min-w-[44px] md:min-w-[36px] inline-flex items-center justify-center border-l border-field text-ink-muted hover:text-claret"
                >
                  <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18" /></svg>
                </button>
              </span>
            );
          })}
        </div>

        <ChartLegend items={legend} className="mb-2" />
        <EChartWrapper
          height="340px"
          option={{
            tooltip: {
              trigger: 'axis' as const,
              formatter: (params: unknown) => {
                const ps = params as { seriesName: string; data: number | null; dataIndex: number; marker: string }[];
                if (!Array.isArray(ps) || ps.length === 0) return '';
                const idx = ps[0].dataIndex;
                const dateKey = idx < hist.length ? hist[idx].date : proj[idx - hist.length]?.date ?? '';
                const projPt = idx >= hist.length ? proj[idx - hist.length] : undefined;
                const rows = ps.filter(p => p.data != null).map(p => {
                  const val = p.seriesName === '90th percentile' && projPt?.p90 != null ? projPt.p90 : (p.data as number);
                  return `${p.marker} ${escapeHtml(p.seriesName)}: ${fmtEUR(val)}`;
                });
                return `${escapeHtml(fmtMonth(monthDate(dateKey)))}<br/>${rows.join('<br/>')}`;
              },
            },
            grid: { left: 4, right: 30, top: 12, bottom: 4, containLabel: true },
            xAxis: {
              type: 'category' as const,
              boundaryGap: false,
              data: [...hist.map(h => fmtMonthShort(monthDate(h.date))), ...proj.map(p => fmtMonthShort(monthDate(p.date)))],
            },
            yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
            series: [
              {
                name: 'Historical', type: 'line' as const, color: tc.forest,
                data: [...hist.map(h => h.value), ...proj.map(() => null)],
                showSymbol: false, lineStyle: { width: 2 },
              },
              {
                name: 'Projected', type: 'line' as const, color: tc.gold,
                data: [...hist.map(() => null), ...proj.map(p => p.value)],
                showSymbol: false, lineStyle: { width: 2, type: 'dashed' as const },
              },
              ...(projData.has_confidence ? [
                {
                  name: '10th percentile', type: 'line' as const, color: tc.gold, stack: 'confidence',
                  data: [...hist.map(() => null), ...proj.map(p => p.p10 ?? null)],
                  showSymbol: false, lineStyle: { width: 0 },
                },
                {
                  name: '90th percentile', type: 'line' as const, color: tc.gold, stack: 'confidence',
                  data: [...hist.map(() => null), ...proj.map(p => (p.p10 != null && p.p90 != null ? Math.max(0, p.p90 - p.p10) : null))],
                  showSymbol: false, lineStyle: { width: 0 }, areaStyle: { color: tc.gold, opacity: 0.16 },
                },
              ] : []),
              ...(projData.target_amount > 0 ? [{
                name: projData.target_goal_name ? `Goal: ${projData.target_goal_name}` : 'Target', type: 'line' as const, color: tc.slate,
                data: [...hist.map(() => null), ...proj.map(() => projData.target_amount)],
                showSymbol: false, lineStyle: { width: 1.5, type: 'dotted' as const },
              }] : []),
              ...shownScenarios.map(({ s }, k) => {
                const byDate = new Map(s.projection.map(p => [p.date, p.value]));
                return {
                  name: s.name, type: 'line' as const, color: scenarioColors[k % scenarioColors.length],
                  data: [...hist.map(() => null), ...proj.map(p => byDate.get(p.date) ?? null)],
                  showSymbol: false, lineStyle: { width: 1.5, type: 'dashed' as const },
                };
              }),
            ],
          }}
        />
      </Section>

      {milestones.length > 0 && (
        <SectionBoundary>
          <Section
            title="Projection Milestones"
            subtitle={<>Projected investable wealth, assuming a return of {fmtPct(projData.return_pct)} per year and <span data-privacy="blur">{fmtEUR(projData.contribution)}</span> paid in each month. “Contributed” is what you pay in from today, on top of today’s value. “Real” is in today’s money at 2 % inflation.</>}
          >
            {/* Phones: cards */}
            <ul className="md:hidden flex flex-col gap-2.5">
              {shownMilestones.map(m => (
                <li key={m.year} className="apple-card px-4 py-3">
                  <div className="flex items-baseline justify-between gap-3 mb-1.5">
                    <span className="text-[14px] font-semibold text-ink">{m.year} <span className="text-ink-muted font-normal">in {m.years_from_now} years</span></span>
                    <span className="text-[14px] font-semibold text-ink" data-privacy="blur">{fmtEUR(m.projected_value)}</span>
                  </div>
                  <dl className="grid grid-cols-3 gap-x-3 text-[12px]">
                    <div><dt className="text-ink-muted">Contributed</dt><dd className="text-ink" data-privacy="blur">{fmtEUR(m.contributions)}</dd></div>
                    <div><dt className="text-ink-muted">Growth</dt><dd className="text-ink" data-privacy="blur">{fmtEUR(m.growth, { sign: true })}</dd></div>
                    <div><dt className="text-ink-muted">Real</dt><dd className="text-ink" data-privacy="blur">{fmtEUR(m.real_value)}</dd></div>
                  </dl>
                </li>
              ))}
            </ul>
            <div className="hidden md:block apple-card overflow-x-auto" role="region" tabIndex={0} aria-label="Projection milestones">
              <table className="apple-table">
                <thead>
                  <tr>
                    <th scope="col">Year</th>
                    <th scope="col" className="num">Projected</th>
                    <th scope="col" className="num">Contributed</th>
                    <th scope="col" className="num">Growth</th>
                    <th scope="col" className="num">Real</th>
                  </tr>
                </thead>
                <tbody>
                  {shownMilestones.map(m => (
                    <tr key={m.year}>
                      <td><span className="font-medium text-ink">{m.year}</span> <span className="text-ink-muted">in {m.years_from_now} years</span></td>
                      <td className="num font-medium text-ink" data-privacy="blur">{fmtEUR(m.projected_value)}</td>
                      <td className="num" data-privacy="blur">{fmtEUR(m.contributions)}</td>
                      <td className="num" data-privacy="blur">{fmtEUR(m.growth, { sign: true })}</td>
                      <td className="num" data-privacy="blur">{fmtEUR(m.real_value)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            {milestones.length > 2 && (
              <button type="button" onClick={() => setShowAllMilestones(prev => !prev)} aria-expanded={showAllMilestones}
                className="apple-btn-secondary apple-btn-sm min-h-[44px] md:min-h-[36px] mt-3">
                {showAllMilestones ? 'Show fewer' : `Show all ${milestones.length} milestones`}
              </button>
            )}
          </Section>
        </SectionBoundary>
      )}

      {acc && (
        <SectionBoundary>
          <Section
            title="Projection Accuracy"
            subtitle={acc.diff_pct >= 0
              ? `Investable wealth is ${fmtPct(acc.diff_pct)} above the default projection made ${acc.months_ago} months ago${acc.assumed_contribution != null ? ` (${fmtEUR(acc.assumed_contribution)} a month at ${fmtPct(acc.assumed_return_pct ?? 0)})` : ''}.`
              : `Investable wealth is ${fmtPct(Math.abs(acc.diff_pct))} below the default projection made ${acc.months_ago} months ago${acc.assumed_contribution != null ? ` (${fmtEUR(acc.assumed_contribution)} a month at ${fmtPct(acc.assumed_return_pct ?? 0)})` : ''}.`}
          >
            <MetricGrid>
              <MetricCard label={`${acc.months_ago} months ago`} value={fmtEUR(acc.past_value)} sublabel="Starting value" />
              <MetricCard label="Projected" value={fmtEUR(acc.projected_value)} sublabel="For today" />
              <MetricCard label="Actual" value={fmtEUR(acc.actual_value)} sublabel="Today" />
              <MetricCard label="Difference" value={fmtEUR(acc.diff_eur, { sign: true })}
                tone={acc.diff_eur > 0 ? 'positive' : acc.diff_eur < 0 ? 'negative' : 'neutral'}
                sublabel={`${fmtPct(acc.diff_pct, { sign: true })} against the projection`} />
            </MetricGrid>
          </Section>
        </SectionBoundary>
      )}

      {projData.sensitivity && projData.sensitivity.length > 0 && projData.projection_end ? (
        <SectionBoundary>
          <SensitivitySection bars={projData.sensitivity} baseline={projData.projection_end} />
        </SectionBoundary>
      ) : null}
      {confirmDialog}
    </>
  );
}

function SensitivitySection({ bars, baseline }: { bars: NonNullable<ProjectionData['sensitivity']>; baseline: number }) {
  const tc = useThemeColors();
  const narrow = useIsNarrow();
  // Bars start at the baseline outcome and run to the pessimistic / optimistic outcome.
  const lowD = bars.map(s => s.low - (s.base ?? baseline));
  const highD = bars.map(s => s.high - (s.base ?? baseline));
  const reach = Math.max(1, ...lowD.map(Math.abs), ...highD.map(Math.abs));
  const pad = reach * (narrow ? 0.55 : 0.3);
  return (
    <Section
      title="Sensitivity Analysis"
      subtitle={<>Projected investable wealth at the end of the horizon when one assumption changes. Bars start at the baseline of <span data-privacy="blur">{fmtEUR(baseline)}</span>; the labels show the resulting value.</>}
      actions={<ChartLegend items={[{ label: 'Pessimistic', color: tc.claret, style: 'box' }, { label: 'Optimistic', color: tc.forest, style: 'box' }]} />}
    >
      <EChartWrapper
        height={`${bars.length * 56 + 44}px`}
        option={{
          tooltip: {
            trigger: 'axis' as const,
            axisPointer: { type: 'shadow' as const },
            formatter: (params: unknown) => {
              const ps = params as { dataIndex: number }[];
              if (!Array.isArray(ps) || ps.length === 0) return '';
              const s = bars[ps[0].dataIndex];
              return `${escapeHtml(s.label)}<br/>Pessimistic: <strong>${fmtEUR(s.low)}</strong><br/>Baseline: ${fmtEUR(s.base ?? baseline)}<br/>Optimistic: <strong>${fmtEUR(s.high)}</strong>`;
            },
          },
          grid: { left: 4, right: 8, top: 8, bottom: 4, containLabel: true },
          xAxis: {
            type: 'value' as const,
            min: -(reach + pad),
            max: reach + pad,
            splitNumber: narrow ? 2 : 4,
            axisLabel: { showMinLabel: false, showMaxLabel: false, formatter: (v: number) => (v === 0 ? 'Baseline' : (v > 0 ? '+' : '') + fmtAxis(v)) },
          },
          yAxis: {
            type: 'category' as const,
            inverse: true,
            data: bars.map(s => s.label),
            axisLabel: { interval: 0, hideOverlap: false, width: narrow ? 92 : 170, overflow: 'break' as const, color: tc.inkBody, fontSize: 12, lineHeight: 15 },
          },
          series: [
            {
              name: 'Pessimistic', type: 'bar' as const, stack: 'range', color: tc.claret, barMaxWidth: 22,
              data: lowD,
              label: { show: true, position: 'left' as const, color: tc.ink, fontSize: 12, formatter: (p: unknown) => fmtAxis(bars[(p as { dataIndex: number }).dataIndex].low) },
            },
            {
              name: 'Optimistic', type: 'bar' as const, stack: 'range', color: tc.forest, barMaxWidth: 22,
              data: highD,
              label: { show: true, position: 'right' as const, color: tc.ink, fontSize: 12, formatter: (p: unknown) => fmtAxis(bars[(p as { dataIndex: number }).dataIndex].high) },
            },
          ],
        }}
      />
      <p className="text-[12px] text-ink-muted mt-2">
        Ranges: return 3 percentage points lower or higher, contribution 500 € a month lower or higher, starting value 20 % lower or higher.
      </p>
    </Section>
  );
}

/* ───────────────────────────────── Accounts tab ───────────────────────────────── */

function AccountsTab({ accounts, allSnapshots }: { accounts: Account[]; allSnapshots: NetWorthSnapshot[] }) {
  const tc = useThemeColors();
  const [period, setPeriod] = useState<Period>('1Y');

  // Weekday snapshots within the period, oldest first
  const chartSnapshots = useMemo(
    () => filterByPeriod(allSnapshots, period).filter(s => isWeekdayKey(s.date)).reverse(),
    [allSnapshots, period]);
  const breakdownLabels = chartSnapshots.map(s => formatDateForPeriod(s.date, period));
  const hasPhysical = chartSnapshots.some(s => (s.physical_component ?? 0) > 0);
  const bands: { name: string; color: string; pick: (s: NetWorthSnapshot) => number }[] = [
    { name: 'Cash', color: tc.slate, pick: s => s.cash_component },
    { name: 'Investments', color: tc.forest, pick: s => s.investment_component },
    // What's yours in property and vehicles (value minus loans), so the stack adds up to net worth.
    ...(hasPhysical ? [{ name: 'Property and vehicles, after loans', color: tc.gold, pick: (s: NetWorthSnapshot) => (s.physical_component ?? 0) - (s.debt_component ?? 0) }] : []),
  ];

  const assetClassLabels: Record<string, string> = {
    brokerage: 'Investments', checking: 'Cash', savings: 'Cash',
    real_estate: 'Real estate', pension: 'Pension', precious_metals: 'Precious metals',
    liability: 'Liabilities', credit: 'Credit',
  };
  const groups: Record<string, Account[]> = {};
  for (const acc of accounts) {
    const label = assetClassLabels[acc.type] || 'Other';
    (groups[label] ??= []).push(acc);
  }
  const groupOrder = ['Investments', 'Cash', 'Real estate', 'Pension', 'Precious metals', 'Liabilities', 'Credit', 'Other'];

  // Per-institution cash totals for the deposit insurance indicator
  const instCashTotals: Record<string, number> = {};
  accounts.forEach(a => {
    if (a.type === 'checking' || a.type === 'savings') {
      instCashTotals[a.institution] = (instCashTotals[a.institution] || 0) + (a.balance ?? 0);
    } else if (a.type === 'brokerage' && a.cash_balance) {
      instCashTotals[a.institution] = (instCashTotals[a.institution] || 0) + a.cash_balance;
    }
  });
  // 90-day trend lines. Snapshots only split cash and investments in total, so the
  // line is the total for that kind of account — labelled as such on the card.
  const sparkSnaps = useMemo(() => {
    const cutoff90 = new Date();
    cutoff90.setDate(cutoff90.getDate() - 90);
    const key = localDateKey(cutoff90);
    return allSnapshots.filter(s => dateKeyOf(s.date) >= key).reverse();
  }, [allSnapshots]);
  const investSpark = sparkSnaps.map(s => s.investment_component);
  const cashSpark = sparkSnaps.map(s => s.cash_component);

  return (
    <>
      <SectionBoundary>
        <Section
          first
          title="Breakdown Over Time"
          subtitle={`Cash, investments${hasPhysical ? ' and property after loans' : ''}, stacked so the top edge is net worth. Showing ${PERIOD_PHRASE[period] === 'all time' ? 'all recorded history' : `the change ${PERIOD_PHRASE[period]}`}.`}
          actions={<PeriodSelector value={period} onChange={setPeriod} periods={PERIODS} />}
        >
          {chartSnapshots.length < 2 ? (
            <p className="text-[14px] text-ink-body">There are not enough snapshots in this period yet. Choose a longer period.</p>
          ) : (
            <>
              <ChartLegend items={bands.map(b => ({ label: b.name, color: b.color, style: 'box' as const }))} className="mb-2" />
              <EChartWrapper
                height="300px"
                option={{
                  tooltip: {
                    trigger: 'axis' as const,
                    formatter: (params: unknown) => {
                      const p = params as { seriesName: string; data: number; dataIndex: number; marker: string }[];
                      if (!Array.isArray(p) || p.length === 0) return '';
                      const snap = chartSnapshots[p[0].dataIndex];
                      const total = p.reduce((sum, s) => sum + (s.data || 0), 0);
                      const rows = p.map(s => `${s.marker} ${escapeHtml(s.seriesName)}: ${fmtEUR(s.data)}`).join('<br/>');
                      return `<strong>${escapeHtml(fmtDateLong(snap.date))}</strong><br/>${rows}<br/>Net worth: <strong>${fmtEUR(total)}</strong>`;
                    },
                  },
                  grid: { left: 4, right: 30, top: 12, bottom: 4, containLabel: true },
                  xAxis: { type: 'category' as const, boundaryGap: false, data: breakdownLabels, axisLabel: { interval: (i: number) => i === 0 || breakdownLabels[i] !== breakdownLabels[i - 1] } },
                  yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => fmtAxis(v) } },
                  series: bands.map(b => ({
                    name: b.name, type: 'line' as const, stack: 'total', color: b.color,
                    data: chartSnapshots.map(b.pick),
                    showSymbol: false, lineStyle: { width: 1 }, areaStyle: { color: b.color, opacity: 0.55 },
                  })),
                }}
              />
            </>
          )}
        </Section>
      </SectionBoundary>

      <SectionBoundary>
        <Section title="Accounts" subtitle="Balances in euros, grouped by kind of account.">
          {accounts.length === 0 ? (
            <EmptyState title="No accounts yet" message="Create an account in Settings and import its transactions to see it here." action={{ label: 'Go to Settings', href: '/settings?tab=accounts' }} />
          ) : (
            <div className="space-y-7">
              {groupOrder.filter(g => groups[g]).map(group => (
                <div key={group}>
                  <div className="flex flex-wrap items-baseline justify-between gap-x-3 mb-3">
                    <h3 className="label-caps">{group}</h3>
                    {Object.keys(groups).length > 1 && (
                      <span className="text-[13px] text-ink-muted" data-privacy="blur">
                        {fmtEUR(groups[group].reduce((s, a) => s + (a.balance ?? 0), 0))}
                      </span>
                    )}
                  </div>
                  <div className="grid grid-cols-1 gap-x-8 gap-y-5 sm:grid-cols-2 lg:grid-cols-3">
                    {groups[group].map(acc => {
                      const isCash = acc.type === 'checking' || acc.type === 'savings';
                      const isBroker = acc.type === 'brokerage';
                      return (
                        <AccountCard
                          key={acc.id}
                          account={acc}
                          sparklineData={isBroker ? investSpark : isCash ? cashSpark : undefined}
                          sparklineLabel={isBroker ? 'All investments, 90 days' : isCash ? 'All cash, 90 days' : undefined}
                          institutionCashTotal={instCashTotals[acc.institution]}
                        />
                      );
                    })}
                  </div>
                </div>
              ))}
            </div>
          )}
        </Section>
      </SectionBoundary>
    </>
  );
}
