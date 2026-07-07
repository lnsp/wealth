import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { api, type Account, type HoldingRow, type NetWorthSnapshot, type GoalProgress, type ProjectionData, type SavingsRateData } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import AccountCard from '../components/AccountCard';
import { TabBar } from '../components/ui';
import { useThemeColors } from '../hooks/useThemeColors';
import { useMarketRefresh } from '../hooks/useMarketRefresh';
import UnvestedPanel from '../components/UnvestedPanel';

type Period = '1D' | '1W' | '1M' | '3M' | '6M' | 'YTD' | '1Y' | 'All';
const PERIODS: Period[] = ['1D', '1W', '1M', '3M', '6M', 'YTD', '1Y', 'All'];

type NWTab = 'overview' | 'projection' | 'accounts';
const NW_TABS_DASHBOARD: { id: NWTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'projection', label: 'Projection' },
  { id: 'accounts', label: 'Accounts' },
];

function periodToDays(period: Period): number {
  const now = new Date();
  switch (period) {
    case '1D': return 2;
    case '1W': return 7;
    case '1M': return 30;
    case '3M': return 90;
    case '6M': return 180;
    case 'YTD': {
      const start = new Date(now.getFullYear(), 0, 1);
      return Math.ceil((now.getTime() - start.getTime()) / (1000 * 60 * 60 * 24)) + 1;
    }
    case '1Y': return 365;
    case 'All': return 9999;
  }
}

function periodLabel(period: Period): string {
  if (period === '1D') return 'today';
  if (period === '1W') return 'this week';
  if (period === 'All') return 'all time';
  return period;
}

function formatChartDate(dateStr: string, period: Period): string {
  const d = new Date(dateStr);
  switch (period) {
    case '1D':
      return d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' });
    case '1W':
      return d.toLocaleDateString('de-DE', { weekday: 'short', day: 'numeric', month: 'short' });
    case '1M':
    case '3M':
      return d.toLocaleDateString('de-DE', { day: 'numeric', month: 'short' });
    case '6M':
    case 'YTD':
    case '1Y':
      return d.toLocaleDateString('de-DE', { day: 'numeric', month: 'short', year: '2-digit' });
    case 'All':
      return d.toLocaleDateString('de-DE', { month: 'short', year: '2-digit' });
  }
}

export default function NetWorth({ defaultTab }: { defaultTab?: NWTab } = {}) {
  const tc = useThemeColors();
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [allSnapshots, setAllSnapshots] = useState<NetWorthSnapshot[]>([]);
  const [loading, setLoading] = useState(true);
  const [period, setPeriod] = useState<Period>('1M');
  const [goals, setGoals] = useState<GoalProgress[]>([]);
  const [projData, setProjData] = useState<ProjectionData | null>(null);
  const [projContrib, setProjContrib] = useState<string>('');
  const [projReturn, setProjReturn] = useState<string>('');
  const [projHorizon, setProjHorizon] = useState<string>(() => localStorage.getItem('proj_horizon') || '30');
  const [projContribGrowth, setProjContribGrowth] = useState<string>(() => localStorage.getItem('proj_contrib_growth') || '0');
  const [savedScenarios, setSavedScenarios] = useState<{ name: string; contribution: number; return_pct: number; projection: { date: string; value: number }[] }[]>(() => {
    try { return JSON.parse(localStorage.getItem('proj_scenarios') || '[]'); } catch { return []; }
  });
  const [activeScenarios, setActiveScenarios] = useState<Set<number>>(() => new Set());
  const [savingsRate, setSavingsRate] = useState<SavingsRateData | null>(null);
  const [showAllMilestones, setShowAllMilestones] = useState(false);
  const [nextActions, setNextActions] = useState<{ title: string; detail: string; impact_eur: number; urgency: string; category: string; link: string }[]>([]);
  const [unreadAlerts, setUnreadAlerts] = useState(0);
  const [jobErrors, setJobErrors] = useState(0);
  const [attribution, setAttribution] = useState('');
  // total_change from /api/portfolio/attribution — market-only price-delta on
  // held shares. Reconciles vs dailyChange (which is total ΔNW including cash
  // moves) so we can surface the gap when they diverge.
  const [attributionTotal, setAttributionTotal] = useState<number | null>(null);
  const [showAttribution, setShowAttribution] = useState(false);
  const [activeTab, setActiveTab] = useState<NWTab>(defaultTab || 'overview');
  const [wfData, setWfData] = useState<{ waterfall: { label: string; value: number; type: string }[]; time_series: { month: string; contributions: number; market_return: number; dividends: number; interest: number; total: number }[]; crossover_month: string; net_contributions: number; market_returns: number; dividends: number; current_nw: number } | null>(null);
  const [intradayPoints, setIntradayPoints] = useState<{ recorded_at: string; total: number; cash_component: number; investment_component: number }[]>([]);

  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [loadError, setLoadError] = useState<string | null>(null);
  const loadData = useCallback(async () => {
    try {
      setLoadError(null);
      const [accRes, nwRes] = await Promise.all([
        api.listAccounts(),
        api.getNetWorth(9999),
      ]);
      setAccounts(accRes.accounts || []);
      setAllSnapshots(nwRes.snapshots || []);
      // Holdings drive the holdings-aware asset-allocation donut; failure is
      // non-fatal — donut just falls back to "no holdings" state.
      api.listHoldings().then(r => setHoldings(r.holdings || [])).catch(() => {});
      // Also fetch intraday data for 1D view
      api.getNetWorthIntraday().then(r => setIntradayPoints(r.points || [])).catch(() => {});
    } catch (e) {
      console.error('Failed to load data:', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load data');
    } finally {
      setLoading(false);
    }
  }, []);

  // Track previous net worth for tick animation
  const prevNetWorth = useRef<number | null>(null);
  const [tick, setTick] = useState<'up' | 'down' | null>(null);

  useEffect(() => { loadData(); }, [loadData]);

  // Auto-refresh during market hours (every 60s)
  useMarketRefresh(loadData, 60_000);

  // Tick animation: flash sage/claret when net worth changes
  useEffect(() => {
    if (allSnapshots.length === 0) return;
    const current = allSnapshots[allSnapshots.length - 1]?.total ?? 0;
    if (prevNetWorth.current !== null && current !== prevNetWorth.current) {
      setTick(current > prevNetWorth.current ? 'up' : 'down');
      const timer = setTimeout(() => setTick(null), 1200);
      return () => clearTimeout(timer);
    }
    prevNetWorth.current = current;
  }, [allSnapshots]);

  useEffect(() => {
    api.getGoalsProgress().then(r => setGoals(r.goals || [])).catch(() => {});
    api.getSavingsRate().then(setSavingsRate).catch(() => {});
    fetch('/api/portfolio/next-actions').then(r => r.json()).then(d => setNextActions(d.actions || [])).catch(() => {});
    api.listNotifications().then(r => setUnreadAlerts(r.unread_count)).catch(() => {});
    api.getAttribution().then(r => { setAttribution(r.summary); setAttributionTotal(r.total_change ?? null); }).catch(() => {});
    fetch('/api/portfolio/wealth-waterfall').then(r => r.json()).then(setWfData).catch(() => {});
    fetch('/api/settings/scheduler-status').then(r => r.json()).then(d => {
      setJobErrors((d.jobs || []).filter((j: { status: string }) => j.status === 'error').length);
    }).catch(() => {});
    api.getProjection().then(d => {
      setProjData(d);
      if (d.contribution != null) setProjContrib(String(d.contribution));
      if (d.return_pct != null) setProjReturn(String(d.return_pct));
    }).catch(() => {});
  }, []);

  // Auto-update projection when sliders change (debounced). FIRE-target
  // sizing parameters (annual expenses, SWR, marginal tax, contribution
  // growth) live on the Planning page now and re-fetch their own projection
  // independently — Net Worth's Projection tab is purely "what's my future
  // NW under these contrib/return/horizon assumptions?".
  useEffect(() => {
    if (!projContrib && !projReturn) return;
    const timer = setTimeout(() => {
      const c = parseFloat(projContrib);
      const r = parseFloat(projReturn);
      const horizon = parseInt(projHorizon);
      const contribG = parseFloat(projContribGrowth);
      if (!isNaN(c) && !isNaN(r)) {
        api.getProjection(
          c, r,
          undefined, // expenses (Planning's FIRE Calc owns this)
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

  // Filter snapshots for change calculation (exact period window)
  const snapshots = useMemo(() => {
    if (period === 'All') return allSnapshots;
    const days = periodToDays(period);
    const cutoff = new Date();
    cutoff.setDate(cutoff.getDate() - days);
    return allSnapshots.filter(s => new Date(s.date) >= cutoff);
  }, [allSnapshots, period]);

  const totalNetWorth = allSnapshots.length > 0 ? allSnapshots[0].total : 0;

  // Liquid net worth: exclude illiquid asset types (real estate, pension)
  const illiquidTypes = new Set(['real_estate', 'pension', 'liability']);
  const liquidNetWorth = accounts.reduce((sum, acc) => {
    if (illiquidTypes.has(acc.type)) return sum;
    return sum + (acc.balance ?? 0);
  }, 0);
  const hasIlliquid = accounts.some(acc => illiquidTypes.has(acc.type));

  // Period change: compare newest vs oldest in filtered range
  const periodStart = snapshots.length > 1 ? snapshots[snapshots.length - 1].total : totalNetWorth;
  const change = totalNetWorth - periodStart;
  const changePct = periodStart !== 0 ? (change / periodStart) * 100 : 0;

  // Daily change: find the most recent snapshot from a previous day (not today)
  const { dailyChange, dailyPrev, dailyChangePct } = useMemo(() => {
    if (allSnapshots.length < 2) return { dailyChange: 0, dailyPrev: totalNetWorth, dailyChangePct: 0 };
    const todayStr = allSnapshots[0].date.slice(0, 10);
    const prev = allSnapshots.find(s => s.date.slice(0, 10) !== todayStr);
    if (!prev) return { dailyChange: 0, dailyPrev: totalNetWorth, dailyChangePct: 0 };
    const dc = totalNetWorth - prev.total;
    const dp = prev.total;
    return { dailyChange: dc, dailyPrev: dp, dailyChangePct: dp !== 0 ? (dc / dp) * 100 : 0 };
  }, [allSnapshots, totalNetWorth]);

  // Chart data: show all data points within the selected period, skip weekends.
  // For 1D, use intraday data points (timestamped every 15 min).
  const chartSnapshots = useMemo(() => {
    if (period === '1D') {
      // Convert intraday points to snapshot-like objects for the chart
      return intradayPoints.map(p => ({
        date: p.recorded_at,
        total: p.total,
        cash_component: p.cash_component,
        investment_component: p.investment_component,
      }));
    }
    const isWeekday = (d: string) => { const day = new Date(d).getDay(); return day !== 0 && day !== 6; };
    const base = period === 'All' ? allSnapshots : (() => {
      const days = periodToDays(period);
      const cutoff = new Date();
      cutoff.setDate(cutoff.getDate() - days);
      return allSnapshots.filter(s => new Date(s.date) >= cutoff);
    })();
    return [...base.filter(s => isWeekday(s.date))].reverse();
  }, [allSnapshots, intradayPoints, period]);

  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  const chartOption = {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const p = params as { seriesName: string; data: number; dataIndex: number; color: string }[];
        if (!Array.isArray(p) || p.length === 0) return '';
        const snap = chartSnapshots[p[0].dataIndex];
        const d = new Date(snap.date);
        const fullDate = period === '1D'
          ? d.toLocaleTimeString('de-DE', { hour: '2-digit', minute: '2-digit' })
          : d.toLocaleDateString('de-DE', { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
        const total = p.reduce((sum, s) => sum + (s.data || 0), 0);
        let html = `<strong>${fullDate}</strong><br/>`;
        html += `Total: ${fmt(total)}<br/>`;
        for (const s of p) {
          html += `<span style="color:${s.color}">${s.seriesName}: ${fmt(s.data)}</span><br/>`;
        }
        return html;
      },
    },
    xAxis: {
      type: 'category' as const,
      data: chartSnapshots.map((s) => formatChartDate(s.date, period)),
      axisLabel: {
        fontSize: 11,
        color: tc.inkMuted,
        rotate: 0,
        interval: chartSnapshots.length > 30
          ? Math.max(Math.floor(chartSnapshots.length / 8) - 1, 1)
          : 'auto',
      },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value' as const,
      axisLabel: {
        formatter: (v: number) => `${(v / 1000).toFixed(0)}k`,
        fontSize: 12,
        color: tc.inkMuted,
      },
      splitLine: { lineStyle: { color: tc.divider } },
    },
    series: [
      {
        name: 'Cash',
        type: 'line' as const,
        stack: 'total',
        areaStyle: { opacity: 0.3 },
        data: chartSnapshots.map((s) => s.cash_component),
        smooth: 0.4,
        showSymbol: false,
        lineStyle: { width: 2 },
      },
      {
        name: 'Investments',
        type: 'line' as const,
        stack: 'total',
        areaStyle: { opacity: 0.3 },
        data: chartSnapshots.map((s) => s.investment_component),
        smooth: 0.4,
        showSymbol: false,
        lineStyle: { width: 2 },
      },
    ],
    grid: { left: 55, right: 16, top: 16, bottom: 36 },
  };

  if (loading) {
    return <div role="status" aria-live="polite" className="flex items-center justify-center py-20 text-[16px] text-ink-muted">Loading<span className="sr-only">, please wait</span>...</div>;
  }

  // Backend-side failure is distinct from "no data yet" — don't render the
  // welcome empty-state because that misleads users into thinking they need
  // to import data when the server is just down.
  if (loadError && allSnapshots.length === 0 && accounts.length === 0) {
    return (
      <div className="space-y-6">
        <h1 className="font-serif text-title text-ink">Net Worth</h1>
        <div role="alert" className="flex items-start gap-2.5 rounded-r-xl bg-inset px-3 py-3 border-l-[3px] border-claret">
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium text-ink">Couldn't load your data</p>
            <p className="text-[12px] text-ink-muted">{loadError}</p>
          </div>
          <button onClick={loadData} className="text-forest text-[13px] font-medium shrink-0">Retry</button>
        </div>
      </div>
    );
  }

  // Empty state: no data imported yet (or accounts exist but no snapshots/transactions)
  if (allSnapshots.length === 0) {
    const hasAccounts = accounts.length > 0;
    return (
      <div className="space-y-6">
        <h1 className="font-serif text-title text-ink">Net Worth</h1>
        <div className="py-16 text-center">
          <svg className="w-16 h-16 mx-auto text-ink-muted/40 mb-4" fill="none" viewBox="0 0 24 24" strokeWidth={1} stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z" />
          </svg>
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">{hasAccounts ? 'Import Your Data' : 'Welcome to Wealth'}</h2>
          <p className="text-[16px] text-ink-muted mb-6 max-w-md mx-auto">
            {hasAccounts
              ? 'Your accounts are set up. Import a CSV file in Settings to start tracking your net worth.'
              : 'Get started by creating an account and importing your first CSV file in Settings.'}
          </p>
          <a href="/settings" className="apple-btn-primary inline-block">Go to Settings</a>
        </div>
      </div>
    );
  }

  // Compute key metrics for Rule of Three
  const investedCapital = savingsRate ? savingsRate.total_deposits - savingsRate.total_withdrawals : 0;
  const allTimeReturn = totalNetWorth - investedCapital;
  const allTimeReturnPct = investedCapital > 0 ? (allTimeReturn / investedCapital) * 100 : 0;
  const cashBalance = accounts.reduce((s, a) => s + (a.cash_balance ?? a.balance ?? 0), 0);

  // When embedded via /planning route, skip hero + tab bar
  const isEmbedded = !!defaultTab;

  return (
    <div className="space-y-6">
      {!isEmbedded && <TabBar tabs={NW_TABS_DASHBOARD} activeTab={activeTab} onTabChange={setActiveTab} />}

      {/* Hero: Net Worth — the focal point (hidden when embedded) */}
      {!isEmbedded && <div className="py-6 md:py-8">
        <p className="font-serif text-[11px] tracking-[0.1em] uppercase text-ink-muted mb-2" style={{ fontVariantCaps: 'small-caps' }}>Total Net Worth</p>
        <p className={`font-serif text-[36px] md:text-[48px] font-normal tracking-tight leading-none transition-colors duration-[250ms] ease-out ${tick === 'up' ? 'text-sage' : tick === 'down' ? 'text-claret' : 'text-ink'}`} style={{ fontVariantNumeric: 'lining-nums tabular-nums' }}>{fmt(totalNetWorth)}</p>
        {hasIlliquid && (
          <p className="text-[13px] text-ink-muted mt-1.5">
            Liquid: <span className="font-semibold text-forest">{fmt(liquidNetWorth)}</span>
          </p>
        )}

        {/* Daily change — subdued, with optional "Why?" */}
        {allSnapshots.length > 1 && (
          <div className="flex items-center gap-2 mt-3">
            <span className={`text-[15px] font-medium tabular-nums ${dailyChange >= 0 ? 'text-sage' : 'text-claret'}`}>
              {dailyChange >= 0 ? '+' : ''}{fmt(dailyChange)}
              {dailyPrev >= 100 && <> ({dailyChangePct >= 0 ? '+' : ''}{dailyChangePct.toFixed(2)}%)</>}
            </span>
            <span className="text-[13px] text-ink-muted">today</span>
            {attribution && (
              <button onClick={() => setShowAttribution(prev => !prev)}
                className="text-[13px] text-forest font-medium hover:underline ml-1 py-2 px-1">
                {showAttribution ? 'Hide' : 'Why?'}
              </button>
            )}
          </div>
        )}
        {showAttribution && attribution && (() => {
          // Reconcile dailyChange (total ΔNW) vs market-only attribution. The
          // remainder is cash flow during the day (deposits, withdrawals,
          // dividends credited, fees). Surface it explicitly when it materially
          // diverges so users don't read the market summary as the whole story.
          const market = attributionTotal ?? 0;
          const other = dailyChange - market;
          const showGap = attributionTotal !== null && Math.abs(other) >= 1;
          return (
            <div className="mt-1.5 pl-0.5 space-y-0.5">
              <p className="text-[12px] text-ink-muted">{attribution}</p>
              {showGap && (
                <p className="text-[11px] text-ink-muted">
                  Market <span className="tabular-nums">{market >= 0 ? '+' : ''}{fmt(market)}</span>
                  {' · '}
                  Cash flow today <span className="tabular-nums">{other >= 0 ? '+' : ''}{fmt(other)}</span>
                </p>
              )}
            </div>
          );
        })()}

        {/* Period change — shown when period is not 1D */}
        {period !== '1D' && snapshots.length > 1 && (
          <p className={`text-[13px] mt-1 tabular-nums ${change >= 0 ? 'text-sage' : 'text-claret'}`}>
            {change >= 0 ? '+' : ''}{fmt(change)}
            {periodStart >= 100 && <> ({changePct >= 0 ? '+' : ''}{changePct.toFixed(2)}%)</>}
            <span className="text-ink-muted ml-1.5">{periodLabel(period)}</span>
          </p>
        )}

        {/* Data freshness — only surface when actually stale (≥2d). Fresh
            snapshots don't need a chrome label; a quiet UI reads more confident
            than one that constantly reassures the user "everything's fine." */}
        {allSnapshots.length > 0 && (() => {
          const snapshotDate = new Date(allSnapshots[0].date);
          const daysOld = Math.max(0, Math.floor((Date.now() - snapshotDate.getTime()) / (1000 * 60 * 60 * 24)));
          if (daysOld < 2) return null;
          const dateStr = snapshotDate.toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' });
          const freshness = daysOld < 10
            ? { label: `${daysOld} days old`, dot: 'bg-amber', text: 'text-amber' }
            : { label: `Stale — ${daysOld}d old (${dateStr})`, dot: 'bg-claret', text: 'text-claret' };
          return (
            <p className={`flex items-center gap-1.5 text-[11px] mt-2 ${freshness.text}`}>
              <span aria-hidden="true" className={`inline-block w-1.5 h-1.5 rounded-full ${freshness.dot}`} />
              {freshness.label}
            </p>
          );
        })()}
      </div>}

      {activeTab === 'overview' && (<>
      {/* Sparkline Chart — clean, minimal, emotional anchor */}
      {chartSnapshots.length > 1 && (
        <div className="-mx-1">
          <EChartWrapper option={{
            tooltip: {
              trigger: 'axis' as const,
              formatter: (params: unknown) => {
                const p = params as { data: number; dataIndex: number }[];
                if (!Array.isArray(p) || p.length === 0) return '';
                const snap = chartSnapshots[p[0].dataIndex];
                const d = new Date(snap.date);
                return `${d.toLocaleDateString('de-DE', { day: 'numeric', month: 'short', year: 'numeric' })}<br/><strong>${fmt(snap.total)}</strong>`;
              },
            },
            xAxis: { type: 'category' as const, show: false, data: chartSnapshots.map(s => s.date) },
            yAxis: { type: 'value' as const, show: false, min: 'dataMin' },
            series: [{
              type: 'line' as const,
              data: chartSnapshots.map(s => s.total),
              smooth: 0.4,
              showSymbol: false,
              lineStyle: { width: 2.5, color: change >= 0 ? tc.sage : tc.claret },
              areaStyle: { color: { type: 'linear', x: 0, y: 0, x2: 0, y2: 1, colorStops: [
                { offset: 0, color: change >= 0 ? tc.sage + '26' : tc.claret + '20' },
                { offset: 1, color: 'rgba(255,255,255,0)' },
              ]}},
            }],
            grid: { left: 0, right: 0, top: 8, bottom: 8 },
          }} height="140px" />
        </div>
      )}

      {/* Period selector */}
      <div className="flex gap-1.5 -mt-4 overflow-x-auto scrollbar-hide" role="group" aria-label="Time period">
        {PERIODS.map((p) => (
          <button
            key={p}
            onClick={() => setPeriod(p)}
            className={`rounded-full px-3 py-2.5 text-[12px] font-medium transition-all duration-150 min-w-[44px] min-h-[44px] md:min-w-0 md:min-h-0 ${
              period === p
                ? 'bg-forest text-white dark:text-parchment-deep'
                : 'text-ink-muted hover:text-ink'
            }`}
          >
            {p}
          </button>
        ))}
      </div>

      {/* Rule of Three */}
      <div className="grid grid-cols-3 gap-px bg-divider">
        <div className="bg-parchment p-3 min-w-0">
          <p className="font-serif text-[11px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>Invested</p>
          <p className="text-[13px] md:text-[18px] font-medium tabular-nums text-ink truncate">{fmt(investedCapital)}</p>
        </div>
        <div className="bg-parchment p-3 min-w-0">
          <p className="font-serif text-[11px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>Return</p>
          <p className={`text-[13px] md:text-[18px] font-medium tabular-nums truncate ${allTimeReturn >= 0 ? 'text-sage' : 'text-claret'}`}>
            {allTimeReturn >= 0 ? '+' : ''}{fmt(allTimeReturn)}
          </p>
          <p className="text-[11px] text-ink-muted">{allTimeReturnPct >= 0 ? '+' : ''}{allTimeReturnPct.toFixed(1)}%</p>
        </div>
        <div className="bg-parchment p-3 min-w-0">
          <p className="font-serif text-[11px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>Cash</p>
          <p className="text-[13px] md:text-[18px] font-medium tabular-nums text-ink truncate">{fmt(cashBalance)}</p>
        </div>
      </div>

      {/* Status bar — compact alerts/system status */}
      {(unreadAlerts > 0 || jobErrors > 0) && (
        <div className="flex items-center gap-3 text-[12px]">
          {unreadAlerts > 0 && (
            <span className="text-amber font-medium">
              {unreadAlerts} unread alert{unreadAlerts !== 1 ? 's' : ''}
            </span>
          )}
          {jobErrors > 0 && (
            <a href="/settings" className="text-claret font-medium hover:underline">
              {jobErrors} job error{jobErrors !== 1 ? 's' : ''}
            </a>
          )}
        </div>
      )}

      <UnvestedPanel mode="summary" />

      {/* Next Actions — top 3 prioritized recommendations */}
      {nextActions.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-3 md:mb-4">
            <h2 className="font-serif text-heading text-ink px-1 md:px-0">Recommended Actions</h2>
            <span className="text-[11px] text-ink-muted tracking-wide">{nextActions.length}</span>
          </div>
          <div className="space-y-2">
            {nextActions.slice(0, 3).map((a, i) => {
              return (
                <a key={i} href={a.link} className="flex items-start gap-3 rounded-[3px] bg-parchment-deep px-4 py-3 hover:bg-divider transition-colors duration-[250ms]">
                  <span className={`mt-1.5 w-1.5 h-1.5 rounded-full shrink-0 ${
                    a.urgency === 'now' ? 'bg-claret' : a.urgency === 'this-month' ? 'bg-amber' : 'bg-forest'
                  }`} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-baseline gap-2">
                      <p className="text-[15px] font-medium text-ink truncate">{a.title}</p>
                      <span className={`text-[11px] font-medium shrink-0 ${
                        a.urgency === 'now' ? 'text-claret' : a.urgency === 'this-month' ? 'text-amber' : 'text-forest'
                      }`}>
                        {a.urgency === 'now' ? 'Act now' : a.urgency === 'this-month' ? 'This month' : 'This quarter'}
                      </span>
                    </div>
                    <p className="text-[13px] text-ink-muted mt-0.5">{a.detail}</p>
                  </div>
                  {a.impact_eur > 0 && (
                    <span className="text-sage text-[13px] font-medium tabular-nums shrink-0 mt-0.5">+{fmt(a.impact_eur)}</span>
                  )}
                </a>
              );
            })}
          </div>
          {nextActions.length > 3 && (
            <a href="/analysis" className="block mt-2 text-center text-[13px] text-forest font-medium hover:text-forest-light">
              View all {nextActions.length} recommendations
            </a>
          )}
        </div>
      )}

      {/* Wealth Attribution — shows where the current net worth came from.
          The waterfall visualizes the sources, the breakdown below it gives
          the exact numbers, and the time series tracks the trajectory. */}
      {wfData && wfData.waterfall && wfData.waterfall.length > 0 && wfData.current_nw > 0 && (() => {
        // Filter out near-zero bars to reduce visual clutter — a 0 EUR row
        // for Interest or Dividends adds no signal but eats x-axis space.
        const bars = wfData.waterfall.filter(w => w.type === 'total' || Math.abs(w.value) >= 1);
        if (bars.length < 2) return null;
        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Wealth Attribution</h2>
            <EChartWrapper option={{
              tooltip: { trigger: 'axis' as const, axisPointer: { type: 'shadow' as const }, formatter: (params: unknown) => {
                const ps = params as { name: string; value: number | number[]; dataIndex: number }[];
                if (!Array.isArray(ps) || ps.length === 0) return '';
                const p = ps[0];
                const bar = bars[p.dataIndex];
                const displayValue = Array.isArray(p.value) ? bar.value : (p.value as number);
                return `${p.name}<br/><strong>${displayValue >= 0 ? '+' : ''}${fmt(displayValue)}</strong>`;
              }},
              grid: { top: 12, right: 10, bottom: 36, left: 56 },
              xAxis: { type: 'category' as const, data: bars.map(w => w.label), axisLabel: { fontSize: 10, interval: 0, rotate: 0, color: tc.inkMuted }, axisLine: { lineStyle: { color: tc.divider } }, axisTick: { show: false } },
              yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, color: tc.inkMuted, formatter: (v: number) => v >= 1000 ? `${Math.round(v/1000)}K` : String(Math.round(v)) }, splitLine: { lineStyle: { color: tc.divider, type: 'dashed' as const } } },
              series: [{
                type: 'bar' as const,
                barWidth: '60%',
                data: (() => {
                  let cumulative = 0;
                  return bars.map(w => {
                    if (w.type === 'total') {
                      return { value: w.value, itemStyle: { color: tc.forest, borderRadius: [2, 2, 0, 0] } };
                    }
                    const start = cumulative;
                    cumulative += w.value;
                    return {
                      value: [start, cumulative],
                      itemStyle: { color: w.value >= 0 ? tc.sage : tc.claret, borderRadius: 2 },
                    };
                  });
                })(),
              }],
            }} height="220px" />

            {/* Breakdown summary — explicit numbers to make the chart readable
                at a glance. Heritage style: serif numbers, sage/claret signs. */}
            <div className="mt-4 grid grid-cols-2 md:grid-cols-3 gap-px bg-divider">
              {bars.filter(w => w.type !== 'total').map(w => (
                <div key={w.label} className="bg-parchment p-3">
                  <p className="font-serif text-[10px] tracking-[0.1em] uppercase text-ink-muted mb-1" style={{ fontVariantCaps: 'small-caps' }}>{w.label}</p>
                  <p className={`font-serif text-[18px] font-medium tabular-nums ${w.value >= 0 ? 'text-sage' : 'text-claret'}`}>
                    {w.value >= 0 ? '+' : ''}{fmt(w.value)}
                  </p>
                  {wfData.current_nw > 0 && (
                    <p className="text-[11px] text-ink-muted tabular-nums">{((w.value / wfData.current_nw) * 100).toFixed(1)}% of NW</p>
                  )}
                </div>
              ))}
            </div>
          </div>
        );
      })()}

      {/* Attribution Over Time — cumulative stacked area showing how each
          source contributed to NW month-over-month. */}
      {wfData && wfData.time_series && wfData.time_series.length > 1 && (() => {
        const hasDiv = wfData.time_series.some(t => Math.abs(t.dividends) >= 1);
        const hasInt = wfData.time_series.some(t => Math.abs(t.interest) >= 1);
        const legendData = ['Contributions', 'Market Returns'];
        if (hasDiv) legendData.push('Dividends');
        if (hasInt) legendData.push('Interest');
        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h3 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Growth Over Time</h3>
            <EChartWrapper option={{
              tooltip: { trigger: 'axis' as const, formatter: (params: unknown) => {
                const ps = params as { name: string; seriesName: string; value: number; marker: string }[];
                if (!Array.isArray(ps) || ps.length === 0) return '';
                const total = ps.reduce((s, p) => s + (p.value || 0), 0);
                const rows = ps.map(p => `${p.marker}${p.seriesName}: <strong>${fmt(p.value)}</strong>`).join('<br/>');
                return `${ps[0].name}<br/>${rows}<br/>━━━<br/>Net Worth: <strong>${fmt(total)}</strong>`;
              }},
              legend: { data: legendData, bottom: 0, textStyle: { fontSize: 11, color: tc.inkBody }, icon: 'circle' as const, itemWidth: 8, itemHeight: 8, itemGap: 12 },
              grid: { top: 12, right: 12, bottom: 36, left: 52 },
              xAxis: { type: 'category' as const, data: wfData.time_series.map(t => { const d = new Date(t.month + '-01'); return d.toLocaleDateString('de-DE', { month: 'short', year: '2-digit' }); }), axisLabel: { fontSize: 10, color: tc.inkMuted, interval: Math.max(Math.floor(wfData.time_series.length / 8) - 1, 0) }, axisLine: { lineStyle: { color: tc.divider } }, axisTick: { show: false } },
              yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, color: tc.inkMuted, formatter: (v: number) => v >= 1000 ? `${Math.round(v/1000)}K` : String(Math.round(v)) }, splitLine: { lineStyle: { color: tc.divider, type: 'dashed' as const } } },
              series: [
                { name: 'Contributions', type: 'line' as const, stack: 'attr', areaStyle: { opacity: 0.45, color: tc.forest }, data: wfData.time_series.map(t => t.contributions), itemStyle: { color: tc.forest }, showSymbol: false, lineStyle: { width: 1, color: tc.forest } },
                { name: 'Market Returns', type: 'line' as const, stack: 'attr', areaStyle: { opacity: 0.45, color: tc.sage }, data: wfData.time_series.map(t => t.market_return), itemStyle: { color: tc.sage }, showSymbol: false, lineStyle: { width: 1, color: tc.sage } },
                ...(hasDiv ? [{ name: 'Dividends', type: 'line' as const, stack: 'attr', areaStyle: { opacity: 0.45, color: tc.gold }, data: wfData.time_series.map(t => t.dividends), itemStyle: { color: tc.gold }, showSymbol: false, lineStyle: { width: 1, color: tc.gold } }] : []),
                ...(hasInt ? [{ name: 'Interest', type: 'line' as const, stack: 'attr', areaStyle: { opacity: 0.45, color: tc.walnut }, data: wfData.time_series.map(t => t.interest), itemStyle: { color: tc.walnut }, showSymbol: false, lineStyle: { width: 1, color: tc.walnut } }] : []),
              ],
            }} height="220px" />
            {wfData.crossover_month && (
              <p className="text-[12px] text-ink-muted mt-2">
                Returns surpassed contributions in <span className="font-medium text-sage">{new Date(wfData.crossover_month + '-01').toLocaleDateString('de-DE', { month: 'long', year: 'numeric' })}</span>
              </p>
            )}
          </div>
        );
      })()}

      {/* Asset Allocation Donut — holdings-aware: brokerage cash is split from
          securities, and securities are binned by their asset_class (etf/stock/
          fund collapse into Equities; bond/commodity/crypto/derivative stand
          alone). Non-brokerage account balances bin by account type. */}
      {accounts.length > 1 && totalNetWorth > 0 && (() => {
        const classLabelByType: Record<string, string> = {
          checking: 'Cash', savings: 'Cash',
          real_estate: 'Real Estate', pension: 'Pension', precious_metals: 'Gold',
          liability: 'Liabilities', credit: 'Credit',
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
          if (acc.type === 'brokerage') {
            // Cash leg of the brokerage — Morgan Stanley etc. typically zero,
            // but Trade Republic / IBKR carry real cash balances.
            add('Cash', acc.cash_balance ?? 0);
            continue;
          }
          add(classLabelByType[acc.type] || 'Other', acc.balance ?? 0);
        }
        // Brokerage holdings — bin by asset_class via the securities table.
        for (const h of holdings) {
          const mv = h.market_value;
          if (mv == null || mv === 0) continue;
          add(assetClassLabels[h.asset_class] || 'Other', mv);
        }
        const entries = Object.entries(byClass).filter(([, v]) => v !== 0).sort(([, a], [, b]) => Math.abs(b) - Math.abs(a));
        if (entries.length < 2) return null;
        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Asset Allocation</h2>
            <EChartWrapper option={{
              tooltip: {
                trigger: 'item' as const,
                formatter: (params: unknown) => {
                  const p = params as { name: string; value: number; percent: number };
                  return `${p.name}<br/>${fmt(p.value)} (${p.percent.toFixed(1)}%)`;
                },
              },
              series: [{
                type: 'pie' as const,
                radius: ['40%', '70%'],
                avoidLabelOverlap: true,
                minAngle: 2,
                data: entries.map(([name, value]) => ({ name, value: Math.round(Math.abs(value)) })),
                label: { fontSize: 12, color: tc.inkBody, formatter: (p: unknown) => {
                  const e = p as { name: string; percent: number };
                  return `${e.name} ${e.percent.toFixed(0)}%`;
                }},
                labelLine: { length: 10, length2: 6 },
                itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
              }],
              // Center label is the sum of the donut bins (not the snapshot
              // total) so the number always reconciles with what the slices
              // show — important now that bins are holdings-aware and can
              // diverge from a lagging net-worth snapshot.
              graphic: [{ type: 'text', left: 'center', top: 'center', style: { text: fmt(entries.reduce((s, [, v]) => s + Math.abs(v), 0)), fontSize: 16, fontWeight: 600, fill: tc.ink } }],
            }} height="260px" />
          </div>
        );
      })()}

      {/* Single-security concentration warning — surfaces any holding whose
          market value exceeds 10% of net worth. Amber at 10–25%, claret beyond. */}
      {holdings.length > 0 && totalNetWorth > 0 && (() => {
        const concentrated = holdings
          .filter(h => h.market_value != null && h.market_value > 0 && (h.market_value / totalNetWorth) > 0.10)
          .map(h => ({
            name: h.name || h.security_isin,
            value: h.market_value as number,
            pct: ((h.market_value as number) / totalNetWorth) * 100,
            severity: ((h.market_value as number) / totalNetWorth) > 0.25 ? 'critical' as const : 'warning' as const,
          }))
          .sort((a, b) => b.pct - a.pct);
        if (concentrated.length === 0) return null;
        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <div className="flex items-center justify-between mb-3 md:mb-4 px-1 md:px-0">
              <h2 className="font-serif text-heading text-ink">Concentration Risk</h2>
              <span className="text-[11px] text-ink-muted">&gt;10% of net worth</span>
            </div>
            <div className="space-y-1.5">
              {concentrated.map((c, i) => (
                <div key={i} className={`flex items-start gap-2.5 rounded-r-xl bg-inset px-3 py-2.5 border-l-[3px] ${
                  c.severity === 'critical' ? 'border-claret' : 'border-amber'
                }`}>
                  <span aria-hidden="true" className={`w-2 h-2 rounded-full shrink-0 mt-1.5 ${
                    c.severity === 'critical' ? 'bg-claret' : 'bg-amber'
                  }`} />
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] font-medium text-ink truncate">
                      <span className={`text-[10px] uppercase tracking-wide mr-1.5 ${c.severity === 'critical' ? 'text-claret' : 'text-amber'}`}>
                        {c.severity === 'critical' ? 'Critical' : 'Warning'}
                      </span>
                      <span data-privacy="blur">{c.name}</span>
                    </p>
                    <p className="text-[12px] text-ink-muted">
                      <span data-privacy="blur" className="tabular-nums">{c.pct.toFixed(1)}%</span> of net worth · <span data-privacy="blur" className="tabular-nums">{fmt(c.value)}</span>
                    </p>
                  </div>
                </div>
              ))}
            </div>
          </div>
        );
      })()}

      {/* Goal Progress */}
      {goals.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:p-5 space-y-3">
          {goals.map(g => (
            <div key={g.id} className="py-2 border-b border-divider last:border-0">
              <div className="flex items-center justify-between mb-1.5">
                <div className="flex items-center gap-2 min-w-0">
                  <span className={`w-2 h-2 rounded-full shrink-0 ${
                    g.status === 'complete' ? 'bg-sage' :
                    g.status === 'ahead' ? 'bg-sage' :
                    g.status === 'on_track' ? 'bg-forest' :
                    'bg-amber'
                  }`} />
                  <span className="text-[15px] font-medium text-ink truncate">{g.name}</span>
                </div>
                <span className="text-[12px] text-ink-muted shrink-0 ml-2">
                  {g.status === 'complete' ? 'Complete' :
                   g.status === 'ahead' ? 'Ahead' :
                   g.status === 'on_track' ? 'On track' :
                   `Behind — projected ${fmt(g.projected_value)}`}
                </span>
              </div>
              {(() => {
                // Clamp the bar to [0, 100] — progress_pct can land outside
                // when the model reports overshoot or pre-funded goals.
                const clampedPct = Math.max(0, Math.min(g.progress_pct, 100));
                // Estimated completion date = today + months_remaining, when
                // we still have a finite trajectory. For complete/ahead goals
                // we surface the user-set target_date instead so the tooltip
                // stays useful.
                let estimateLabel = '';
                if (g.status === 'complete') {
                  estimateLabel = 'Reached';
                } else if (g.months_remaining > 0) {
                  const est = new Date();
                  est.setMonth(est.getMonth() + g.months_remaining);
                  estimateLabel = `Estimated ${est.toLocaleDateString('de-DE', { month: 'short', year: 'numeric' })}`;
                } else if (g.target_date) {
                  estimateLabel = `Target ${new Date(g.target_date).toLocaleDateString('de-DE', { month: 'short', year: 'numeric' })} — past due`;
                }
                return (
                  <div className="flex items-center gap-3" title={estimateLabel}>
                    <div role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(clampedPct)} aria-label={`${g.name} progress: ${clampedPct.toFixed(1)}%${estimateLabel ? `, ${estimateLabel.toLowerCase()}` : ''}`} className="flex-1 h-2 rounded-full bg-parchment-deep overflow-hidden">
                      <div
                        className={`h-full rounded-full transition-all duration-500 ${
                          g.status === 'complete' || g.status === 'ahead' ? 'bg-sage' :
                          g.status === 'on_track' ? 'bg-forest' : 'bg-amber'
                        }`}
                        style={{ width: `${clampedPct}%` }}
                      />
                    </div>
                    <span className="text-[12px] tabular-nums text-ink-muted shrink-0 w-24 text-right">
                      {fmt(g.current_value)} / {fmt(g.target_amount)}
                    </span>
                  </div>
                );
              })()}
              <div className="flex items-center justify-between mt-1">
                <span className="text-[11px] text-ink-muted">
                  {g.progress_pct.toFixed(1)}% — {g.months_remaining > 0 ? `${Math.floor(g.months_remaining / 12)}y ${g.months_remaining % 12}m remaining` : 'past due'}
                </span>
                {g.monthly_contribution > 0 && (
                  <span className="text-[11px] text-ink-muted">
                    {fmt(g.monthly_contribution)}/mo
                  </span>
                )}
              </div>
            </div>
          ))}

          {/* Goal-Asset Mismatch Detection */}
          {(() => {
            const mismatches: { goal: string; severity: 'critical' | 'moderate' | 'info'; message: string }[] = [];

            goals.forEach(g => {
              const fundingAccount = g.funding_account_id ? accounts.find(a => a.id === g.funding_account_id) : null;
              const yearsLeft = g.months_remaining / 12;

              // Critical: short-horizon goal (<3 years) funded by equities
              if (yearsLeft > 0 && yearsLeft < 3 && fundingAccount?.type === 'brokerage') {
                mismatches.push({ goal: g.name, severity: 'critical', message: `Due in ${Math.ceil(yearsLeft)}y but funded by brokerage (equities). A market crash could prevent reaching the target. Consider moving to cash or bonds.` });
              }
              // Moderate: long-horizon goal (>10 years) sitting in cash
              if (yearsLeft > 10 && fundingAccount && (fundingAccount.type === 'checking' || fundingAccount.type === 'savings')) {
                mismatches.push({ goal: g.name, severity: 'moderate', message: `${Math.floor(yearsLeft)}y horizon but funded by ${fundingAccount.type}. Cash returns may not keep pace with inflation. Consider equities for long-term growth.` });
              }
              // Over-funded
              if (g.progress_pct > 110) {
                mismatches.push({ goal: g.name, severity: 'info', message: `${g.progress_pct.toFixed(0)}% funded (${fmt(g.current_value - g.target_amount)} above target). Consider reallocating excess to other goals.` });
              }
              // Shortfall probability: behind schedule and less than half the time remaining
              if (g.status === 'behind' && g.months_remaining > 0 && g.progress_pct < 50 && yearsLeft < 5) {
                const shortfall = g.target_amount - g.projected_value;
                if (shortfall > 0) {
                  mismatches.push({ goal: g.name, severity: 'critical', message: `Projected ${fmt(shortfall)} shortfall. Increase contributions by ${fmt(shortfall / g.months_remaining)}/mo to close the gap.` });
                }
              }
            });

            if (mismatches.length === 0) return null;

            return (
              <div className="mt-3 pt-3 border-t border-divider">
                <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Mismatch Alerts</p>
                <div className="space-y-1.5">
                  {mismatches.map((m, i) => (
                    <div key={i} className={`flex items-start gap-2.5 rounded-xl px-3 py-2 ${
                      m.severity === 'critical' ? 'bg-inset border-l-[3px] border-claret' : m.severity === 'moderate' ? 'bg-inset border-l-[3px] border-amber' : 'bg-inset border-l-[3px] border-sage'
                    }`}>
                      <span className={`w-2 h-2 rounded-full shrink-0 mt-1.5 ${
                        m.severity === 'critical' ? 'bg-claret' : m.severity === 'moderate' ? 'bg-amber' : 'bg-sage'
                      }`} />
                      <div>
                        <p className="text-[13px] font-medium text-ink">{m.goal}</p>
                        <p className="text-[12px] text-ink-muted">{m.message}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            );
          })()}
        </div>
      )}

      {/* Glide Path — recommended allocation per goal */}
      {goals.length > 0 && goals.some(g => g.months_remaining > 0) && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Glide Path</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Recommended equity allocation decreases as each goal approaches its target date.
          </p>

          {(() => {
            // Glide path formula: equity% = min(100, max(0, (years - 2) / 18 * 100))
            const glideEquity = (yearsLeft: number) => Math.min(100, Math.max(0, Math.round((yearsLeft - 2) / 18 * 100)));

            const goalPaths = goals.filter(g => g.months_remaining > 0).map(g => {
              const yearsLeft = g.months_remaining / 12;
              const recommended = glideEquity(yearsLeft);
              const fundingAccount = g.funding_account_id ? accounts.find(a => a.id === g.funding_account_id) : null;
              // Approximate: brokerage ~90% equity, savings 0%, other 50%
              const actual = fundingAccount?.type === 'brokerage' ? 90 : fundingAccount?.type === 'savings' ? 0 : fundingAccount?.type === 'checking' ? 0 : 50;
              const drift = actual - recommended;

              return { name: g.name, yearsLeft, recommended, actual, drift, targetDate: g.target_date };
            });

            // Chart: glide path curve + goal positions
            const chartData: number[][] = [];
            for (let y = 0; y <= 30; y++) {
              chartData.push([y, glideEquity(y)]);
            }

            return (
              <>
                <EChartWrapper option={{
                  tooltip: { trigger: 'axis' as const },
                  xAxis: { type: 'value' as const, name: 'Years to Goal', nameLocation: 'middle' as const, nameGap: 25, min: 0, max: 30, axisLabel: { fontSize: 11, color: tc.inkMuted }, splitLine: { show: false } },
                  yAxis: { type: 'value' as const, name: 'Equity %', min: 0, max: 100, axisLabel: { formatter: (v: number) => `${v}%`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
                  series: [
                    { name: 'Recommended', type: 'line' as const, data: chartData, smooth: 0.4, showSymbol: false, lineStyle: { color: tc.forest, width: 2 }, areaStyle: { opacity: 0.06, color: tc.forest } },
                    { name: 'Your Goals', type: 'scatter' as const, data: goalPaths.map(g => [Math.round(g.yearsLeft * 10) / 10, g.actual]), symbolSize: 12, itemStyle: { color: tc.gold } },
                  ],
                  grid: { left: 50, right: 20, top: 10, bottom: 40 },
                  legend: { data: ['Recommended', 'Your Goals'], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
                }} height="220px" />

                <div className="mt-3 space-y-1 px-1 md:px-0">
                  {goalPaths.map(g => (
                    <div key={g.name} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-1.5">
                      <div className="min-w-0 flex-1">
                        <p className="text-[13px] text-ink truncate">{g.name}</p>
                        <p className="text-[11px] text-ink-muted">{g.yearsLeft.toFixed(1)}y remaining</p>
                      </div>
                      <div className="flex items-center gap-3 shrink-0 ml-3">
                        <div className="text-right">
                          <p className="text-[11px] text-ink-muted">Recommended</p>
                          <p className="text-[13px] tabular-nums text-forest font-medium">{g.recommended}% equity</p>
                        </div>
                        <div className="text-right">
                          <p className="text-[11px] text-ink-muted">Actual</p>
                          <p className={`text-[13px] tabular-nums font-medium ${Math.abs(g.drift) > 20 ? 'text-claret' : Math.abs(g.drift) > 10 ? 'text-amber' : 'text-sage'}`}>{g.actual}% equity</p>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </>
            );
          })()}
        </div>
      )}

      {/* Goal Priority & Shortfall — budget is set on the Planning page
          (Contribution Router); read it from localStorage so the shortfall
          check works without dragging that state into NetWorth. */}
      {goals.length > 1 && (() => {
        const totalNeeded = goals.reduce((s, g) => s + g.monthly_contribution, 0);
        const budget = parseFloat(localStorage.getItem('monthly_invest_budget') || '') || 0;
        const hasShortfall = budget > 0 && totalNeeded > budget;

        if (!hasShortfall && goals.every(g => g.status !== 'behind')) return null;

        // Priority order: lower number = higher priority
        const priorityLabels: Record<number, string> = { 10: 'Emergency', 20: 'Employer Match', 30: 'Debt Payoff', 40: 'Time-Sensitive', 50: 'Long-Term' };
        const sorted = [...goals].sort((a, b) => (a.priority || 50) - (b.priority || 50));

        let remaining = budget;
        const allocations = sorted.map(g => {
          const needed = g.monthly_contribution;
          const allocated = hasShortfall ? Math.min(needed, Math.max(0, remaining)) : needed;
          remaining -= allocated;
          const shortfall = needed - allocated;
          const priorityLabel = priorityLabels[g.priority] || `Priority ${g.priority}`;
          return { ...g, allocated, shortfall, priorityLabel };
        });

        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Goal Priority</h2>
            <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
              {hasShortfall
                ? `Budget shortfall: ${fmt(totalNeeded)}/mo needed but only ${fmt(budget)}/mo available. Contributions allocated by priority.`
                : 'Goals ranked by priority. Adjust priority in Settings to control allocation order.'}
            </p>

            <div className="space-y-1.5 px-1 md:px-0">
              {allocations.map((g, i) => (
                <div key={g.id} className="flex items-center justify-between rounded-xl bg-parchment-deep px-3 py-2">
                  <div className="flex items-center gap-2.5 min-w-0 flex-1">
                    <span className="text-[12px] text-ink-muted w-5 shrink-0">{i + 1}.</span>
                    <div className="min-w-0">
                      <p className="text-[14px] text-ink truncate">{g.name}</p>
                      <p className="text-[11px] text-ink-muted">{g.priorityLabel} · {g.months_remaining > 0 ? `${Math.ceil(g.months_remaining / 12)}y left` : 'past due'}</p>
                    </div>
                  </div>
                  <div className="text-right shrink-0 ml-3">
                    {hasShortfall ? (
                      <>
                        <p className={`text-[13px] tabular-nums font-medium ${g.shortfall > 0 ? 'text-claret' : 'text-sage'}`}>
                          {fmt(g.allocated)}/mo
                        </p>
                        {g.shortfall > 0 && (
                          <p className="text-[11px] text-claret tabular-nums">-{fmt(g.shortfall)} unfunded</p>
                        )}
                      </>
                    ) : (
                      <p className={`text-[13px] tabular-nums font-medium ${g.status === 'behind' ? 'text-amber' : 'text-sage'}`}>
                        {fmt(g.monthly_contribution)}/mo
                      </p>
                    )}
                  </div>
                </div>
              ))}
            </div>

            {hasShortfall && (
              <p className="text-[12px] text-claret mt-3 px-1 md:px-0">
                Increase monthly budget by {fmt(totalNeeded - budget)} to fully fund all goals.
              </p>
            )}
          </div>
        );
      })()}

      </>)}

      {activeTab === 'projection' && (<>
      {/* Wealth Projection */}
      {projData && (projData.history.length > 0 || projData.projection.length > 0) && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Wealth Projection</h2>
          <p className="text-[13px] text-ink-muted mb-4">
            Historical trend + projected growth{projData.target_amount > 0 ? ` toward ${fmt(projData.target_amount)} goal` : ''}
          </p>

          {/* What-if sliders */}
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 mb-4">
            <div className="flex items-center gap-2">
              <label className="text-[12px] text-ink-muted shrink-0 w-28">Monthly</label>
              <input aria-label="Monthly contribution" type="range" min="0" max="5000" step="100" value={projContrib}
                onChange={e => setProjContrib(e.target.value)}
                className="flex-1 accent-forest" />
              <span className="text-[13px] tabular-nums w-16 text-right">{fmt(parseFloat(projContrib) || 0)}</span>
            </div>
            <div className="flex items-center gap-2">
              <label className="text-[12px] text-ink-muted shrink-0 w-28">Return</label>
              <input aria-label="Expected return" type="range" min="0" max="15" step="0.5" value={projReturn}
                onChange={e => setProjReturn(e.target.value)}
                className="flex-1 accent-forest" />
              <span className="text-[13px] tabular-nums w-12 text-right">{projReturn}%</span>
            </div>
            <div className="flex items-center gap-2">
              <label className="text-[12px] text-ink-muted shrink-0 w-28">Horizon</label>
              <input aria-label="Time horizon in years" type="range" min="1" max="50" step="1" value={projHorizon}
                onChange={e => { setProjHorizon(e.target.value); localStorage.setItem('proj_horizon', e.target.value); }}
                className="flex-1 accent-forest" />
              <span className="text-[13px] tabular-nums w-16 text-right">{projHorizon} {parseInt(projHorizon) === 1 ? 'year' : 'years'}</span>
            </div>
            <div className="flex items-center gap-2">
              <label className="text-[12px] text-ink-muted shrink-0 w-28">Contrib growth</label>
              <input aria-label="Annual contribution growth rate" type="range" min="0" max="20" step="0.5" value={projContribGrowth}
                onChange={e => { setProjContribGrowth(e.target.value); localStorage.setItem('proj_contrib_growth', e.target.value); }}
                className="flex-1 accent-forest" />
              <span className="text-[13px] tabular-nums w-12 text-right">{projContribGrowth}%/yr</span>
            </div>
          </div>

          {/* Save / Compare Scenarios */}
          <div className="flex flex-wrap items-center gap-2 mb-4">
            <button
              onClick={() => {
                if (savedScenarios.length >= 3) return;
                const name = `${fmt(parseFloat(projContrib) || 0)}/mo @ ${projReturn}%`;
                const scenario = { name, contribution: parseFloat(projContrib) || 0, return_pct: parseFloat(projReturn) || 7, projection: projData?.projection || [] };
                const updated = [...savedScenarios, scenario];
                setSavedScenarios(updated);
                localStorage.setItem('proj_scenarios', JSON.stringify(updated));
                setActiveScenarios(prev => new Set([...prev, updated.length - 1]));
              }}
              disabled={savedScenarios.length >= 3}
              className="rounded-[8px] bg-parchment-deep text-ink-body px-3 py-1.5 text-[12px] font-medium hover:bg-divider disabled:opacity-40"
            >
              Save Scenario ({savedScenarios.length}/3)
            </button>
            {savedScenarios.map((s, i) => (
              <div key={i} className="flex items-center gap-1">
                <button
                  onClick={() => setActiveScenarios(prev => {
                    const next = new Set(prev);
                    next.has(i) ? next.delete(i) : next.add(i);
                    return next;
                  })}
                  className={`rounded-[8px] px-2 py-1 text-[11px] font-medium transition-colors ${
                    activeScenarios.has(i)
                      ? 'bg-parchment-deep border border-forest text-forest'
                      : 'bg-parchment-deep text-ink-muted'
                  }`}
                >
                  {s.name}
                </button>
                <button
                  onClick={() => {
                    const updated = savedScenarios.filter((_, j) => j !== i);
                    setSavedScenarios(updated);
                    localStorage.setItem('proj_scenarios', JSON.stringify(updated));
                    setActiveScenarios(prev => {
                      const next = new Set<number>();
                      prev.forEach(idx => { if (idx < i) next.add(idx); else if (idx > i) next.add(idx - 1); });
                      return next;
                    });
                  }}
                  className="text-ink-muted hover:text-claret text-[11px]"
                >
                  ×
                </button>
              </div>
            ))}
          </div>

          <EChartWrapper option={{
            tooltip: {
              trigger: 'axis' as const,
              formatter: (params: unknown) => {
                const ps = params as { seriesName: string; data: number; axisValue: string; marker: string }[];
                if (!Array.isArray(ps) || ps.length === 0) return '';
                return `${ps[0].axisValue}<br/>${ps.filter(p => p.data != null).map(p => `${p.marker} ${p.seriesName}: ${fmt(p.data)}`).join('<br/>')}`;
              },
            },
            legend: {
              data: ['Historical', 'Projected', ...(projData.target_amount > 0 ? ['Target'] : []), ...savedScenarios.filter((_, i) => activeScenarios.has(i)).map(s => s.name)],
              bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted },
            },
            xAxis: {
              type: 'category' as const,
              data: [...projData.history.map(h => h.date), ...projData.projection.map(p => p.date)],
              axisLabel: {
                fontSize: 11, color: tc.inkMuted,
                interval: Math.max(Math.floor((projData.history.length + projData.projection.length) / 10) - 1, 1),
              },
              axisLine: { show: false }, axisTick: { show: false }, boundaryGap: false,
            },
            yAxis: {
              type: 'value' as const,
              axisLabel: { formatter: (v: number) => v >= 1000000 ? `${(v/1000000).toFixed(1)}M` : v >= 1000 ? `${(v/1000).toFixed(0)}k` : `${v}`, fontSize: 11, color: tc.inkMuted },
              splitLine: { lineStyle: { color: tc.divider } },
            },
            series: [
              {
                name: 'Historical',
                type: 'line' as const,
                data: [...projData.history.map(h => h.value), ...projData.projection.map(() => null)],
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { color: tc.forest, width: 2 },
                areaStyle: { color: 'rgba(0, 122, 255, 0.08)' },
              },
              {
                name: 'Projected',
                type: 'line' as const,
                data: [...projData.history.map(() => null), ...projData.projection.map(p => p.value)],
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { color: tc.sage, width: 2, type: 'dashed' as const },
                areaStyle: { color: 'rgba(52, 199, 89, 0.06)' },
              },
              ...(projData.has_confidence ? [
                {
                  name: 'P90 (optimistic)',
                  type: 'line' as const,
                  data: [...projData.history.map(() => null), ...projData.projection.map(p => p.p90 ?? null)],
                  smooth: 0.3, showSymbol: false,
                  lineStyle: { width: 0 },
                  areaStyle: { color: 'rgba(52, 199, 89, 0.10)' },
                  stack: 'confidence',
                },
                {
                  name: 'P10 (pessimistic)',
                  type: 'line' as const,
                  data: [...projData.history.map(() => null), ...projData.projection.map(p => {
                    if (p.p10 != null && p.p90 != null) return p.p90 - p.p10;
                    return null;
                  })],
                  smooth: 0.3, showSymbol: false,
                  lineStyle: { width: 0 },
                  areaStyle: { color: 'rgba(52, 199, 89, 0.10)' },
                  stack: 'confidence',
                },
              ] : []),
              ...(projData.target_amount > 0 ? [{
                name: 'Target',
                type: 'line' as const,
                data: [...projData.history.map(() => null), ...projData.projection.map(() => projData.target_amount)],
                showSymbol: false,
                lineStyle: { color: tc.gold, width: 1.5, type: 'dotted' as const },
              }] : []),
              // Saved scenarios overlay
              ...savedScenarios.filter((_, i) => activeScenarios.has(i)).map((s, i) => {
                const colors = [tc.walnut, tc.claret, tc.slate];
                const projDates = projData.projection.map(p => p.date);
                const scenarioMap = new Map(s.projection.map(p => [p.date, p.value]));
                return {
                  name: s.name,
                  type: 'line' as const,
                  data: [...projData.history.map(() => null), ...projDates.map(d => scenarioMap.get(d) ?? null)],
                  smooth: 0.3, showSymbol: false,
                  lineStyle: { color: colors[i % colors.length], width: 1.5, type: 'dashed' as const },
                };
              }),
            ],
            grid: { left: 55, right: 16, top: 16, bottom: 40 },
          }} height="320px" />
        </div>
      )}

      {/* Projection Milestones */}
      {projData?.milestones && projData.milestones.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Projection Milestones</h2>
          <p className="text-[13px] text-ink-muted mb-3">
            Projected net worth at key milestones, assuming {projData.return_pct}% annual return and {fmt(projData.contribution)}/mo contributions.
          </p>
          {/* Mobile: card layout */}
          <div className="md:hidden space-y-2">
            {(showAllMilestones ? projData.milestones : projData.milestones.slice(0, 2)).map(m => (
              <div key={m.year} className="rounded-lg bg-parchment-deep px-3 py-2">
                <div className="flex items-baseline justify-between mb-1">
                  <span className="text-[13px] font-semibold text-ink">{m.year} <span className="text-ink-muted font-normal">+{m.years_from_now}y</span></span>
                  <span className="text-[13px] font-semibold tabular-nums">{fmt(m.projected_value)}</span>
                </div>
                <div className="grid grid-cols-3 gap-x-3 text-[11px] tabular-nums">
                  <div><span className="text-ink-muted">Contributed</span><br/><span>{fmt(m.contributions)}</span></div>
                  <div><span className="text-ink-muted">Growth</span><br/><span className="text-sage font-medium">{fmt(m.growth)}</span></div>
                  <div><span className="text-ink-muted">Real</span><br/><span>{fmt(m.real_value)}</span></div>
                </div>
              </div>
            ))}
          </div>
          {/* Desktop: table */}
          <div className="hidden md:block overflow-x-auto">
            <table className="w-full text-[13px]">
              <thead>
                <tr className="text-ink-muted border-b border-divider">
                  <th className="px-2 py-2 text-left font-medium">Year</th>
                  <th className="px-2 py-2 text-right font-medium">Projected</th>
                  <th className="px-2 py-2 text-right font-medium">Contributed</th>
                  <th className="px-2 py-2 text-right font-medium">Growth</th>
                  <th className="px-2 py-2 text-right font-medium">Real (2% infl.)</th>
                </tr>
              </thead>
              <tbody>
                {(showAllMilestones ? projData.milestones : projData.milestones.slice(0, 2)).map(m => (
                  <tr key={m.year} className="border-b border-divider">
                    <td className="px-2 py-2 tabular-nums">
                      <span className="font-medium">{m.year}</span>
                      <span className="text-ink-muted ml-1">+{m.years_from_now}y</span>
                    </td>
                    <td className="px-2 py-2 text-right tabular-nums font-medium">{fmt(m.projected_value)}</td>
                    <td className="px-2 py-2 text-right tabular-nums text-ink-muted">{fmt(m.contributions)}</td>
                    <td className="px-2 py-2 text-right tabular-nums text-sage font-medium">{fmt(m.growth)}</td>
                    <td className="px-2 py-2 text-right tabular-nums text-ink-muted">{fmt(m.real_value)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {projData.milestones.length > 2 && (
            <button onClick={() => setShowAllMilestones(prev => !prev)}
              className="w-full mt-2 py-1.5 text-[13px] text-forest font-medium">
              {showAllMilestones ? 'Show less' : `Show all ${projData.milestones.length} milestones`}
            </button>
          )}
        </div>
      )}

      {/* Projection Accuracy */}
      {projData?.projection_accuracy && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Projection Accuracy</h2>
          <p className="text-[13px] text-ink-muted mb-3">
            {projData.projection_accuracy.diff_pct >= 0
              ? `You are outperforming your ${projData.projection_accuracy.months_ago}-month-ago projection by ${projData.projection_accuracy.diff_pct}%.`
              : `Your portfolio is ${Math.abs(projData.projection_accuracy.diff_pct)}% below the ${projData.projection_accuracy.months_ago}-month-ago projection.`}
          </p>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Then</p>
              <p className="text-[13px] md:text-[15px] font-semibold tabular-nums">{fmt(projData.projection_accuracy.past_value)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Projected</p>
              <p className="text-[13px] md:text-[15px] font-semibold tabular-nums">{fmt(projData.projection_accuracy.projected_value)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Actual</p>
              <p className="text-[13px] md:text-[15px] font-semibold tabular-nums">{fmt(projData.projection_accuracy.actual_value)}</p>
            </div>
            <div className={`rounded-xl p-3 text-center ${Math.abs(projData.projection_accuracy.diff_pct) <= 5 ? 'bg-inset border-l-[3px] border-sage' : Math.abs(projData.projection_accuracy.diff_pct) <= 15 ? 'bg-inset border-l-[3px] border-amber' : 'bg-inset border-l-[3px] border-claret'}`}>
              <p className="text-[12px] text-ink-muted mb-1">Difference</p>
              <p className={`text-[13px] md:text-[15px] font-semibold tabular-nums ${projData.projection_accuracy.diff_eur >= 0 ? 'text-sage' : 'text-claret'}`}>
                {projData.projection_accuracy.diff_eur >= 0 ? '+' : ''}{fmt(projData.projection_accuracy.diff_eur)}
              </p>
              <p className="text-[11px] text-ink-muted mt-0.5">
                {projData.projection_accuracy.diff_pct >= 0 ? '+' : ''}{projData.projection_accuracy.diff_pct}%
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Sensitivity Analysis */}
      {projData?.sensitivity && projData.sensitivity.length > 0 && projData.projection_end && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Sensitivity Analysis</h2>
          <p className="text-[13px] text-ink-muted mb-3">
            How changing each assumption affects your projected outcome ({fmt(projData.projection_end)} baseline).
          </p>
          <EChartWrapper option={{
            tooltip: {
              trigger: 'axis' as const,
              formatter: (params: unknown) => {
                const ps = params as { seriesName: string; data: number; name: string; marker: string }[];
                if (!Array.isArray(ps)) return '';
                return `${ps[0].name}<br/>${ps.map(p => `${p.marker} ${p.seriesName}: ${fmt(p.data)}`).join('<br/>')}`;
              },
            },
            legend: { data: ['Pessimistic', 'Optimistic'], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
            xAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => v >= 1000000 ? `${(v/1000000).toFixed(1)}M` : v >= 1000 ? `${(v/1000).toFixed(0)}k` : `${v}`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
            yAxis: { type: 'category' as const, data: projData.sensitivity.map(s => s.label), axisLabel: { fontSize: 12, color: tc.inkBody } },
            series: [
              { name: 'Pessimistic', type: 'bar' as const, data: projData.sensitivity.map(s => s.low), itemStyle: { color: tc.claret, borderRadius: [4, 0, 0, 4] }, barMaxWidth: 24 },
              { name: 'Optimistic', type: 'bar' as const, data: projData.sensitivity.map(s => s.high), itemStyle: { color: tc.sage, borderRadius: [0, 4, 4, 0] }, barMaxWidth: 24 },
            ],
            grid: { left: 120, right: 20, top: 10, bottom: 40 },
          }} height="180px" />
        </div>
      )}
      </>)}


      {activeTab === 'accounts' && (<>
      {/* Net Worth Chart — detailed stacked view */}
      {chartSnapshots.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Breakdown Over Time</h2>
          <EChartWrapper option={chartOption} height="280px" />
        </div>
      )}

      {/* Counterparty Exposure */}
      {accounts.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Counterparty Exposure</h2>
          {(() => {
            const institutions: Record<string, { cash: number; securities: number; scheme: string; accounts: string[] }> = {};
            accounts.forEach(a => {
              const inst = a.institution;
              if (!institutions[inst]) {
                institutions[inst] = { cash: 0, securities: 0, scheme: inst === 'sparkasse' ? 'Institutssicherung' : 'Einlagensicherung', accounts: [] };
              }
              institutions[inst].accounts.push(a.name);
              if (a.type === 'checking' || a.type === 'savings') {
                institutions[inst].cash += a.balance ?? 0;
              } else if (a.type === 'brokerage') {
                institutions[inst].securities += a.holdings_value ?? 0;
                institutions[inst].cash += a.cash_balance ?? 0;
              }
            });
            const rows = Object.entries(institutions).sort((a, b) => (b[1].cash + b[1].securities) - (a[1].cash + a[1].securities));
            return (
              <div className="overflow-x-auto px-1 md:px-0">
                <table className="w-full text-[13px] min-w-[500px]">
                  <thead>
                    <tr className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] border-b border-divider">
                      <th className="text-left py-2 font-medium">Institution</th>
                      <th className="text-right py-2 font-medium">Cash Deposits</th>
                      <th className="text-right py-2 font-medium">Securities</th>
                      <th className="text-left py-2 font-medium">Scheme</th>
                      <th className="text-right py-2 font-medium">Status</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map(([inst, data]) => {
                      const pct = data.cash / 100_000 * 100;
                      const status = pct >= 100 ? 'Over limit' : pct >= 80 ? 'Near limit' : 'Covered';
                      const statusColor = pct >= 100 ? 'text-claret' : pct >= 80 ? 'text-amber' : 'text-sage';
                      return (
                        <tr key={inst} className="border-b border-divider">
                          <td className="py-2 text-ink font-medium">{inst.split('_').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ')}</td>
                          <td className="py-2 text-right tabular-nums">{fmt(data.cash)}</td>
                          <td className="py-2 text-right tabular-nums text-ink-muted">{data.securities > 0 ? fmt(data.securities) : '—'}</td>
                          <td className="py-2 text-ink-muted">{data.securities > 0 ? `Sondervermögen + ${data.scheme}` : data.scheme}</td>
                          <td className={`py-2 text-right font-medium ${statusColor}`}>{status}{data.securities > 0 ? ' (cash)' : ''}</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                <p className="text-[11px] text-ink-muted mt-2">Securities held in brokerage accounts are Sondervermögen (segregated assets), protected regardless of institution solvency. Cash deposits covered up to 100.000 EUR per institution.</p>
              </div>
            );
          })()}
        </div>
      )}

      {/* Account Cards grouped by asset class */}
      {accounts.length > 0 && (() => {
        const assetClassLabels: Record<string, string> = {
          brokerage: 'Investments', checking: 'Cash', savings: 'Cash',
          real_estate: 'Real Estate', pension: 'Pension', precious_metals: 'Precious Metals',
          liability: 'Liabilities', credit: 'Credit',
        };
        const groups: Record<string, typeof accounts> = {};
        for (const acc of accounts) {
          const label = assetClassLabels[acc.type] || 'Other';
          (groups[label] ??= []).push(acc);
        }
        const groupOrder = ['Investments', 'Cash', 'Real Estate', 'Pension', 'Precious Metals', 'Liabilities', 'Credit', 'Other'];
        return (
          <div className="space-y-4">
            {groupOrder.filter(g => groups[g]).map(group => (
              <div key={group}>
                <h2 className="font-serif text-heading text-ink mb-3 flex items-baseline gap-2">
                  <span>{group}</span>
                  {Object.keys(groups).length > 1 && (
                    <span className="text-[13px] text-ink-muted font-normal">
                      {fmt(groups[group].reduce((s, a) => s + (a.balance ?? 0), 0))}
                    </span>
                  )}
                </h2>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
                  {(() => {
                    // Compute per-institution cash totals for deposit insurance
                    const instCashTotals: Record<string, number> = {};
                    accounts.forEach(a => {
                      if (a.type === 'checking' || a.type === 'savings') {
                        instCashTotals[a.institution] = (instCashTotals[a.institution] || 0) + (a.balance ?? 0);
                      } else if (a.type === 'brokerage' && a.cash_balance) {
                        instCashTotals[a.institution] = (instCashTotals[a.institution] || 0) + a.cash_balance;
                      }
                    });
                    return groups[group].map((acc) => {
                      const cutoff90 = new Date();
                      cutoff90.setDate(cutoff90.getDate() - 90);
                      const sparkSnaps = allSnapshots.filter(s => new Date(s.date) >= cutoff90).reverse();
                      const sparkline = sparkSnaps.map((s) =>
                        acc.type === 'brokerage' ? s.investment_component : s.cash_component
                      );
                      return <AccountCard key={acc.id} account={acc} sparklineData={sparkline} institutionCashTotal={instCashTotals[acc.institution]} />;
                    });
                  })()}
                </div>
              </div>
            ))}
          </div>
        );
      })()}
      </>)}

    </div>
  );
}
