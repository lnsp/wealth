import { useState, useEffect, useCallback } from 'react';
import { api, type HoldingRow, type PerformanceData, type DividendData, type PerformanceHistoryPoint, type AllocationData, type RebalanceTrade, type Account, type Security, type GoalProgress, type PriceAlertEntry, type NotificationEntry } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import HoldingsTable from '../components/HoldingsTable';
import { TabBar, PeriodSelector, filterByPeriod, formatDateForPeriod } from '../components/ui';
import type { Period } from '../components/ui';
import { useThemeColors } from '../hooks/useThemeColors';
import { useMarketRefresh } from '../hooks/useMarketRefresh';
import UnvestedPanel from '../components/UnvestedPanel';

type PortfolioTab = 'overview' | 'dividends' | 'allocation' | 'holdings' | 'goals';
const PORTFOLIO_TABS: { id: PortfolioTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'dividends', label: 'Dividends' },
  { id: 'allocation', label: 'Allocation' },
  { id: 'holdings', label: 'Holdings' },
  { id: 'goals', label: 'Goals & Alerts' },
];

export default function Portfolio() {
  const tc = useThemeColors();
  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [performance, setPerformance] = useState<PerformanceData | null>(null);
  const [dividends, setDividends] = useState<DividendData | null>(null);
  const [perfHistory, setPerfHistory] = useState<PerformanceHistoryPoint[]>([]);
  const [loading, setLoading] = useState(true);
  const [dividendView, setDividendView] = useState<'monthly' | 'yearly' | 'cumulative'>('monthly');
  const [priceAsOf, setPriceAsOf] = useState<string | null>(null);
  const [allocation, setAllocation] = useState<AllocationData | null>(null);
  const [editingTargets, setEditingTargets] = useState(false);
  const [targetDrafts, setTargetDrafts] = useState<Record<string, string>>({});
  const [rebalanceTrades, setRebalanceTrades] = useState<RebalanceTrade[]>([]);
  const [rebalanceMsg, setRebalanceMsg] = useState('');
  const [depositAmount, setDepositAmount] = useState('');
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [filterAccount, setFilterAccount] = useState<string>('');
  const [healthScore, setHealthScore] = useState<{ score: number; subscores: { name: string; score: number; weight: number; status: string; detail: string }[] } | null>(null);
  const [securityDetail, setSecurityDetail] = useState<Awaited<ReturnType<typeof api.getSecurityDetail>> | null>(null);
  const [tmDate, setTmDate] = useState('');
  const [tmData, setTmData] = useState<{ date: string; holdings: { isin: string; name: string; quantity: number; value: number; weight_pct: number }[]; net_worth: number; current: { net_worth: number }; change: { net_worth_change: number; net_worth_pct: number; contributions: number; market_return: number; dividends: number } } | null>(null);
  const [tmLoading, setTmLoading] = useState(false);
  const [savingsPlans, setSavingsPlans] = useState<Awaited<ReturnType<typeof api.getSavingsPlans>> | null>(null);
  const [allocHistory, setAllocHistory] = useState<{ history: { date: string; weights: Record<string, number> }[]; holdings: string[] } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<PortfolioTab>('overview');
  const [growthPeriod, setGrowthPeriod] = useState<Period>('All');
  const [allocPeriod, setAllocPeriod] = useState<Period>('All');
  const [switchAltISIN, setSwitchAltISIN] = useState('');
  const [switchResult, setSwitchResult] = useState<{ current_name: string; total_invested: number; current_value: number; alternative_value: number; difference_eur: number; difference_pct: number; correlation: number; low_correlation_warning: boolean; unrealized_gain: number; teilfreistellung: number; taxable_gain: number; freibetrag_used: number; tax_on_switch: number; net_proceeds: number; break_even_years: number; is_equity: boolean } | null>(null);
  const [switchLoading, setSwitchLoading] = useState(false);

  // Goals & Alerts state
  const [securities, setSecurities] = useState<Security[]>([]);
  const [goals, setGoals] = useState<GoalProgress[]>([]);
  const [goalName, setGoalName] = useState('');
  const [goalTarget, setGoalTarget] = useState('');
  const [goalDate, setGoalDate] = useState('');
  const [goalContrib, setGoalContrib] = useState('');
  const [goalReturn, setGoalReturn] = useState('7');
  const [alerts, setAlerts] = useState<PriceAlertEntry[]>([]);
  const [notifications, setNotifications] = useState<NotificationEntry[]>([]);
  const [alertType, setAlertType] = useState('price_above');
  const [alertISIN, setAlertISIN] = useState('');
  const [alertThreshold, setAlertThreshold] = useState('');

  const loadPortfolio = useCallback(() => {
    Promise.all([
      api.listHoldings(),
      api.getPerformance(),
      api.getDividends(),
    ])
      .then(([holdRes, perfRes, divRes]) => {
        setHoldings(holdRes.holdings || []);
        setPriceAsOf(holdRes.price_as_of || null);
        setPerformance(perfRes);
        setDividends(divRes);
      })
      .catch(e => setError(e.message || 'Failed to load portfolio data'))
      .finally(() => setLoading(false));
    api.getPerformanceHistory().then(res => setPerfHistory(res.history || [])).catch(() => {});
  }, []);

  useEffect(() => {
    loadPortfolio();
    api.getAllocation().then(setAllocation).catch(() => {});
    api.listAccounts().then(res => setAccounts((res.accounts || []).filter(a => a.type === 'brokerage'))).catch(() => {});
    api.getAllocationHistory().then(setAllocHistory).catch(() => {});
    api.getHealthScore().then(setHealthScore).catch(() => {});
    api.getSavingsPlans().then(setSavingsPlans).catch(() => {});
    api.listSecurities().then(res => setSecurities(res.securities || [])).catch(() => {});
    api.listGoals().then(r => setGoals(r.goals || [])).catch(() => {});
    api.listAlerts().then(r => setAlerts(r.alerts || [])).catch(() => {});
    api.listNotifications().then(r => setNotifications(r.notifications || [])).catch(() => {});
  }, [loadPortfolio]);

  // Auto-refresh during market hours (every 60s)
  useMarketRefresh(loadPortfolio, 60_000);

  // Reload holdings and performance when account filter changes
  useEffect(() => {
    api.listHoldings(filterAccount || undefined).then(res => {
      setHoldings(res.holdings || []);
      setPriceAsOf(res.price_as_of || null);
    }).catch(() => {});
    api.getPerformance(filterAccount || undefined).then(setPerformance).catch(() => {});
  }, [filterAccount]);

  if (loading) {
    return <div className="flex items-center justify-center py-20 text-[16px] text-ink-muted">Loading...</div>;
  }

  if (error) {
    return (
      <div className="flex flex-col items-center justify-center py-20 gap-3">
        <svg className="w-10 h-10 text-claret" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
          <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
        </svg>
        <p className="text-[16px] text-claret">{error}</p>
        <button onClick={() => { setError(null); loadPortfolio(); }} className="text-forest text-[15px] font-medium">Retry</button>
      </div>
    );
  }

  // hasMarketPrices = ANY holding has a live price. fullyPriced = ALL do.
  // market_value and unrealized_pl are EUR-converted server-side
  // (HandleHoldings); avg_cost_basis is still per-share in the security's
  // native currency. To get an EUR-clean cost basis without doing FX on the
  // client, derive it from market_value - unrealized_pl for priced holdings.
  // Unpriced holdings fall back to qty × avg_cost_basis (native) — surfaced
  // via the "Value (partial market)" label so the user knows the total is
  // a hybrid.
  const hasMarketPrices = holdings.some((h) => h.market_value != null);
  const fullyPriced = holdings.length > 0 && holdings.every((h) => h.market_value != null);
  const pricedCount = holdings.filter((h) => h.market_value != null).length;
  const totalMarketValue = hasMarketPrices
    ? holdings.reduce((sum, h) => sum + (h.market_value ?? h.quantity * h.avg_cost_basis), 0)
    : holdings.reduce((sum, h) => sum + h.quantity * h.avg_cost_basis, 0);
  const totalUnrealizedPL = holdings.reduce((sum, h) => sum + (h.unrealized_pl ?? 0), 0);
  // costBasis = marketValue - PL for priced rows, falling back to qty × cost
  // for unpriced ones (those rows contribute 0 to unrealized_pl and their
  // market_value fallback equals their cost basis anyway, so the math
  // remains coherent).
  const totalCostBasis = totalMarketValue - totalUnrealizedPL;
  const totalPL = totalUnrealizedPL;
  const totalPLPct = totalCostBasis !== 0 ? (totalPL / totalCostBasis) * 100 : 0;
  const totalDividends = holdings.reduce((sum, h) => sum + h.total_dividends, 0);
  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  const netCash = performance ? performance.total_invested - performance.total_withdrawn : 0;

  const hasMonthlyDividends = dividends && dividends.monthly && dividends.monthly.length > 0;
  const hasYearlyDividends = dividends && dividends.yearly && dividends.yearly.length > 0;
  const hasDividendData = dividendView === 'yearly' ? hasYearlyDividends : hasMonthlyDividends;

  const dividendChartData = dividendView === 'yearly'
    ? { labels: dividends?.yearly?.map(d => d.year) ?? [], values: dividends?.yearly?.map(d => Math.round(d.amount * 100) / 100) ?? [] }
    : { labels: dividends?.monthly?.map(d => d.month) ?? [], values: dividends?.monthly?.map(d => Math.round(d.amount * 100) / 100) ?? [] };

  const dividendChartOption = hasDividendData ? {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const p = params as { data: number; name: string }[];
        if (Array.isArray(p) && p.length > 0) {
          return `${p[0].name}<br/>Dividends: ${fmt(p[0].data)}`;
        }
        return '';
      },
    },
    xAxis: {
      type: 'category' as const,
      data: dividendChartData.labels,
      axisLabel: { rotate: dividendView === 'monthly' ? 45 : 0, fontSize: 11, color: tc.inkMuted },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value' as const,
      axisLabel: {
        formatter: (v: number) => v >= 1000 ? `${(v / 1000).toFixed(0)}k` : `${v.toFixed(0)}`,
        fontSize: 12,
        color: tc.inkMuted,
      },
      splitLine: { lineStyle: { color: tc.divider } },
    },
    series: [{
      type: 'bar' as const,
      data: dividendChartData.values,
      itemStyle: { borderRadius: [4, 4, 0, 0], color: tc.sage },
      barMaxWidth: dividendView === 'yearly' ? 50 : 30,
    }],
    grid: { left: 50, right: 16, top: 16, bottom: dividendView === 'monthly' ? 60 : 30 },
  } : null;

  return (
    <div className="space-y-6">
      <TabBar tabs={PORTFOLIO_TABS} activeTab={activeTab} onTabChange={setActiveTab} />

      {activeTab === 'overview' && (<>
      {/* Price freshness + account filter */}
      <div className="flex items-center justify-between">
        {priceAsOf ? (
          <p className="text-[12px] text-ink-muted">
            Prices as of {new Date(priceAsOf).toLocaleDateString('de-DE', { day: '2-digit', month: '2-digit', year: 'numeric' })}
            {(() => {
              const days = Math.floor((Date.now() - new Date(priceAsOf).getTime()) / (1000 * 60 * 60 * 24));
              if (days > 3) return <span className="text-amber ml-1">(stale)</span>;
              return null;
            })()}
          </p>
        ) : <div />}
        {accounts.length > 1 && (
          <select
            value={filterAccount}
            onChange={e => setFilterAccount(e.target.value)}
            className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-1.5 text-[15px]"
          >
            <option value="">All Accounts</option>
            {accounts.map(a => <option key={a.id} value={a.id}>{a.name}</option>)}
          </select>
        )}
      </div>

      {/* KPI row */}
      <div className="grid grid-cols-2 gap-2 md:gap-3 md:grid-cols-4">
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <p className="text-[12px] md:text-[13px] text-ink-muted">
            {fullyPriced ? 'Market Value' : hasMarketPrices ? 'Value (partial market)' : 'Value (Cost)'}
          </p>
          <p className="font-serif text-[20px] md:text-[22px] text-ink mt-1 tabular-nums">{fmt(totalMarketValue)}</p>
          {hasMarketPrices && (
            <p className="text-[11px] md:text-[12px] text-ink-muted mt-0.5 tabular-nums">
              Cost: {fmt(totalCostBasis)}
              {!fullyPriced && holdings.length > 0 && (
                <span className="text-amber"> · {pricedCount}/{holdings.length} priced</span>
              )}
            </p>
          )}
        </div>
        {hasMarketPrices && (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <p className="text-[12px] md:text-[13px] text-ink-muted">P&L</p>
            <p className={`font-serif text-[20px] md:text-[22px] mt-1 tabular-nums ${totalPL >= 0 ? 'text-sage' : 'text-claret'}`}>
              {totalPL >= 0 ? '+' : ''}{fmt(totalPL)}
            </p>
            <p className={`text-[11px] md:text-[12px] mt-0.5 tabular-nums ${totalPL >= 0 ? 'text-sage' : 'text-claret'}`}>
              {totalPLPct >= 0 ? '+' : ''}{totalPLPct.toFixed(2)}%
            </p>
          </div>
        )}
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <p className="text-[12px] md:text-[13px] text-ink-muted">Positions</p>
          <p className="font-serif text-[20px] md:text-[22px] text-ink mt-1">{holdings.length}</p>
        </div>
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <p className="text-[12px] md:text-[13px] text-ink-muted">Dividends</p>
          <p className="font-serif text-[20px] md:text-[22px] text-sage mt-1 tabular-nums">{fmt(totalDividends)}</p>
        </div>
      </div>

      {/* Health Score */}
      {healthScore && healthScore.score > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center gap-4 px-1 md:px-0">
            <div className={`w-16 h-16 rounded-full flex items-center justify-center text-white dark:text-parchment-deep text-xl font-bold shrink-0 ${
              healthScore.score >= 80 ? 'bg-sage' : healthScore.score >= 60 ? 'bg-amber' : 'bg-claret'
            }`}>
              {healthScore.score}
            </div>
            <div className="flex-1 min-w-0">
              <h2 className="font-serif text-heading text-ink mb-1">Portfolio Health</h2>
              <div className="flex flex-wrap gap-x-4 gap-y-1 mt-1">
                {healthScore.subscores.map(s => (
                  <span key={s.name} className="text-[12px]">
                    <span className={`font-medium ${s.status === 'good' ? 'text-sage' : s.status === 'fair' ? 'text-amber' : 'text-claret'}`}>
                      {s.score}
                    </span>
                    <span className="text-ink-muted ml-0.5">{s.name}</span>
                  </span>
                ))}
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Performance summary */}
      {performance && performance.total_invested > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4">Performance</h2>
          <div className="grid grid-cols-2 md:grid-cols-3 gap-x-4 md:gap-x-8 gap-y-3">
            <div>
              <p className="text-[11px] md:text-[12px] text-ink-muted" title="Total deposits minus withdrawals across all accounts">Net Invested</p>
              <p className="text-[13px] md:text-[15px] font-medium text-ink tabular-nums mt-0.5">{fmt(netCash)}</p>
            </div>
            <div>
              <p className="text-[11px] md:text-[12px] text-ink-muted">Total Gains</p>
              <p className={`text-[13px] md:text-[15px] font-medium tabular-nums mt-0.5 ${performance.total_return >= 0 ? 'text-sage' : 'text-claret'}`}>
                {performance.total_return >= 0 ? '+' : ''}{fmt(performance.total_return)}
              </p>
              <p className={`text-[11px] tabular-nums ${performance.total_return >= 0 ? 'text-sage' : 'text-claret'}`}>
                {performance.total_return_pct >= 0 ? '+' : ''}{performance.total_return_pct.toFixed(2)}%
              </p>
            </div>
            <div>
              <p className="text-[11px] md:text-[12px] text-ink-muted">TWR</p>
              <p className={`text-[13px] md:text-[15px] font-semibold tabular-nums mt-0.5 ${performance.twr >= 0 ? 'text-sage' : 'text-claret'}`}>
                {performance.twr >= 0 ? '+' : ''}{performance.twr.toFixed(2)}%
              </p>
            </div>
            <div>
              <p className="text-[11px] md:text-[12px] text-ink-muted">IRR</p>
              <p className={`text-[13px] md:text-[15px] font-semibold tabular-nums mt-0.5 ${performance.irr >= 0 ? 'text-sage' : 'text-claret'}`}>
                {performance.irr >= 0 ? '+' : ''}{performance.irr.toFixed(2)}%
              </p>
            </div>
            <div>
              <p className="text-[11px] md:text-[12px] text-ink-muted">Realized</p>
              <p className={`text-[13px] md:text-[15px] font-medium tabular-nums mt-0.5 ${performance.realized_pl >= 0 ? 'text-sage' : 'text-claret'}`}>
                {performance.realized_pl >= 0 ? '+' : ''}{fmt(performance.realized_pl)}
              </p>
            </div>
            <div>
              <p className="text-[11px] md:text-[12px] text-ink-muted">Unrealized <span className="text-[11px] text-ink-muted">(avg)</span></p>
              <p className={`text-[13px] md:text-[15px] font-medium tabular-nums mt-0.5 ${performance.unrealized_pl >= 0 ? 'text-sage' : 'text-claret'}`}>
                {performance.unrealized_pl >= 0 ? '+' : ''}{fmt(performance.unrealized_pl)}
              </p>
            </div>
          </div>
        </div>
      )}

      {/* Performance History Chart */}
      {perfHistory.length > 1 && (() => {
        const filtered = filterByPeriod(perfHistory, growthPeriod);
        return (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex flex-col md:flex-row md:items-start md:justify-between mb-3 md:mb-4 px-1 md:px-0 gap-3">
            <div>
              <h2 className="font-serif text-heading text-ink">Growth Over Time</h2>
              <p className="text-[11px] md:text-[12px] text-ink-muted mt-1">Capital invested stacks cash deposits and RSU vests, valued at fair-market value on the vest date.</p>
            </div>
            <PeriodSelector value={growthPeriod} onChange={setGrowthPeriod} />
          </div>
          <EChartWrapper option={{
            tooltip: {
              trigger: 'axis' as const,
              formatter: (params: unknown) => {
                const p = params as { seriesName: string; data: number; axisValueLabel: string; color: string }[];
                if (!Array.isArray(p) || p.length === 0) return '';
                let html = `<strong>${p[0].axisValueLabel}</strong>`;
                for (const s of p) {
                  const isPct = s.seriesName === 'Return' || s.seriesName === 'MSCI World';
                  const color = isPct ? s.color
                    : s.seriesName === 'Cash' ? tc.inkMuted
                    : s.seriesName === 'In-kind' ? tc.slate
                    : tc.forest;
                  html += `<br/><span style="color:${color}">${s.seriesName}: ${
                    isPct ? `${s.data >= 0 ? '+' : ''}${s.data.toFixed(1)}%`
                    : new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(s.data)
                  }</span>`;
                }
                return html;
              },
            },
            legend: { data: ['Portfolio Value', 'Cash', 'In-kind', 'Return', ...(filtered.some(p => p.benchmark_pct != null) ? ['MSCI World'] : [])], bottom: 0, itemGap: 12, textStyle: { fontSize: 10, color: tc.inkMuted } },
            xAxis: {
              type: 'category' as const,
              data: filtered.map(p => formatDateForPeriod(p.date, growthPeriod)),
              axisLabel: { fontSize: 11, color: tc.inkMuted, rotate: 45 },
              axisLine: { show: false },
              axisTick: { show: false },
            },
            yAxis: [
              {
                type: 'value' as const,
                axisLabel: { formatter: (v: number) => `${(v / 1000).toFixed(0)}k`, fontSize: 12, color: tc.inkMuted },
                splitLine: { lineStyle: { color: tc.divider } },
              },
              {
                type: 'value' as const,
                axisLabel: { formatter: (v: number) => `${v.toFixed(0)}%`, fontSize: 12, color: tc.inkMuted },
                splitLine: { show: false },
              },
            ],
            series: [
              {
                name: 'Portfolio Value',
                type: 'line' as const,
                data: filtered.map(p => Math.round(p.portfolio_value)),
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { width: 2.5, color: tc.forest },
                areaStyle: { opacity: 0.08, color: tc.forest },
              },
              {
                name: 'Cash',
                type: 'line' as const,
                stack: 'invested',
                data: filtered.map(p => Math.round(p.cash_invested)),
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { width: 1.5, color: tc.inkMuted, type: 'dashed' as const },
                areaStyle: { opacity: 0.05, color: tc.inkMuted },
              },
              {
                name: 'In-kind',
                type: 'line' as const,
                stack: 'invested',
                data: filtered.map(p => Math.round(p.in_kind_invested)),
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { width: 1.5, color: tc.slate, type: 'dashed' as const },
                areaStyle: { opacity: 0.1, color: tc.slate },
              },
              {
                name: 'Return',
                type: 'line' as const,
                yAxisIndex: 1,
                data: filtered.map(p => Math.round(p.return_pct * 10) / 10),
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { width: 2, color: tc.sage },
              },
              ...(filtered.some(p => p.benchmark_pct != null) ? [{
                name: 'MSCI World',
                type: 'line' as const,
                yAxisIndex: 1,
                data: filtered.map(p => p.benchmark_pct != null ? Math.round(p.benchmark_pct * 10) / 10 : null),
                smooth: 0.3,
                showSymbol: false,
                lineStyle: { width: 1.5, color: tc.walnut, type: 'dotted' as const },
              }] : []),
            ],
            grid: { left: 55, right: 45, top: 10, bottom: 55 },
          }} height="300px" />
        </div>
        );
      })()}

      </>)}

      {activeTab === 'dividends' && (<>
      {/* Dividend Dashboard */}
      {dividends && dividends.total > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Dividend Income</h2>

          {/* Dividend KPIs */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-2 md:gap-3 mb-4 px-1 md:px-0">
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Total Received</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-sage">{fmt(dividends.total)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Trailing 12 Months</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-sage">{fmt(dividends.trailing_12m)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Yield on Cost</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{dividends.yield_on_cost.toFixed(2)}%</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Growth YoY</p>
              <p className={`font-serif text-[20px] font-semibold tabular-nums ${dividends.dividend_growth >= 0 ? 'text-sage' : 'text-claret'}`}>
                {Math.abs(dividends.dividend_growth) > 500
                  ? 'N/A'
                  : `${dividends.dividend_growth >= 0 ? '+' : ''}${dividends.dividend_growth.toFixed(1)}%`}
              </p>
              {Math.abs(dividends.dividend_growth) > 500 && (
                <p className="text-[11px] text-ink-muted mt-0.5">insufficient history</p>
              )}
            </div>
          </div>

          {/* View toggle */}
          <div className="flex gap-1.5 mb-3 px-1 md:px-0" role="group" aria-label="Dividend view">
            {(['monthly', 'yearly', 'cumulative'] as const).map((view) => (
              <button
                key={view}
                onClick={() => setDividendView(view as 'monthly' | 'yearly')}
                className={`rounded-[8px] px-3 py-2.5 text-[12px] font-medium transition-all duration-150 capitalize ${
                  dividendView === view
                    ? 'bg-forest text-white dark:text-parchment-deep'
                    : 'bg-parchment-deep text-ink-body hover:bg-divider active:bg-divider'
                }`}
              >
                {view}
              </button>
            ))}
          </div>

          {/* Charts */}
          {dividendView === 'cumulative' && dividends.cumulative && dividends.cumulative.length > 0 ? (
            <EChartWrapper option={{
              tooltip: {
                trigger: 'axis' as const,
                formatter: (params: unknown) => {
                  const ps = params as { seriesName: string; data: number; axisValue: string; marker: string }[];
                  if (!Array.isArray(ps) || ps.length === 0) return '';
                  return `${ps[0].axisValue}<br/>${ps.map(p => `${p.marker} ${p.seriesName}: ${fmt(p.data)}`).join('<br/>')}`;
                },
              },
              legend: { data: ['Monthly', 'Cumulative'], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
              xAxis: {
                type: 'category' as const,
                data: dividends.cumulative.map(c => c.month),
                axisLabel: { fontSize: 11, color: tc.inkMuted, interval: Math.max(Math.floor(dividends.cumulative.length / 8) - 1, 0) },
                axisLine: { show: false }, axisTick: { show: false },
              },
              yAxis: [
                { type: 'value' as const, axisLabel: { formatter: (v: number) => `${v}`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
                { type: 'value' as const, axisLabel: { formatter: (v: number) => `${v}`, fontSize: 11, color: tc.inkMuted }, splitLine: { show: false } },
              ],
              series: [
                { name: 'Monthly', type: 'bar' as const, data: dividends.cumulative.map(c => Math.round(c.amount * 100) / 100), itemStyle: { color: tc.sage, borderRadius: [3, 3, 0, 0] }, barMaxWidth: 20 },
                { name: 'Cumulative', type: 'line' as const, yAxisIndex: 1, data: dividends.cumulative.map(c => c.cumulative), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.forest, width: 2 } },
              ],
              grid: { left: 45, right: 45, top: 10, bottom: 40 },
            }} height="250px" />
          ) : dividendChartOption ? (
            <EChartWrapper option={dividendChartOption} height="220px" />
          ) : null}

          {/* By Security breakdown */}
          {dividends.by_security && dividends.by_security.length > 0 && (
            <div className="mt-3 md:mt-4 pt-3 md:pt-4 border-t border-divider px-1 md:px-0">
              <p className="text-[12px] text-ink-muted mb-2">By Security</p>
              <div className="space-y-1.5">
                {dividends.by_security.map((s) => (
                  <div key={s.isin} className="flex items-center justify-between gap-2">
                    <p className="text-[13px] md:text-[15px] text-ink truncate">{s.name}</p>
                    <p className="text-[13px] md:text-[15px] text-sage tabular-nums font-medium shrink-0">{fmt(s.amount)}</p>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Dividend Calendar */}
          {dividends.calendar && dividends.calendar.length > 0 && (
            <div className="mt-3 md:mt-4 pt-3 md:pt-4 border-t border-divider px-1 md:px-0">
              <p className="text-[12px] text-ink-muted mb-2">Expected Upcoming Dividends</p>
              <div className="space-y-1.5">
                {(() => {
                  // Group by month
                  const byMonth: Record<string, { name: string; expected: number }[]> = {};
                  dividends.calendar.forEach((c: { month: string; name: string; expected: number }) => {
                    (byMonth[c.month] ??= []).push({ name: c.name, expected: c.expected });
                  });
                  return Object.entries(byMonth).slice(0, 6).map(([month, entries]) => (
                    <div key={month} className="rounded-lg bg-inset border-l-[3px] border-sage px-3 py-2">
                      <p className="text-[12px] font-medium text-ink mb-1">{month}</p>
                      {entries.map((e, i) => (
                        <div key={i} className="flex items-center justify-between">
                          <span className="text-[12px] text-ink-muted truncate">{e.name}</span>
                          <span className="text-[12px] text-sage font-medium tabular-nums shrink-0 ml-2">~{fmt(e.expected)}</span>
                        </div>
                      ))}
                    </div>
                  ));
                })()}
              </div>
            </div>
          )}
        </div>
      )}

      </>)}

      {activeTab === 'allocation' && (<>
      {/* Target Allocation */}
      {allocation && allocation.allocations.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-3 md:mb-4 px-1 md:px-0">
            <div>
              <h2 className="font-serif text-heading text-ink">Target Allocation</h2>
              {allocation.has_targets && (
                <p className="text-[13px] text-ink-muted mt-0.5">
                  Max drift: <span className={Math.abs(allocation.max_drift) > 5 ? 'text-claret font-medium' : Math.abs(allocation.max_drift) > 2 ? 'text-amber font-medium' : 'text-sage font-medium'}>
                    {allocation.max_drift > 0 ? '+' : ''}{allocation.max_drift}%
                  </span>
                </p>
              )}
            </div>
            <button
              onClick={() => {
                if (editingTargets) {
                  // Save
                  const allocs = Object.entries(targetDrafts)
                    .map(([isin, val]) => ({ isin, target_pct: parseFloat(val) || 0 }));
                  api.setAllocation(allocs).then(() => {
                    setEditingTargets(false);
                    api.getAllocation().then(setAllocation).catch(() => {});
                  }).catch(console.error);
                } else {
                  // Start editing
                  const drafts: Record<string, string> = {};
                  for (const a of allocation.allocations) {
                    drafts[a.isin] = String(a.target_pct);
                  }
                  setTargetDrafts(drafts);
                  setEditingTargets(true);
                }
              }}
              className={`rounded-lg px-3 py-1.5 text-[13px] font-medium transition-colors ${
                editingTargets
                  ? 'bg-sage text-white dark:text-parchment-deep'
                  : 'bg-parchment-deep text-ink-body hover:bg-divider'
              }`}
            >
              {editingTargets ? 'Save' : 'Edit Targets'}
            </button>
          </div>

          {/* Target vs Actual bars */}
          <div className="space-y-3">
            {allocation.allocations.map(a => (
              <div key={a.isin} className="px-1 md:px-0">
                <div className="flex items-center justify-between mb-1">
                  <span className="text-[15px] font-medium text-ink truncate mr-2">{a.name}</span>
                  <div className="flex items-center gap-2 shrink-0">
                    {editingTargets ? (
                      <div className="flex items-center gap-1">
                        <span className="text-[12px] text-ink-muted">Target:</span>
                        <input
                          type="number"
                          min="0"
                          max="100"
                          step="0.5"
                          value={targetDrafts[a.isin] ?? '0'}
                          onChange={e => setTargetDrafts(d => ({ ...d, [a.isin]: e.target.value }))}
                          className="w-16 text-right text-[13px] font-medium tabular-nums rounded border border-divider px-1.5 py-0.5 bg-parchment text-ink"
                        />
                        <span className="text-[12px] text-ink-muted">%</span>
                      </div>
                    ) : (
                      <>
                        <span className="text-[13px] tabular-nums text-ink-body">
                          {a.actual_pct}%{a.target_pct > 0 ? ` / ${a.target_pct}%` : ''}
                        </span>
                        {a.target_pct > 0 && (
                          // Drift badge: bg-{color}/20 alpha modifiers don't
                          // render against this codebase's var-backed Tailwind
                          // colors, so the tint was invisible. Use a parchment
                          // surface with a coloured outline + matching text
                          // colour — clearly tinted in both modes.
                          <span className={`text-[12px] font-semibold tabular-nums px-2 py-0.5 rounded bg-parchment-deep border ${
                            a.status === 'on_target' ? 'border-sage text-sage' :
                            a.status === 'overweight' ? 'border-claret text-claret' :
                            'border-amber text-amber'
                          }`}>
                            {a.drift_pct > 0 ? '+' : ''}{a.drift_pct}%
                          </span>
                        )}
                      </>
                    )}
                  </div>
                </div>
                {/* Single bar with target tick. The previous two-tone bar
                    rendered an amber "gap" segment at opacity 0.3 that was
                    nearly invisible against parchment (below WCAG AA at the
                    14px bar height it was the only signal of under-allocation).
                    Now: bar shows actual%, a 2px vertical claret tick marks
                    target%. Bar colour reflects status — forest when within
                    tolerance, claret when overweight, walnut-ish forest when
                    underweight (just shorter than the tick). */}
                <svg className="w-full mt-1.5" height="14" role="img" aria-label={`${a.name}: ${a.actual_pct}% actual${a.target_pct > 0 ? `, ${a.target_pct}% target` : ''}`}>
                  <defs>
                    <clipPath id={`bar-clip-${a.isin}`}>
                      <rect x="0" y="0" width="100%" height="14" rx="7" />
                    </clipPath>
                  </defs>
                  {/* Track */}
                  <rect x="0" y="0" width="100%" height="14" rx="7" fill="var(--color-divider)" opacity="0.5" />
                  <g clipPath={`url(#bar-clip-${a.isin})`}>
                    <rect
                      x="0" y="0"
                      width={`${Math.min(a.actual_pct, 100)}%`}
                      height="14"
                      fill={a.status === 'overweight' ? 'var(--color-claret)' : 'var(--color-forest)'}
                      opacity="0.75"
                    />
                  </g>
                  {a.target_pct > 0 && a.target_pct <= 100 && (
                    // Target tick: 2px vertical claret bar, ink ring around it so
                    // it stays visible whether the underlying bar segment is forest,
                    // claret, or empty parchment track.
                    <>
                      <rect x={`calc(${a.target_pct}% - 2px)`} y="-1" width="4" height="16" fill="var(--color-parchment)" />
                      <rect x={`calc(${a.target_pct}% - 1px)`} y="-1" width="2" height="16" fill="var(--color-ink)" />
                    </>
                  )}
                </svg>
              </div>
            ))}
          </div>

          {!allocation.has_targets && !editingTargets && (
            <p className="text-[13px] text-ink-muted mt-3 px-1 md:px-0">
              No target allocation set. Click "Edit Targets" to define your desired portfolio weights.
            </p>
          )}
        </div>
      )}

      {/* Rebalancing Suggestions */}
      {allocation && allocation.has_targets && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Rebalancing</h2>

          <div className="flex flex-col sm:flex-row gap-2 mb-3 px-1 md:px-0">
            <div className="flex gap-2 flex-1">
              <input
                type="number"
                placeholder="Deposit amount (EUR)"
                value={depositAmount}
                onChange={e => setDepositAmount(e.target.value)}
                className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] tabular-nums"
              />
              <button
                onClick={() => {
                  const dep = parseFloat(depositAmount) || 0;
                  api.getRebalance(dep > 0 ? dep : undefined).then(r => {
                    setRebalanceTrades(r.trades || []);
                    setRebalanceMsg(r.message);
                  }).catch(console.error);
                }}
                className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2 text-[16px] font-medium whitespace-nowrap"
              >
                {depositAmount ? 'Allocate Deposit' : 'Compute Trades'}
              </button>
            </div>
          </div>

          {rebalanceMsg && (
            <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">{rebalanceMsg}</p>
          )}

          {rebalanceTrades.length > 0 && (
            <div className="space-y-2 px-1 md:px-0">
              {rebalanceTrades.map(t => (
                <div key={t.isin} className={`flex items-center justify-between rounded-xl px-3 py-2.5 ${
                  t.action === 'buy' ? 'bg-inset border-l-[3px] border-sage' : 'bg-inset border-l-[3px] border-claret'
                }`}>
                  <div className="min-w-0 flex-1">
                    <p className="text-[15px] font-medium text-ink">{t.name}</p>
                    <p className="text-[12px] text-ink-muted">
                      {t.current_pct}% → {t.target_pct}%
                    </p>
                  </div>
                  <div className="text-right shrink-0 ml-3">
                    <p className={`text-[15px] font-semibold tabular-nums ${
                      t.action === 'buy' ? 'text-sage' : 'text-claret'
                    }`}>
                      {t.action === 'buy' ? 'Buy' : 'Sell'} {fmt(t.amount)}
                    </p>
                    {t.shares != null && t.shares > 0 && (
                      <p className="text-[12px] text-ink-muted tabular-nums">
                        ~{t.shares} shares
                      </p>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}

          {rebalanceTrades.length === 0 && rebalanceMsg && (
            <p className="text-[13px] text-ink-muted px-1 md:px-0">
              No trades needed — portfolio is within target allocation.
            </p>
          )}
        </div>
      )}

      {/* Allocation History */}
      {allocHistory && allocHistory.history.length > 3 && (() => {
        const filtered = filterByPeriod(allocHistory.history, allocPeriod);
        return (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-1 px-1 md:px-0">
            <h2 className="font-serif text-heading text-ink">Allocation History</h2>
            <PeriodSelector value={allocPeriod} onChange={setAllocPeriod} />
          </div>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">How your portfolio composition evolved over time.</p>
          <EChartWrapper option={{
            tooltip: { trigger: 'axis' as const },
            legend: { type: 'scroll' as const, bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
            xAxis: { type: 'category' as const, data: filtered.map(h => formatDateForPeriod(h.date, allocPeriod)), axisLabel: { fontSize: 11, color: tc.inkMuted, rotate: 45 }, boundaryGap: false },
            yAxis: { type: 'value' as const, max: 100, axisLabel: { formatter: (v: number) => `${v}%`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
            series: allocHistory.holdings.map(name => ({
              name, type: 'line' as const, stack: 'total', areaStyle: { opacity: 0.7 },
              emphasis: { focus: 'series' as const }, symbol: 'none',
              data: filtered.map(h => {
                for (const [isin, w] of Object.entries(h.weights)) {
                  if (name === isin || holdings.some(hold => hold.security_isin === isin && hold.name === name)) return w;
                }
                return 0;
              }),
            })),
            grid: { left: 45, right: 10, top: 10, bottom: 60 },
          }} height="320px" />
        </div>
        );
      })()}

      {/* Savings Plans */}
      {savingsPlans && savingsPlans.plans.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Savings Plans</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            {savingsPlans.plan_count} detected plans · {fmt(savingsPlans.total_monthly)}/month total
          </p>
          <div className="space-y-2 px-1 md:px-0">
            {savingsPlans.plans.map(p => (
              <div key={p.isin} className="rounded-xl bg-parchment-deep px-3 py-2.5">
                <div className="flex items-center justify-between">
                  <div className="min-w-0 flex-1">
                    <p className="text-[15px] font-medium text-ink truncate">{p.name}</p>
                    <p className="text-[12px] text-ink-muted">
                      ~{fmt(p.monthly_amount)}/mo · {p.executions} buys · {p.months_active} months
                    </p>
                  </div>
                  <div className="text-right shrink-0 ml-3">
                    <p className="text-[15px] font-semibold tabular-nums">{fmt(p.current_value)}</p>
                    <p className={`text-[12px] font-medium tabular-nums ${p.dca_return_pct >= 0 ? 'text-sage' : 'text-claret'}`}>
                      {p.dca_return_pct >= 0 ? '+' : ''}{p.dca_return_pct}%
                    </p>
                  </div>
                </div>
                {p.lump_sum_value > 0 && (
                  <div className="flex items-center gap-3 mt-1.5 pt-1.5 border-t border-divider text-[11px]">
                    <span className="text-ink-muted">DCA: {fmt(p.current_value)}</span>
                    <span className="text-ink-muted">Lump-sum: {fmt(p.lump_sum_value)}</span>
                    <span className={`font-medium ${p.dca_advantage_eur >= 0 ? 'text-sage' : 'text-claret'}`}>
                      DCA {p.dca_advantage_eur >= 0 ? 'wins' : 'loses'} by {fmt(Math.abs(p.dca_advantage_eur))}
                    </span>
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      </>)}

      {activeTab === 'holdings' && (<>
      {/* Holdings */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Holdings</h2>
        <HoldingsTable holdings={holdings} onSecurityClick={(isin) => {
          if (securityDetail?.isin === isin) { setSecurityDetail(null); return; }
          api.getSecurityDetail(isin).then(setSecurityDetail).catch(console.error);
        }} />
      </div>

      <UnvestedPanel mode="detail" />

      {/* Security Detail Panel */}
      {securityDetail && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-start justify-between mb-3">
            <div>
              <h2 className="font-serif text-heading font-semibold text-ink">{securityDetail.name}</h2>
              <p className="text-[12px] text-ink-muted">
                {securityDetail.isin} {securityDetail.symbol && `· ${securityDetail.symbol}`} · {securityDetail.asset_class}
                {securityDetail.ter > 0 && ` · TER ${securityDetail.ter.toFixed(2)}%`}
              </p>
            </div>
            <button onClick={() => setSecurityDetail(null)} className="text-ink-muted hover:text-claret text-xl leading-none">×</button>
          </div>

          {/* KPI Cards */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Market Value</p>
              <p className="text-[13px] md:text-[15px] font-semibold tabular-nums">{fmt(securityDetail.total_value)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">P&L</p>
              <p className={`text-[13px] md:text-[15px] font-semibold tabular-nums ${securityDetail.unrealized_pl >= 0 ? 'text-sage' : 'text-claret'}`}>
                {securityDetail.unrealized_pl >= 0 ? '+' : ''}{fmt(securityDetail.unrealized_pl)}
              </p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Weight</p>
              <p className="text-[13px] md:text-[15px] font-semibold tabular-nums">{securityDetail.weight_pct}%</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Shares</p>
              <p className="text-[13px] md:text-[15px] font-semibold tabular-nums">{securityDetail.total_quantity}</p>
            </div>
          </div>

          {/* Price Sparkline */}
          {securityDetail.sparkline.length > 2 && (() => {
            // Build buy/sell marker data aligned to sparkline dates. The
            // sparkline x-axis is category-based, so a marker only renders
            // when its x-value matches an existing category. Transactions
            // executed on weekends/holidays have no matching market_data row
            // and used to be silently dropped — now we snap each one to the
            // first sparkline date on or after the txn date so the marker
            // appears at the next trading day instead of vanishing.
            const sparkDates = securityDetail.sparkline.map(s => s.date);
            const sparkSet = new Set(sparkDates);
            const priceMap = new Map(securityDetail.sparkline.map(s => [s.date, s.price]));
            const firstSparkDate = sparkDates[0];
            const lastSparkDate = sparkDates[sparkDates.length - 1];
            const snapToSparkline = (txnDate: string): string | null => {
              if (sparkSet.has(txnDate)) return txnDate;
              if (txnDate < firstSparkDate || txnDate > lastSparkDate) return null;
              // sparkDates is chronologically sorted; pick the first date on
              // or after the txn date (forward-fill to next trading day).
              for (const d of sparkDates) {
                if (d >= txnDate) return d;
              }
              return null;
            };
            const buyMarkers: [string, number][] = [];
            const sellMarkers: [string, number][] = [];
            const avgCost = securityDetail.total_quantity > 0 ? securityDetail.total_cost / securityDetail.total_quantity : 0;
            securityDetail.transactions.forEach(t => {
              const snapped = snapToSparkline(t.date);
              if (!snapped) return;
              const price = priceMap.get(snapped) || 0;
              if (t.type === 'buy' || t.type === 'savings_plan') buyMarkers.push([snapped, price]);
              else if (t.type === 'sell') sellMarkers.push([snapped, price]);
            });
            return (
              <div className="mb-4">
                <p className="text-[12px] text-ink-muted mb-1">
                  Price (90 days)
                  {avgCost > 0 && <span className="text-amber ml-2">— avg cost {fmt(avgCost)}</span>}
                  {buyMarkers.length > 0 && <span className="text-sage ml-2">● buys</span>}
                  {sellMarkers.length > 0 && <span className="text-claret ml-2">● sells</span>}
                </p>
                <EChartWrapper option={{
                  grid: { left: 50, right: 16, top: 8, bottom: 24 },
                  xAxis: { type: 'category' as const, data: securityDetail.sparkline.map(s => s.date), show: false },
                  yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, formatter: (v: number) => `${v.toFixed(0)}` }, splitLine: { lineStyle: { color: tc.divider } } },
                  series: [
                    { type: 'line', data: securityDetail.sparkline.map(s => s.price), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.forest, width: 1.5 }, areaStyle: { color: `color-mix(in srgb, ${tc.forest} 6%, transparent)` },
                      ...(avgCost > 0 ? { markLine: { silent: true, symbol: 'none', data: [{ yAxis: avgCost, lineStyle: { color: tc.gold, type: 'dashed', width: 1 }, label: { show: false } }] } } : {}),
                    },
                    ...(buyMarkers.length > 0 ? [{
                      type: 'scatter' as const, name: 'Buy',
                      data: buyMarkers.map(([date, price]) => [date, price]),
                      symbol: 'circle', symbolSize: 8,
                      itemStyle: { color: tc.sage },
                    }] : []),
                    ...(sellMarkers.length > 0 ? [{
                      type: 'scatter' as const, name: 'Sell',
                      data: sellMarkers.map(([date, price]) => [date, price]),
                      symbol: 'diamond', symbolSize: 10,
                      itemStyle: { color: tc.claret },
                    }] : []),
                  ],
                  tooltip: { trigger: 'axis' as const },
                }} height="180px" />
              </div>
            );
          })()}

          {/* Per-Account Positions */}
          {securityDetail.positions.length > 1 && (
            <div className="mb-4">
              <p className="text-[12px] text-ink-muted mb-1">By Account</p>
              <div className="space-y-1">
                {securityDetail.positions.map((p, i) => (
                  <div key={i} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-1.5">
                    <span className="text-[13px] text-ink">{p.account}</span>
                    <span className="text-[13px] tabular-nums">{p.quantity} shares · {fmt(p.value)}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Transaction History (last 10) */}
          {securityDetail.transactions.length > 0 && (
            <div>
              <p className="text-[12px] text-ink-muted mb-1">Recent Transactions ({securityDetail.transactions.length} total)</p>
              <div className="overflow-x-auto">
                <table className="w-full text-[12px] min-w-[400px]">
                  <thead>
                    <tr className="text-ink-muted text-left border-b border-divider">
                      <th className="px-2 py-1 font-medium">Date</th>
                      <th className="px-2 py-1 font-medium">Type</th>
                      <th className="px-2 py-1 font-medium text-right">Qty</th>
                      <th className="px-2 py-1 font-medium text-right">Amount</th>
                      <th className="px-2 py-1 font-medium text-right">Holding</th>
                    </tr>
                  </thead>
                  <tbody>
                    {securityDetail.transactions.slice(-10).reverse().map((t, i) => (
                      <tr key={i} className="border-b border-divider">
                        <td className="px-2 py-1 tabular-nums">{t.date}</td>
                        <td className={`px-2 py-1 font-medium ${t.type === 'buy' || t.type === 'savings_plan' ? 'text-sage' : t.type === 'sell' ? 'text-claret' : 'text-forest'}`}>{t.type}</td>
                        <td className="px-2 py-1 text-right tabular-nums">{t.quantity > 0 ? t.quantity : '—'}</td>
                        <td className="px-2 py-1 text-right tabular-nums">{fmt(t.amount)}</td>
                        <td className="px-2 py-1 text-right tabular-nums">{t.running_qty}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          <p className="text-[11px] text-ink-muted mt-3">
            First buy: {securityDetail.first_buy} · Cost basis: {fmt(securityDetail.total_cost)}
          </p>

          {/* Product Switch Comparison */}
          <div className="mt-4 pt-3 border-t border-divider">
            <p className="text-[12px] text-ink-muted mb-2">What if I had bought a different product?</p>
            <div className="flex gap-2 mb-2">
              <input
                type="text"
                value={switchAltISIN}
                onChange={e => setSwitchAltISIN(e.target.value.toUpperCase())}
                placeholder="Alternative ISIN (e.g. IE00B4L5Y983)"
                className="flex-1 rounded-[6px] border border-divider bg-parchment text-ink px-2 py-1 text-[12px] tabular-nums"
              />
              <button
                onClick={async () => {
                  if (!switchAltISIN || !securityDetail) return;
                  setSwitchLoading(true);
                  setSwitchResult(null);
                  try {
                    const res = await fetch(`/api/portfolio/switch-compare?current=${securityDetail.isin}&alternative=${switchAltISIN}`);
                    if (res.ok) setSwitchResult(await res.json());
                  } catch {} finally { setSwitchLoading(false); }
                }}
                disabled={switchLoading || !switchAltISIN}
                className="apple-btn-primary text-[12px] px-3 py-1"
              >
                {switchLoading ? 'Comparing...' : 'Compare'}
              </button>
            </div>

            {switchResult && (
              <div className="space-y-2">
                <div className="grid grid-cols-3 gap-2">
                  <div className="rounded-lg bg-parchment-deep p-2 text-center">
                    <p className="text-[11px] text-ink-muted">Current Value</p>
                    <p className="font-serif text-[15px] font-semibold tabular-nums">{fmt(switchResult.current_value)}</p>
                  </div>
                  <div className="rounded-lg bg-parchment-deep p-2 text-center">
                    <p className="text-[11px] text-ink-muted">Alternative</p>
                    <p className="font-serif text-[15px] font-semibold tabular-nums">{fmt(switchResult.alternative_value)}</p>
                  </div>
                  <div className="rounded-lg bg-parchment-deep p-2 text-center">
                    <p className="text-[11px] text-ink-muted">Difference</p>
                    <p className={`font-serif text-[15px] font-semibold tabular-nums ${switchResult.difference_eur >= 0 ? 'text-sage' : 'text-claret'}`}>
                      {switchResult.difference_eur >= 0 ? '+' : ''}{fmt(switchResult.difference_eur)}
                    </p>
                  </div>
                </div>
                <div className="flex items-center justify-between text-[11px] text-ink-muted">
                  <span>Correlation: {switchResult.correlation.toFixed(2)}</span>
                  <span>{switchResult.difference_pct >= 0 ? '+' : ''}{switchResult.difference_pct}%</span>
                </div>
                {switchResult.low_correlation_warning && (
                  <p className="text-[11px] text-amber">Low correlation ({switchResult.correlation.toFixed(2)}) — these products track different markets. Comparison may not be meaningful.</p>
                )}

                {/* Tax cost of switching */}
                {switchResult.unrealized_gain !== 0 && (
                  <div className="mt-2 pt-2 border-t border-divider">
                    <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-1.5">Tax Cost of Switching</p>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-[11px]">
                      <span className="text-ink-muted">Unrealized Gain</span>
                      <span className={`text-right tabular-nums ${switchResult.unrealized_gain >= 0 ? 'text-sage' : 'text-claret'}`}>{fmt(switchResult.unrealized_gain)}</span>
                      {switchResult.is_equity && switchResult.teilfreistellung > 0 && (<>
                        <span className="text-ink-muted">Teilfreistellung (30%)</span>
                        <span className="text-right tabular-nums text-sage">-{fmt(switchResult.teilfreistellung)}</span>
                      </>)}
                      {switchResult.freibetrag_used > 0 && (<>
                        <span className="text-ink-muted">Freibetrag</span>
                        <span className="text-right tabular-nums text-sage">-{fmt(switchResult.freibetrag_used)}</span>
                      </>)}
                      <span className="text-ink-muted font-medium">Tax on Switch</span>
                      <span className="text-right tabular-nums text-claret font-medium">{fmt(switchResult.tax_on_switch)}</span>
                      <span className="text-ink-muted">Net Proceeds</span>
                      <span className="text-right tabular-nums">{fmt(switchResult.net_proceeds)}</span>
                    </div>
                    {switchResult.break_even_years > 0 ? (
                      <p className="text-[11px] text-ink-muted mt-1.5">Break-even: ~{switchResult.break_even_years} year{switchResult.break_even_years !== 1 ? 's' : ''} for the alternative to recover the tax cost.</p>
                    ) : switchResult.difference_eur < 0 && switchResult.tax_on_switch > 0 ? (
                      <p className="text-[11px] text-claret mt-1.5">No break-even — the alternative historically underperforms, so the tax cost would never be recovered.</p>
                    ) : null}
                  </div>
                )}

                {/* Gradual Switch Strategy */}
                <div className="mt-2 pt-2 border-t border-divider">
                  <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-1.5">Switch Strategy</p>
                  {switchResult.difference_eur < 0 && switchResult.unrealized_gain >= 0 ? (
                    <p className="text-[11px] text-ink-muted">No switch recommended — the alternative has historically underperformed by {fmt(Math.abs(switchResult.difference_eur))}.</p>
                  ) : <div className="space-y-1.5">
                    {/* Option 1: Redirect contributions */}
                    <div className="flex items-start gap-2 text-[11px]">
                      <span className="w-4 h-4 rounded-full bg-parchment-deep border border-sage text-sage flex items-center justify-center shrink-0 mt-0.5 text-[10px] font-semibold">1</span>
                      <div>
                        <p className="text-ink font-medium">Redirect future contributions</p>
                        <p className="text-ink-muted">Stop buying the current product and invest new money in the alternative. No tax event. Gradual transition over time.</p>
                      </div>
                    </div>

                    {/* Option 2: Tax-year splitting */}
                    {switchResult.tax_on_switch > 1000 && (
                      <div className="flex items-start gap-2 text-[11px]">
                        <span className="w-4 h-4 rounded-full bg-parchment-deep border border-amber text-amber flex items-center justify-center shrink-0 mt-0.5 text-[10px] font-semibold">2</span>
                        <div>
                          <p className="text-ink font-medium">Split across tax years</p>
                          <p className="text-ink-muted">Sell {fmt(switchResult.current_value / 2)}/year over 2 years to use the Sparerpauschbetrag twice (saves ~{fmt(Math.min(1000, switchResult.taxable_gain) * 0.26375)}).</p>
                        </div>
                      </div>
                    )}

                    {/* Option 3: Loss harvesting */}
                    {switchResult.unrealized_gain < 0 && (
                      <div className="flex items-start gap-2 text-[11px]">
                        <span className="w-4 h-4 rounded-full bg-parchment-deep border border-forest text-forest flex items-center justify-center shrink-0 mt-0.5 text-[10px] font-semibold">3</span>
                        <div>
                          <p className="text-ink font-medium">Tax-loss harvesting opportunity</p>
                          <p className="text-ink-muted">Current holding has {fmt(Math.abs(switchResult.unrealized_gain))} in unrealized losses. Selling now and switching realizes the loss, which offsets future gains (saves ~{fmt(Math.abs(switchResult.unrealized_gain) * 0.26375)} in tax).</p>
                        </div>
                      </div>
                    )}

                    {/* Option 3 alt: Full switch when tax is low */}
                    {switchResult.unrealized_gain >= 0 && switchResult.tax_on_switch <= 1000 && switchResult.tax_on_switch > 0 && (
                      <div className="flex items-start gap-2 text-[11px]">
                        <span className="w-4 h-4 rounded-full bg-parchment-deep border border-forest text-forest flex items-center justify-center shrink-0 mt-0.5 text-[10px] font-semibold">2</span>
                        <div>
                          <p className="text-ink font-medium">Full switch (low tax cost)</p>
                          <p className="text-ink-muted">Tax cost of {fmt(switchResult.tax_on_switch)} is modest. A one-time switch may be simpler than a gradual transition.</p>
                        </div>
                      </div>
                    )}
                  </div>}
                </div>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Time Machine */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <h2 className="font-serif text-heading text-ink mb-2">Time Machine</h2>
        <p className="text-[13px] text-ink-muted mb-3">Reconstruct your portfolio at any historical date.</p>
        <div className="flex gap-2 mb-3">
          <input type="date" value={tmDate} onChange={e => setTmDate(e.target.value)}
            className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[15px]" />
          <button onClick={async () => {
            if (!tmDate) return;
            setTmLoading(true);
            try {
              const res = await fetch(`/api/portfolio/time-machine?date=${tmDate}`);
              setTmData(await res.json());
            } catch { /* ignore */ } finally { setTmLoading(false); }
          }} disabled={!tmDate || tmLoading} className="apple-btn-primary shrink-0">
            {tmLoading ? '...' : 'Go'}
          </button>
        </div>

        {tmData && (
          <div className="space-y-3">
            {/* Then vs Now comparison */}
            <div className="grid grid-cols-2 gap-2">
              <div className="rounded-lg bg-parchment-deep p-2.5 text-center">
                <p className="text-[11px] text-ink-muted">{tmData.date}</p>
                <p className="font-serif text-[20px] font-semibold tabular-nums">{fmt(tmData.net_worth)}</p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5 text-center">
                <p className="text-[11px] text-ink-muted">Today</p>
                <p className="font-serif text-[20px] font-semibold tabular-nums">{fmt(tmData.current.net_worth)}</p>
              </div>
            </div>

            {/* Change attribution */}
            <div className="rounded-xl bg-parchment-deep p-3">
              <div className="flex items-baseline justify-between mb-2">
                <span className="text-[15px] font-medium text-ink">Change Since</span>
                <span className={`text-[15px] font-semibold tabular-nums ${tmData.change.net_worth_change >= 0 ? 'text-sage' : 'text-claret'}`}>
                  {tmData.change.net_worth_change >= 0 ? '+' : ''}{fmt(tmData.change.net_worth_change)} ({tmData.change.net_worth_pct >= 0 ? '+' : ''}{tmData.change.net_worth_pct}%)
                </span>
              </div>
              <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-[12px]">
                <span className="text-ink-muted">Contributions</span>
                <span className="tabular-nums text-right">{fmt(tmData.change.contributions)}</span>
                <span className="text-ink-muted">Market Return</span>
                <span className={`tabular-nums text-right ${tmData.change.market_return >= 0 ? 'text-sage' : 'text-claret'}`}>{fmt(tmData.change.market_return)}</span>
                <span className="text-ink-muted">Dividends</span>
                <span className="tabular-nums text-right">{fmt(tmData.change.dividends)}</span>
              </div>
            </div>

            {/* Historical holdings */}
            {tmData.holdings.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-[12px] font-medium text-ink-muted">Holdings on {tmData.date}</p>
                {tmData.holdings.map((hld, i) => (
                  <div key={i} className="flex items-center justify-between text-[12px]">
                    <div className="min-w-0 flex-1">
                      <span className="font-medium text-ink">{hld.name}</span>
                      <span className="text-ink-muted ml-1">{hld.weight_pct}%</span>
                    </div>
                    <span className="tabular-nums text-ink shrink-0 ml-2">{fmt(hld.value)}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
      </>)}

      {activeTab === 'goals' && (<>
      {/* Financial Goals */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-3">Financial Goals</h2>

        {goals.length > 0 && (
          <div className="space-y-2 mb-4">
            {goals.map(g => (
              <div key={g.id} className="flex items-center justify-between rounded-xl bg-parchment-deep px-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <p className="text-[15px] font-medium text-ink">{g.name}</p>
                  <p className="text-[12px] text-ink-muted">
                    {fmt(g.target_amount)}
                    {' by '}{g.target_date}
                    {g.monthly_contribution > 0 && ` · ${fmt(g.monthly_contribution)}/mo`}
                  </p>
                </div>
                <button
                  onClick={() => api.deleteGoal(g.id).then(() => setGoals(prev => prev.filter(x => x.id !== g.id))).catch(console.error)}
                  className="text-claret text-[12px] font-medium shrink-0 ml-3 hover:underline"
                >Delete</button>
              </div>
            ))}
          </div>
        )}

        <div className="space-y-2">
          <div className="grid grid-cols-1 md:grid-cols-2 gap-2">
            <input aria-label="Goal name" type="text" placeholder="Goal name (e.g., Retirement)" value={goalName} onChange={e => setGoalName(e.target.value)}
              className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] placeholder-ink-muted" />
            <input aria-label="Target amount" type="number" placeholder="Target amount (EUR)" value={goalTarget} onChange={e => setGoalTarget(e.target.value)}
              className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] placeholder-ink-muted tabular-nums" />
            <input aria-label="Target date" type="date" value={goalDate} onChange={e => setGoalDate(e.target.value)}
              className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] text-ink" />
            <input aria-label="Monthly contribution" type="number" placeholder="Monthly contribution (EUR)" value={goalContrib} onChange={e => setGoalContrib(e.target.value)}
              className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] placeholder-ink-muted tabular-nums" />
          </div>
          <div className="flex items-center gap-2">
            <label className="text-[12px] text-ink-muted shrink-0">Assumed return:</label>
            <input type="number" aria-label="Assumed annual return" value={goalReturn} onChange={e => setGoalReturn(e.target.value)} step="0.5"
              className="w-16 rounded-[8px] border border-divider bg-parchment text-ink px-2 py-2.5 text-[16px] tabular-nums text-right" />
            <span className="text-[12px] text-ink-muted">%/year</span>
            <div className="flex-1" />
            <button
              onClick={() => {
                if (!goalName || !goalTarget || !goalDate) return;
                api.createGoal({ name: goalName, target_amount: parseFloat(goalTarget), target_date: goalDate,
                  monthly_contribution: parseFloat(goalContrib) || 0, assumed_return_pct: parseFloat(goalReturn) || 7,
                }).then(() => { setGoalName(''); setGoalTarget(''); setGoalDate(''); setGoalContrib('');
                  api.listGoals().then(r => setGoals(r.goals || [])).catch(() => {}); }).catch(console.error);
              }}
              disabled={!goalName || !goalTarget || !goalDate}
              className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2.5 text-[16px] font-medium disabled:opacity-40 hover:bg-forest-light transition-colors"
            >Add Goal</button>
          </div>
        </div>
      </div>

      {/* Price Alerts */}
      <div>
        <h2 className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-3">Price Alerts</h2>

        {alerts.length > 0 && (
          <div className="space-y-2 mb-4">
            {alerts.map(a => (
              <div key={a.id} className={`flex items-center justify-between rounded-xl px-3 py-2.5 ${a.is_active ? 'bg-parchment-deep' : 'bg-parchment-deep opacity-60'}`}>
                <div className="min-w-0 flex-1">
                  <p className="text-[15px] font-medium text-ink">
                    {a.alert_type === 'portfolio_milestone' ? 'Net Worth' : (a.security_name || a.security_isin || '—')}
                    {' '}
                    <span className="text-[12px] text-ink-muted">
                      {a.alert_type === 'price_above' ? '≥' : a.alert_type === 'price_below' ? '≤' : a.alert_type === 'portfolio_milestone' ? 'crosses' : 'Δ'}
                      {' '}{fmt(a.threshold)}
                    </span>
                  </p>
                </div>
                <div className="flex items-center gap-2 shrink-0 ml-2">
                  <button onClick={() => api.toggleAlert(a.id).then(() => api.listAlerts().then(r => setAlerts(r.alerts || []))).catch(console.error)}
                    className={`text-[12px] font-medium ${a.is_active ? 'text-amber' : 'text-sage'}`}>
                    {a.is_active ? 'Pause' : 'Resume'}
                  </button>
                  <button onClick={() => api.deleteAlert(a.id).then(() => setAlerts(prev => prev.filter(x => x.id !== a.id))).catch(console.error)}
                    className="text-claret text-[12px] font-medium">Delete</button>
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="space-y-2">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
            <select aria-label="Alert type" value={alertType} onChange={e => setAlertType(e.target.value)}
              className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px]">
              <option value="price_above">Price above</option>
              <option value="price_below">Price below</option>
              <option value="portfolio_milestone">Net worth milestone</option>
            </select>
            {alertType !== 'portfolio_milestone' && (
              <select aria-label="Security" value={alertISIN} onChange={e => setAlertISIN(e.target.value)}
                className="rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px]">
                <option value="">Select security...</option>
                {securities.map(s => <option key={s.isin} value={s.isin}>{s.name}</option>)}
              </select>
            )}
            <div className="flex gap-2">
              <input aria-label="Alert threshold" type="number" placeholder="Threshold (EUR)" value={alertThreshold} onChange={e => setAlertThreshold(e.target.value)}
                className="flex-1 rounded-[8px] border border-divider bg-parchment text-ink px-3 py-2 text-[16px] tabular-nums" />
              <button
                onClick={() => {
                  if (!alertThreshold) return;
                  api.createAlert({
                    alert_type: alertType,
                    security_isin: alertType !== 'portfolio_milestone' ? alertISIN : undefined,
                    threshold: parseFloat(alertThreshold),
                  }).then(() => {
                    setAlertThreshold('');
                    api.listAlerts().then(r => setAlerts(r.alerts || [])).catch(() => {});
                  }).catch(console.error);
                }}
                disabled={!alertThreshold || (alertType !== 'portfolio_milestone' && !alertISIN)}
                className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2.5 text-[16px] font-medium disabled:opacity-40 hover:bg-forest-light transition-colors"
              >Add</button>
            </div>
          </div>
        </div>

        {/* Recent Notifications */}
        {notifications.length > 0 && (
          <div className="mt-4 pt-4 border-t border-divider">
            <div className="flex items-center justify-between mb-2">
              <p className="text-[15px] font-medium text-ink">Recent Notifications</p>
              <button onClick={() => api.markNotificationsRead().then(() => setNotifications(prev => prev.map(n => ({ ...n, is_read: true })))).catch(console.error)}
                className="text-[12px] text-forest font-medium">Mark all read</button>
            </div>
            <div className="space-y-1.5">
              {notifications.slice(0, 10).map(n => (
                <div key={n.id} className={`flex items-start gap-2 rounded-lg px-3 py-2 ${n.is_read ? 'bg-parchment' : 'bg-inset border-l-[3px] border-forest'}`}>
                  <span className={`w-2 h-2 rounded-full shrink-0 mt-1.5 ${n.is_read ? 'bg-divider' : 'bg-forest'}`} />
                  <div className="min-w-0 flex-1">
                    <p className="text-[13px] text-ink">{n.message}</p>
                    <p className="text-[11px] text-ink-muted">{new Date(n.triggered_at).toLocaleDateString('de-DE')}</p>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
      </>)}
    </div>
  );
}
