import { useState, useEffect } from 'react';
import { api, type ETFHoldingEntry, type HoldingRow, type ConcentrationAlert, type TopHolding, type TreemapNode, type SectorHistoryPoint, type RiskData, type CurrencyExposureEntry, type TaxData, type CostData, type TaxLot } from '../api/client';
import EChartWrapper from '../components/charts/EChartWrapper';
import { TabBar, PeriodSelector, filterByPeriod, formatDateForPeriod, periodCutoff } from '../components/ui';
import type { Period } from '../components/ui';
import { useThemeColors } from '../hooks/useThemeColors';

type AnalysisTab = 'risk' | 'tax' | 'costs' | 'allocation' | 'spending';
const ANALYSIS_TABS_SLIM: { id: AnalysisTab; label: string }[] = [
  { id: 'risk', label: 'Risk' },
  { id: 'costs', label: 'Costs' },
  { id: 'allocation', label: 'Allocation' },
  { id: 'spending', label: 'Spending' },
];

type TaxSubTab = 'overview' | 'tools' | 'filing';
const TAX_TABS: { id: TaxSubTab; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'tools', label: 'Tools' },
  { id: 'filing', label: 'Filing' },
];

export default function Analysis({ defaultTab }: { defaultTab?: AnalysisTab } = {}) {
  const tc = useThemeColors();
  const [activeTab, setActiveTab] = useState<AnalysisTab>(defaultTab || 'risk');
  const [taxSubTab, setTaxSubTab] = useState<TaxSubTab>(() => (localStorage.getItem('tax_subtab') as TaxSubTab) || 'overview');
  const [sectors, setSectors] = useState<Record<string, number>>({});
  const [countries, setCountries] = useState<Record<string, number>>({});
  const [overlap, setOverlap] = useState<{ labels: string[]; matrix: number[][] } | null>(null);
  const [holdings, setHoldings] = useState<HoldingRow[]>([]);
  const [alerts, setAlerts] = useState<ConcentrationAlert[]>([]);
  const [topHoldings, setTopHoldings] = useState<TopHolding[]>([]);
  const [treemapData, setTreemapData] = useState<TreemapNode[]>([]);
  const [sectorHistory, setSectorHistory] = useState<SectorHistoryPoint[]>([]);
  const [allSectors, setAllSectors] = useState<string[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [riskData, setRiskData] = useState<RiskData | null>(null);
  const [currencies, setCurrencies] = useState<CurrencyExposureEntry[]>([]);
  const [taxData, setTaxData] = useState<TaxData | null>(null);
  const [taxYear, setTaxYear] = useState<number>(new Date().getFullYear());
  const [costData, setCostData] = useState<CostData | null>(null);
  const [fsaStatus, setFsaStatus] = useState<{ year: number; accounts: { account_name: string; dividends: number; interest: number; realized_gains: number; total_income: number }[]; total_income: number; allowance: number; joint: boolean; used: number; remaining: number; utilization_pct: number; recommendation?: { account: string; projected_income: number; recommended_fsa: number }[] } | null>(null);
  const [fsaJoint, setFsaJoint] = useState<boolean>(() => localStorage.getItem('fsa_joint') === '1');
  const [lossPots, setLossPots] = useState<{ year: number; equity_losses: number; equity_gains: number; equity_balance: number; general_losses: number; general_gains: number; general_balance: number; carry_forward_equity: number; carry_forward_general: number }[]>([]);
  const [alternatives, setAlternatives] = useState<{ isin: string; name: string; current_ter: number; alternatives: { name: string; ter: number; annual_saving: number; ten_year_saving: number }[] }[]>([]);
  const [correlation, setCorrelation] = useState<{ labels: string[]; matrix: number[][] } | null>(null);
  const [sectorDriftPeriod, setSectorDriftPeriod] = useState<Period>('All');
  const [drawdownPeriod, setDrawdownPeriod] = useState<Period>('All');
  const [rollingVolPeriod, setRollingVolPeriod] = useState<Period>('All');
  const [fxHistory, setFxHistory] = useState<Record<string, { date: string; rate: number }[]> | null>(null);
  const [benchComp, setBenchComp] = useState<{ benchmark_name: string; actual_value: number; benchmark_value: number; difference: number; comparison: { date: string; actual: number; benchmark: number }[] } | null>(null);
  const [inflation, setInflation] = useState<{ history: { date: string; nominal: number; real: number }[]; nominal_return?: number; real_return?: number; purchasing_power_lost?: number } | null>(null);
  const [taxLots, setTaxLots] = useState<TaxLot[]>([]);
  const [sellAmounts, setSellAmounts] = useState<Record<string, string>>({});
  const [sellResults, setSellResults] = useState<{ results: { isin: string; name: string; sell_amount: number; cost_basis: number; realized_gain: number; teilfreistellung: number; taxable_gain: number; estimated_tax: number; net_proceeds: number; lots_consumed: number; is_equity_fund: boolean }[]; total_tax: number; total_proceeds: number; effective_rate?: number; church_tax_rate?: number } | null>(null);
  const [churchTaxRate, setChurchTaxRate] = useState<number>(() => parseFloat(localStorage.getItem('church_tax_rate') || '0') || 0);
  const [cashFlow, setCashFlow] = useState<{ history: { month: string; income: number; expenses: number; net: number }[]; projection: { month: string; income: number; expenses: number; net: number }[]; avg_income: number; avg_expenses: number; avg_net: number } | null>(null);

  const [spendingData, setSpendingData] = useState<{ monthly: { month: string; income: number; expenses: number; net: number; by_category: Record<string, number> }[]; categories: { category: string; total: number; count: number; avg_monthly: number }[]; subscriptions: { name: string; amount: number; occurrences: number; annual_cost: number }[]; total_income: number; total_expenses: number; savings_rate_pct: number; savings_rate_pct_12m: number; window_months: number; avg_monthly_expense: number; months_analyzed: number } | null>(null);

  const [taxCalendar, setTaxCalendar] = useState<{ year: number; events: { month: number; date: string; title: string; description: string; amount?: number; category: string; urgency: string; action_url?: string }[]; upcoming: number } | null>(null);
  const [reconAmounts, setReconAmounts] = useState<Record<string, string>>(() => {
    try { return JSON.parse(localStorage.getItem('recon_amounts') || '{}'); } catch { return {}; }
  });
  const [journal, setJournal] = useState<{ entries: { id: string; date: string; action_type: string; isin?: string; amount?: number; reason: string; outcome?: string; days_ago: number }[] } | null>(null);
  const [journalReason, setJournalReason] = useState('');
  const [journalType, setJournalType] = useState('buy');
  const [volContext, setVolContext] = useState<{ elevated: boolean; drawdown_pct: number; vol_30d: number; message: string; past_drawdowns: { from_date: string; trough_date: string; recovery_date: string; max_drawdown_pct: number; recovery_days: number }[]; ath_date: string; all_time_high: number } | null>(null);
  const [stressTest, setStressTest] = useState<{ scenarios: { name: string; period: string; index_return_pct: number; recovery_months: number; worst_month_pct: number; description: string }[]; portfolio_impact: { name: string; drawdown_pct: number; drawdown_eur: number; recovery_months: number; worst_month_pct: number; worst_month_eur: number }[]; holding_impacts: { name: string; isin: string; value: number; is_equity: boolean; drawdown_eur: number; drawdown_pct: number }[]; dca_recovery: { name: string; without_dca_months: number; with_dca_months: number; acceleration_months: number }[]; portfolio_value: number; equity_pct: number; cash_buffer: number; cash_buffer_months: number } | null>(null);
  const [sparplanStreak, setSparplanStreak] = useState<{ current_streak: number; longest_streak: number; total_months: number; active_months: number; missed_months: number; consistency_pct: number; avg_monthly: number; missed_cost: number; total_invested: number } | null>(null);
  const [anlageKAP, setAnlageKAP] = useState<{ year: number; brokers: { broker_name: string; account_name: string; dividends: number; interest: number; realized_gains: number; realized_losses: number; teilfreistellung: number; withheld_tax: number }[]; anlage_kap: { line: number; description: string; amount: number }[]; total_income: number; net_taxable: number; estimated_tax: number; total_withheld: number; cross_broker_note: string; broker_count: number } | null>(null);

  // ETF holdings drill-down
  const [selectedETF, setSelectedETF] = useState<string | null>(null);
  const [etfHoldings, setEtfHoldings] = useState<ETFHoldingEntry[]>([]);
  const [etfName, setEtfName] = useState('');
  const [etfLoading, setEtfLoading] = useState(false);

  useEffect(() => {
    // Primary data: allocation summary (sectors + countries + currency in 1 call) + overlap + holdings
    Promise.all([api.getAllocationSummary(), api.getOverlap(), api.listHoldings()])
      .then(([summary, ovl, hld]) => {
        // Defensive defaults — server has been observed returning {} on empty
        // portfolios, which used to crash the page on Object.entries(undefined).
        setSectors(summary.sectors || {});
        setCountries(summary.countries || {});
        setCurrencies(summary.currencies || []);
        setOverlap(ovl && Array.isArray(ovl.labels) ? ovl : { labels: [], matrix: [] });
        setHoldings((hld.holdings || []).filter(h => h.asset_class === 'etf'));
      })
      .catch(e => setError(e.message || 'Failed to load analysis data'))
      .finally(() => setLoading(false));
    // Secondary data (non-blocking)
    api.getAlerts().then(alt => setAlerts(alt.alerts || [])).catch(() => {});
    api.getTopHoldings().then(th => setTopHoldings(th.holdings || [])).catch(() => {});
    api.getTreemap().then(tm => setTreemapData(tm.children || [])).catch(() => {});
    api.getSectorHistory().then(sh => { setSectorHistory(sh.history || []); setAllSectors(sh.sectors || []); }).catch(() => {});
    api.getRisk().then(setRiskData).catch(() => {});
    api.getTax().then(setTaxData).catch(() => {});
    api.getTaxLots().then(r => setTaxLots(r.lots || [])).catch(() => {});
    fetch('/api/analysis/loss-pots').then(r => r.json()).then(d => {
      setLossPots(d.years || []);
      if (d.cross_broker_saving > 0) {
        // Could show a notification, for now just logged
      }
    }).catch(() => {});
    fetch(`/api/analysis/fsa-status${fsaJoint ? '?joint=1' : ''}`).then(r => r.json()).then(setFsaStatus).catch(() => {});
    api.getCosts().then(setCostData).catch(() => {});
    api.getCorrelation().then(setCorrelation).catch(() => {});
    api.getAlternatives().then(r => setAlternatives(r.alternatives || [])).catch(() => {});
    api.getFXHistory().then(r => setFxHistory(r.rates)).catch(() => {});
    api.getCashFlow().then(setCashFlow).catch(() => {});
    api.getInflation().then(setInflation).catch(() => {});
    api.getBenchmarkComparison().then(setBenchComp).catch(() => {});
    fetch('/api/analysis/spending').then(r => r.json()).then(setSpendingData).catch(() => {});
    fetch('/api/analysis/volatility-context').then(r => r.json()).then(setVolContext).catch(() => {});
    fetch('/api/analysis/crisis-stress-test').then(r => r.json()).then(setStressTest).catch(() => {});
    fetch('/api/analysis/journal').then(r => r.json()).then(setJournal).catch(() => {});
    fetch('/api/portfolio/sparplan-streak').then(r => r.json()).then(setSparplanStreak).catch(() => {});
    fetch('/api/analysis/tax-calendar').then(r => r.json()).then(setTaxCalendar).catch(() => {});
    fetch('/api/analysis/anlage-kap').then(r => r.json()).then(setAnlageKAP).catch(() => {});
  }, []);

  // Re-fetch FSA when joint toggle changes; persist preference.
  useEffect(() => {
    localStorage.setItem('fsa_joint', fsaJoint ? '1' : '0');
    fetch(`/api/analysis/fsa-status${fsaJoint ? '?joint=1' : ''}`).then(r => r.json()).then(setFsaStatus).catch(() => {});
  }, [fsaJoint]);

  const loadETFHoldings = (isin: string) => {
    if (selectedETF === isin) {
      setSelectedETF(null);
      setEtfHoldings([]);
      return;
    }
    setSelectedETF(isin);
    setEtfLoading(true);
    api.getETFHoldings(isin)
      .then((data) => {
        setEtfHoldings(data.holdings);
        setEtfName(data.etf_name);
      })
      .catch(console.error)
      .finally(() => setEtfLoading(false));
  };

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
        <button onClick={() => { setError(null); setLoading(true); Promise.all([api.getAllocationSummary(), api.getOverlap(), api.listHoldings()]).then(([summary, ovl, hld]) => { setSectors(summary.sectors || {}); setCountries(summary.countries || {}); setCurrencies(summary.currencies || []); setOverlap(ovl && Array.isArray(ovl.labels) ? ovl : { labels: [], matrix: [] }); setHoldings((hld.holdings || []).filter(h => h.asset_class === 'etf')); }).catch(e => setError(e.message)).finally(() => setLoading(false)); }} className="text-forest text-[15px] font-medium">Retry</button>
      </div>
    );
  }

  const sectorEntries = Object.entries(sectors).sort(([, a], [, b]) => b - a);
  const countryEntries = Object.entries(countries).sort(([, a], [, b]) => b - a);

  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(n);

  const sectorChartOption = {
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
      data: sectorEntries.map(([name, value]) => ({ name, value: Math.round(value * 100) / 100 })),
      label: { fontSize: 11, color: tc.inkBody, overflow: 'truncate' as const, width: 80 },
      itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
    }],
    graphic: [{ type: 'text', left: 'center', top: 'center', style: { text: `${sectorEntries.length} sectors`, fontSize: 13, fontWeight: 600, fill: tc.inkMuted } }],
  };

  const filteredSectorHistory = filterByPeriod(sectorHistory, sectorDriftPeriod);
  const sectorDriftOption = filteredSectorHistory.length > 1 ? {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const ps = params as { seriesName: string; value: number; marker: string }[];
        if (!Array.isArray(ps) || ps.length === 0) return '';
        const lines = ps
          .filter(p => p.value > 0.1)
          .sort((a, b) => b.value - a.value)
          .map(p => `${p.marker} ${p.seriesName}: ${p.value.toFixed(1)}%`);
        return lines.join('<br/>');
      },
    },
    legend: {
      type: 'scroll' as const,
      bottom: 0,
      textStyle: { fontSize: 11, color: tc.inkMuted },
    },
    xAxis: {
      type: 'category' as const,
      data: filteredSectorHistory.map(s => formatDateForPeriod(s.date, sectorDriftPeriod)),
      axisLabel: { fontSize: 11, color: tc.inkMuted, rotate: 45 },
      boundaryGap: false,
    },
    yAxis: {
      type: 'value' as const,
      max: 100,
      axisLabel: { formatter: (v: number) => `${v}%`, fontSize: 11, color: tc.inkMuted },
      splitLine: { lineStyle: { color: tc.divider } },
    },
    series: allSectors.map(sector => ({
      name: sector,
      type: 'line' as const,
      stack: 'total',
      areaStyle: { opacity: 0.7 },
      emphasis: { focus: 'series' as const },
      symbol: 'none',
      data: filteredSectorHistory.map(s => Math.round((s.sectors[sector] || 0) * 100) / 100),
    })),
    grid: { left: 45, right: 10, top: 10, bottom: 60 },
  } : null;

  const countryChartOption = {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const p = params as { name: string; data: number }[];
        if (!Array.isArray(p) || p.length === 0) return '';
        return `${p[0].name}<br/>${fmt(p[0].data)}`;
      },
    },
    xAxis: {
      type: 'value' as const,
      axisLabel: { formatter: (v: number) => v >= 1000 ? `${(v / 1000).toFixed(0)}k` : `${v.toFixed(0)}`, fontSize: 12, color: tc.inkMuted },
      splitLine: { lineStyle: { color: tc.divider } },
    },
    yAxis: {
      type: 'category' as const,
      data: countryEntries.slice(0, 15).reverse().map(([name]) => name),
      axisLabel: { fontSize: 12, color: tc.inkBody },
    },
    series: [{
      type: 'bar' as const,
      data: countryEntries.slice(0, 15).reverse().map(([, value]) => Math.round(value * 100) / 100),
      itemStyle: { borderRadius: [0, 4, 4, 0] },
      barMaxWidth: 20,
    }],
    grid: { left: 60, right: 10, top: 10, bottom: 30 },
  };

  const overlapChartOption = overlap && overlap.labels.length > 0 ? {
    tooltip: {
      formatter: (params: unknown) => {
        const p = params as { data: number[] };
        const [x, y, val] = p.data;
        return `${overlap.labels[x]} vs ${overlap.labels[y]}: ${val.toFixed(1)}%`;
      },
    },
    xAxis: {
      type: 'category' as const,
      data: overlap.labels,
      axisLabel: { rotate: 45, fontSize: 11, color: tc.inkBody },
    },
    yAxis: {
      type: 'category' as const,
      data: overlap.labels,
      axisLabel: { fontSize: 11, color: tc.inkBody },
    },
    visualMap: {
      min: 0,
      max: 100,
      calculable: true,
      orient: 'horizontal' as const,
      left: 'center',
      bottom: 0,
      inRange: { color: [tc.divider, tc.sage] },
      textStyle: { color: tc.inkMuted },
    },
    series: [{
      type: 'heatmap' as const,
      data: overlap.matrix.flatMap((row, i) =>
        row.map((val, j) => [i, j, Math.round(val * 10) / 10])
      ),
      label: { show: true, fontSize: 11, formatter: (p: unknown) => `${(p as { data: number[] }).data[2]}%` },
      itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
    }],
    grid: { left: 100, right: 20, top: 10, bottom: 80 },
  } : null;

  const topHoldingsChartOption = topHoldings.length > 0 ? {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const p = params as { data: number; name: string }[];
        if (Array.isArray(p) && p.length > 0) {
          return `${p[0].name}: ${p[0].data.toFixed(2)}%`;
        }
        return '';
      },
    },
    xAxis: {
      type: 'value' as const,
      axisLabel: { formatter: (v: number) => `${v.toFixed(1)}%`, fontSize: 12, color: tc.inkMuted },
      splitLine: { lineStyle: { color: tc.divider } },
    },
    yAxis: {
      type: 'category' as const,
      data: topHoldings.slice(0, 15).reverse().map(h => h.name.length > 25 ? h.name.slice(0, 22) + '...' : h.name),
      axisLabel: { fontSize: 11, color: tc.inkBody },
    },
    series: [{
      type: 'bar' as const,
      data: topHoldings.slice(0, 15).reverse().map(h => Math.round(h.exposure_pct * 100) / 100),
      itemStyle: {
        borderRadius: [0, 4, 4, 0],
        color: (params: { dataIndex: number }) => {
          const val = topHoldings.slice(0, 15).reverse()[params.dataIndex]?.exposure_pct ?? 0;
          if (val >= 10) return tc.claret;
          if (val >= 5) return tc.gold;
          return tc.forest;
        },
      },
      barMaxWidth: 20,
    }],
    grid: { left: 120, right: 20, top: 10, bottom: 30 },
  } : null;

  const treemapChartOption = treemapData.length > 0 ? {
    tooltip: {
      formatter: (params: unknown) => {
        const p = params as { name: string; value: number; treePathInfo: { name: string }[] };
        const path = p.treePathInfo?.map(n => n.name).filter(Boolean).join(' → ') || p.name;
        return `${path}<br/>${fmt(p.value)}`;
      },
    },
    series: [{
      type: 'treemap' as const,
      data: treemapData,
      width: '100%',
      height: '100%',
      roam: false,
      nodeClick: 'zoomToNode' as const,
      breadcrumb: {
        show: true,
        itemStyle: { color: tc.divider, borderColor: tc.divider, textStyle: { color: tc.inkBody } },
      },
      levels: [
        { // ETF level
          itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 3, gapWidth: 3 },
          upperLabel: { show: true, height: 24, fontSize: 13, fontWeight: 600, color: tc.parchment },
        },
        { // Sector level
          itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2, gapWidth: 2 },
          upperLabel: { show: true, height: 20, fontSize: 11, color: tc.parchment },
        },
        { // Stock level
          itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 1 },
          label: { show: true, fontSize: 10, color: tc.parchment, formatter: (p: unknown) => {
            const n = (p as { name: string }).name;
            return n.length > 15 ? n.slice(0, 12) + '...' : n;
          }},
        },
      ],
      label: { show: true, fontSize: 11, color: tc.parchment },
      upperLabel: { show: true, color: tc.parchment },
    }],
  } : null;

  const currencyChartOption = currencies.length > 0 ? {
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
      data: currencies.map(c => ({ name: c.currency, value: Math.round(c.value * 100) / 100 })),
      label: { fontSize: 12, color: tc.inkBody, formatter: (p: unknown) => {
        const entry = p as { name: string; percent: number };
        return `${entry.name} ${entry.percent.toFixed(1)}%`;
      }},
      itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
    }],
    graphic: [{ type: 'text', left: 'center', top: 'center', style: { text: `${currencies.length} currencies`, fontSize: 13, fontWeight: 600, fill: tc.inkMuted } }],
  } : null;

  const filteredDrawdown = riskData?.risk.drawdown_series ? filterByPeriod(riskData.risk.drawdown_series, drawdownPeriod) : [];
  // Per-window peak-to-trough so the Max Drawdown KPI matches the chart's
  // selected period. drawdown_series carries the running "% below peak"
  // per snapshot; the min in the filtered window IS the window's max DD,
  // the date at that index is the trough, and the most recent zero-or-
  // above-zero drawdown before it is the peak the trough fell from.
  const windowDDStats = (() => {
    if (drawdownPeriod === 'All' && riskData) {
      return {
        pct: riskData.risk.max_drawdown,
        start: riskData.risk.max_drawdown_start,
        end: riskData.risk.max_drawdown_end,
        days: riskData.risk.max_drawdown_days,
      };
    }
    if (filteredDrawdown.length === 0) return { pct: 0, start: '', end: '', days: 0 };
    let troughIdx = 0;
    for (let i = 1; i < filteredDrawdown.length; i++) {
      if (filteredDrawdown[i].drawdown < filteredDrawdown[troughIdx].drawdown) troughIdx = i;
    }
    const trough = filteredDrawdown[troughIdx];
    // Walk back to the most recent drawdown >= 0 (running peak). If none in
    // window, fall back to the window's first date and prefix with ≥ in the
    // caption so the user knows the peak predates the window.
    let peakIdx = 0;
    let peakKnown = false;
    for (let i = troughIdx; i >= 0; i--) {
      if (filteredDrawdown[i].drawdown >= 0) { peakIdx = i; peakKnown = true; break; }
    }
    const peakDate = filteredDrawdown[peakIdx].date;
    const days = Math.max(0, Math.round((new Date(trough.date).getTime() - new Date(peakDate).getTime()) / 86_400_000));
    return {
      pct: -trough.drawdown,
      start: (peakKnown ? '' : '≥') + peakDate,
      end: trough.date,
      days,
    };
  })();
  const drawdownChartOption = filteredDrawdown.length > 0 ? {
    tooltip: {
      trigger: 'axis' as const,
      formatter: (params: unknown) => {
        const p = params as { data: number; axisValue: string }[];
        if (!Array.isArray(p) || p.length === 0) return '';
        return `${p[0].axisValue}<br/>Drawdown: ${p[0].data.toFixed(2)}%`;
      },
    },
    xAxis: {
      type: 'category' as const,
      data: filteredDrawdown.map(d => d.date),
      axisLabel: {
        fontSize: 10,
        color: tc.inkMuted,
        interval: Math.max(Math.floor(filteredDrawdown.length / 6) - 1, 1),
        rotate: filteredDrawdown.length > 60 ? 30 : 0,
        formatter: (v: string) => formatDateForPeriod(v, drawdownPeriod),
      },
      axisLine: { show: false },
      axisTick: { show: false },
      boundaryGap: false,
    },
    yAxis: {
      type: 'value' as const,
      max: 0,
      axisLabel: { formatter: (v: number) => `${v}%`, fontSize: 11, color: tc.inkMuted },
      splitLine: { lineStyle: { color: tc.divider } },
    },
    series: [{
      type: 'line' as const,
      data: filteredDrawdown.map(d => d.drawdown),
      areaStyle: { color: 'rgba(255, 59, 48, 0.12)' },
      lineStyle: { color: tc.claret, width: 1.5 },
      itemStyle: { color: tc.claret },
      symbol: 'none',
    }],
    grid: { left: 50, right: 16, top: 16, bottom: filteredDrawdown.length > 60 ? 52 : 36 },
  } : null;

  const hasData = sectorEntries.length > 0 || countryEntries.length > 0;

  return (
    <div className="space-y-6">
      {!defaultTab && <TabBar tabs={ANALYSIS_TABS_SLIM} activeTab={activeTab} onTabChange={setActiveTab} />}

      {/* Risk metrics dashboard */}
      {activeTab === 'risk' && <>

      {/* --- RISK TAB --- */}
      {riskData && riskData.risk.annualized_volatility > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Risk Analytics</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Portfolio risk metrics computed from daily valuations.
            {riskData.risk.current_drawdown > 0 && (
              <span className="text-claret ml-1">
                Currently {riskData.risk.current_drawdown.toFixed(1)}% below all-time high ({fmt(riskData.risk.all_time_high)} on {riskData.risk.ath_date}).
              </span>
            )}
          </p>

          {/* KPI grid */}
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3 mb-4">
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Volatility (ann.)</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{riskData.risk.annualized_volatility.toFixed(2)}%</p>
              {riskData.benchmark_risk && (
                <p className="text-[11px] text-ink-muted mt-0.5">Benchmark: {riskData.benchmark_risk.annualized_volatility.toFixed(2)}%</p>
              )}
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Sharpe Ratio</p>
              <p className={`font-serif text-[20px] font-semibold tabular-nums ${riskData.risk.sharpe_ratio >= 0.5 ? 'text-sage' : riskData.risk.sharpe_ratio >= 0 ? 'text-ink' : 'text-claret'}`}>
                {riskData.risk.sharpe_ratio.toFixed(2)}
              </p>
              {riskData.benchmark_risk && (
                <p className="text-[11px] text-ink-muted mt-0.5">Benchmark: {riskData.benchmark_risk.sharpe_ratio.toFixed(2)}</p>
              )}
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Sortino Ratio</p>
              <p className={`font-serif text-[20px] font-semibold tabular-nums ${riskData.risk.sortino_ratio >= 0.5 ? 'text-sage' : riskData.risk.sortino_ratio >= 0 ? 'text-ink' : 'text-claret'}`}>
                {riskData.risk.sortino_ratio.toFixed(2)}
              </p>
              {riskData.benchmark_risk && (
                <p className="text-[11px] text-ink-muted mt-0.5">Benchmark: {riskData.benchmark_risk.sortino_ratio.toFixed(2)}</p>
              )}
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Max Drawdown{drawdownPeriod !== 'All' && <span className="text-ink-muted"> ({drawdownPeriod})</span>}</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">
                -{windowDDStats.pct.toFixed(2)}%
              </p>
              <p className="text-[11px] text-ink-muted mt-0.5">
                {windowDDStats.start && windowDDStats.end
                  ? `${windowDDStats.start} to ${windowDDStats.end} (${windowDDStats.days}d)`
                  : '—'}
              </p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">VaR (95%, 1-day)</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">
                {fmt(riskData.risk.value_at_risk_95)}
              </p>
              <p className="text-[11px] text-ink-muted mt-0.5">Expected worst daily loss</p>
            </div>
          </div>

          {/* Drawdown chart */}
          {drawdownChartOption && (
            <div>
              <div className="flex items-center justify-between mb-2 px-1 md:px-0">
                <h3 className="font-serif text-heading text-ink">Drawdown Over Time</h3>
                <PeriodSelector value={drawdownPeriod} onChange={setDrawdownPeriod} />
              </div>
              <EChartWrapper option={drawdownChartOption} height="220px" />
            </div>
          )}

          {/* Rolling volatility */}
          {riskData?.rolling && Object.keys(riskData.rolling).length > 0 && (() => {
            const windows = ['30d', '90d', '365d'].filter(w => riskData.rolling?.[w]?.length);
            if (windows.length === 0) return null;
            const cutoff = periodCutoff(rollingVolPeriod);
            // Each series carries its own [timestamp, vol] pairs. The chart
            // uses a time-based x-axis so series with different start dates
            // render cleanly side-by-side. Previously we union'd dates across
            // windows into a category axis and mapped each window via
            // byDate[d]?.[w]?.volatility ?? null, which:
            //   1. correctly clipped pre-start (good — null gap, line breaks)
            //   2. but ALSO introduced mid-range nulls because the backend
            //      step-samples each window at window/10 days, so the 90d
            //      and 365d series had null at every intermediate union
            //      date — line broke into many short segments.
            // Per-series time pairs eliminate the union-null mid-range issue
            // while keeping the leading-edge clip (no pre-window-complete
            // data is emitted by the backend, so each series naturally
            // starts where it has a full window).
            const seriesData = windows.map(w => {
              const points = riskData.rolling![w]
                .filter(p => !cutoff || new Date(p.date) >= cutoff)
                .map(p => [new Date(p.date).getTime(), p.volatility] as [number, number]);
              return { window: w, points };
            });
            const anyData = seriesData.some(s => s.points.length > 0);
            if (!anyData) return null;
            return (
              <div className="mt-4">
                <div className="flex items-center justify-between mb-2 px-1 md:px-0">
                  <h3 className="font-serif text-heading text-ink">Rolling Volatility</h3>
                  <PeriodSelector value={rollingVolPeriod} onChange={setRollingVolPeriod} />
                </div>
                <EChartWrapper option={{
                  tooltip: { trigger: 'axis' as const },
                  legend: { data: windows.map(w => `Vol ${w}`), bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
                  xAxis: {
                    type: 'time' as const,
                    axisLabel: { fontSize: 11, color: tc.inkMuted },
                    axisLine: { show: false },
                    axisTick: { show: false },
                  },
                  yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => `${v}%`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
                  series: seriesData.map((s, i) => ({
                    name: `Vol ${s.window}`,
                    type: 'line' as const,
                    data: s.points,
                    smooth: 0.3,
                    showSymbol: false,
                    // connectNulls explicit — defensive belt-and-braces. Each
                    // series has no nulls (per-series pairs only include
                    // dates with actual values), so the leading-edge clip
                    // comes from each series simply starting where the
                    // backend's first emitted point lands. Setting to false
                    // also guarantees no fake leading line if the
                    // shape ever changes upstream.
                    connectNulls: false,
                    lineStyle: { width: i === 0 ? 2 : 1.5, type: i === 2 ? ('dashed' as const) : ('solid' as const) },
                  })),
                  grid: { left: 45, right: 16, top: 10, bottom: 40 },
                }} height="200px" />
              </div>
            );
          })()}
        </div>
      )}

      {/* Volatility Context */}
      {volContext && volContext.elevated && volContext.message && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h3 className="font-serif text-heading text-ink mb-2">Market Context</h3>
          <div className="rounded-xl bg-inset border-l-[3px] border-amber px-3 py-2.5 mb-3">
            <p className="text-[15px] text-ink">{volContext.message}</p>
          </div>
          <div className="grid grid-cols-3 gap-2 mb-3">
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">Drawdown</p>
              <p className={`text-[13px] font-semibold tabular-nums ${volContext.drawdown_pct < -5 ? 'text-claret' : 'text-ink'}`}>
                {volContext.drawdown_pct.toFixed(1)}%
              </p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">30d Volatility</p>
              <p className={`text-[13px] font-semibold tabular-nums ${volContext.vol_30d > 25 ? 'text-amber' : 'text-ink'}`}>
                {volContext.vol_30d.toFixed(0)}%
              </p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">Past Recoveries</p>
              <p className="text-[13px] font-semibold tabular-nums text-ink">{volContext.past_drawdowns?.length || 0}</p>
            </div>
          </div>
          {volContext.past_drawdowns && volContext.past_drawdowns.length > 0 && (
            <div className="space-y-1">
              <p className="text-[12px] font-medium text-ink-muted">Past Drawdown Recoveries</p>
              {volContext.past_drawdowns.slice(-5).map((ep, i) => (
                <div key={i} className="flex items-center justify-between text-[12px]">
                  <span className="text-ink">{ep.from_date.slice(0, 7)} → {ep.trough_date.slice(0, 7)}</span>
                  <span className="tabular-nums">
                    <span className="text-claret">{ep.max_drawdown_pct}%</span>
                    <span className="text-ink-muted mx-1">·</span>
                    <span className="text-sage">{ep.recovery_days}d recovery</span>
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Sparplan Consistency Tracker */}
      {sparplanStreak && sparplanStreak.total_months > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h3 className="font-serif text-heading text-ink mb-2">Sparplan Consistency</h3>
          <div className="grid grid-cols-2 md:grid-cols-4 gap-2 mb-3">
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">Current Streak</p>
              <p className="text-[13px] font-semibold tabular-nums text-sage">{sparplanStreak.current_streak} months</p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">Longest Streak</p>
              <p className="text-[13px] font-semibold tabular-nums text-ink">{sparplanStreak.longest_streak} months</p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">Consistency</p>
              <p className={`text-[13px] font-semibold tabular-nums ${sparplanStreak.consistency_pct >= 90 ? 'text-sage' : sparplanStreak.consistency_pct >= 70 ? 'text-amber' : 'text-claret'}`}>
                {sparplanStreak.consistency_pct}%
              </p>
            </div>
            <div className="rounded-lg bg-parchment-deep p-3">
              <p className="text-[11px] text-ink-muted">Missed Cost</p>
              <p className="text-[13px] font-semibold tabular-nums text-claret">
                {sparplanStreak.missed_cost > 0 ? fmt(sparplanStreak.missed_cost) : '—'}
              </p>
            </div>
          </div>
          <p className="text-[12px] text-ink-muted">
            {sparplanStreak.active_months} of {sparplanStreak.total_months} months invested
            ({sparplanStreak.missed_months} missed) · Avg {fmt(sparplanStreak.avg_monthly)}/mo · Total {fmt(sparplanStreak.total_invested)}
          </p>
        </div>
      )}

      {/* Crisis Stress Test */}
      {stressTest && stressTest.portfolio_impact && stressTest.portfolio_impact.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h3 className="font-serif text-heading text-ink mb-2 px-1 md:px-0">Historical Crisis Stress Test</h3>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            How your current portfolio ({fmt(stressTest.portfolio_value)}, {stressTest.equity_pct}% equity) would have performed in past crises.
          </p>

          <div className="overflow-x-auto px-1 md:px-0">
            <table className="w-full text-[13px] min-w-[700px]">
              <thead>
                <tr className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] border-b border-divider">
                  <th className="text-left py-2 font-medium">Crisis</th>
                  <th className="text-right py-2 font-medium">Drawdown</th>
                  <th className="text-right py-2 font-medium">Impact</th>
                  <th className="text-right py-2 font-medium">Worst Month</th>
                  <th className="text-right py-2 font-medium">Recovery</th>
                  <th className="text-right py-2 font-medium">With DCA</th>
                </tr>
              </thead>
              <tbody>
                {[...stressTest.portfolio_impact]
                  .sort((a, b) => a.drawdown_pct - b.drawdown_pct)
                  .map((impact, i) => {
                    const scenario = stressTest.scenarios.find(s => s.name === impact.name);
                    const dca = stressTest.dca_recovery?.find(d => d.name === impact.name);
                    return (
                      <tr key={i} className="border-b border-divider">
                        <td className="py-2">
                          <p className="text-ink font-medium">{impact.name}</p>
                          <p className="text-[11px] text-ink-muted">{scenario?.period}</p>
                        </td>
                        <td className="py-2 text-right tabular-nums text-claret font-medium">{impact.drawdown_pct.toFixed(1)}%</td>
                        <td className="py-2 text-right tabular-nums text-claret">{fmt(impact.drawdown_eur)}</td>
                        <td className="py-2 text-right tabular-nums text-claret">{impact.worst_month_pct.toFixed(1)}%</td>
                        <td className="py-2 text-right tabular-nums text-ink-muted">{impact.recovery_months} mo</td>
                        <td className="py-2 text-right tabular-nums text-sage">{dca ? `${dca.with_dca_months} mo` : '—'}{dca && dca.acceleration_months > 0 ? <span className="text-[11px] text-sage/60 ml-1">(-{dca.acceleration_months})</span> : ''}</td>
                      </tr>
                    );
                  })}
              </tbody>
            </table>
          </div>

          {/* Cash buffer adequacy */}
          <div className="mt-4 grid grid-cols-2 md:grid-cols-3 gap-2 px-1 md:px-0">
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Cash Buffer</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{fmt(stressTest.cash_buffer)}</p>
              <p className="text-[11px] text-ink-muted">{stressTest.cash_buffer_months} months at 5% withdrawal</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Worst-Case Loss</p>
              {(() => { const worst = [...stressTest.portfolio_impact].sort((a, b) => a.drawdown_eur - b.drawdown_eur)[0]; return (<><p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(worst?.drawdown_eur || 0)}</p><p className="text-[11px] text-ink-muted">{worst?.name || 'worst case'}</p></>); })()}
            </div>
            <div className="rounded-xl bg-parchment-deep p-3 text-center">
              <p className="text-[12px] text-ink-muted mb-1">Equity Exposure</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{stressTest.equity_pct}%</p>
              <p className="text-[11px] text-ink-muted">of portfolio in stocks/ETFs</p>
            </div>
          </div>

          {/* Per-holding impact (worst crisis) */}
          {stressTest.holding_impacts && stressTest.holding_impacts.length > 0 && (
            <div className="mt-4 px-1 md:px-0">
              <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Per-Holding Impact (2008 GFC Scenario)</p>
              <div className="space-y-1">
                {stressTest.holding_impacts
                  .filter(h => h.value > 0)
                  .sort((a, b) => a.drawdown_eur - b.drawdown_eur)
                  .map(h => (
                    <div key={h.isin} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-1.5">
                      <div className="min-w-0 flex-1">
                        <p className="text-[13px] text-ink truncate">{h.name}</p>
                        <p className="text-[11px] text-ink-muted">{h.is_equity ? 'Equity' : 'Non-equity'} · {fmt(h.value)}</p>
                      </div>
                      <p className={`text-[13px] tabular-nums font-medium shrink-0 ml-3 ${h.drawdown_eur < 0 ? 'text-claret' : 'text-ink-muted'}`}>
                        {h.drawdown_eur < 0 ? fmt(h.drawdown_eur) : 'No impact'}
                      </p>
                    </div>
                  ))}
              </div>
            </div>
          )}

        </div>
      )}

      {/* Stress-Adjusted Suggestions */}
      {stressTest && stressTest.portfolio_impact && (() => {
        const suggestions: { title: string; detail: string; severity: 'high' | 'medium' | 'low' }[] = [];
        const eqPct = stressTest.equity_pct;
        const worstImpact = [...stressTest.portfolio_impact].sort((a, b) => a.drawdown_pct - b.drawdown_pct)[0];
        const worstDrawdown = worstImpact?.drawdown_pct ?? 0;
        const avgRecovery = stressTest.portfolio_impact.reduce((s, i) => s + i.recovery_months, 0) / stressTest.portfolio_impact.length;
        const cashMonths = stressTest.cash_buffer_months || 0;

        // High equity correlation — all crises hit similarly hard
        if (eqPct > 90) {
          suggestions.push({ title: 'High equity concentration', detail: `${eqPct}% in equities means nearly full exposure to every crisis. Adding 10-20% in bonds or gold could reduce worst-case drawdown by 5-10 percentage points without significantly impacting long-term returns.`, severity: 'high' });
        } else if (eqPct > 80) {
          suggestions.push({ title: 'Consider diversifiers', detail: `At ${eqPct}% equity, your portfolio tracks major indices closely. A 5-10% allocation to bonds, gold, or REITs could smooth volatility.`, severity: 'medium' });
        }

        // Cash buffer inadequacy
        if (cashMonths < 3) {
          suggestions.push({ title: 'Build emergency cash buffer', detail: `Only ${cashMonths} months of expenses in cash. A 3-6 month buffer prevents forced selling during drawdowns.`, severity: 'high' });
        } else if (cashMonths < 6) {
          suggestions.push({ title: 'Strengthen cash buffer', detail: `${cashMonths} months of expenses in cash. Consider building to 6 months for full crisis resilience.`, severity: 'low' });
        }

        // Severe worst-case
        if (worstDrawdown < -40) {
          suggestions.push({ title: 'Severe drawdown exposure', detail: `Worst case: ${worstDrawdown.toFixed(0)}% drawdown (${fmt(worstImpact?.drawdown_eur ?? 0)}). If this would force you to sell, consider reducing equity allocation or building a larger cash buffer.`, severity: 'medium' });
        }

        // Long recovery times
        if (avgRecovery > 30) {
          suggestions.push({ title: 'Long average recovery', detail: `Average recovery across crises: ${Math.round(avgRecovery)} months. Continued DCA during drawdowns significantly accelerates recovery — do not stop investing during crashes.`, severity: 'low' });
        }

        if (suggestions.length === 0) return null;

        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h3 className="font-serif text-heading text-ink mb-2 px-1 md:px-0">Stress-Adjusted Suggestions</h3>
            <div className="space-y-1.5 px-1 md:px-0">
              {suggestions.map((s, i) => (
                <div key={i} className={`flex items-start gap-2.5 rounded-xl px-3 py-2.5 ${
                  s.severity === 'high' ? 'bg-inset border-l-[3px] border-claret' : s.severity === 'medium' ? 'bg-inset border-l-[3px] border-amber' : 'bg-inset border-l-[3px] border-sage'
                }`}>
                  <span className={`w-2.5 h-2.5 rounded-full shrink-0 mt-1 ${
                    s.severity === 'high' ? 'bg-claret' : s.severity === 'medium' ? 'bg-amber' : 'bg-sage'
                  }`} />
                  <div>
                    <p className="text-[13px] font-medium text-ink">{s.title}</p>
                    <p className="text-[12px] text-ink-muted mt-0.5">{s.detail}</p>
                  </div>
                </div>
              ))}
            </div>
          </div>
        );
      })()}

      {/* Decision Journal */}
      <div className="border-t border-divider pt-6 py-3 md:py-5">
        <h3 className="font-serif text-heading text-ink mb-2">Decision Journal</h3>
        <p className="text-[13px] text-ink-muted mb-3">Record why you made investment decisions. Review them later to learn from your reasoning.</p>

        <div className="space-y-2 mb-3">
          <div className="flex gap-2">
            <select aria-label="Decision type" value={journalType} onChange={e => setJournalType(e.target.value)}
              className="shrink-0 rounded-[8px] border border-divider bg-parchment px-2 py-1.5 text-[12px]">
              <option value="buy">Buy</option>
              <option value="sell">Sell</option>
              <option value="rebalance">Rebalance</option>
              <option value="tax_harvest">Tax Harvest</option>
              <option value="allocation_change">Allocation Change</option>
              <option value="other">Other</option>
            </select>
            <input aria-label="Decision reason" type="text" value={journalReason} onChange={e => setJournalReason(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter' && journalReason.trim()) {
                fetch('/api/analysis/journal', { method: 'POST', headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ action_type: journalType, reason: journalReason }) })
                  .then(() => { setJournalReason(''); fetch('/api/analysis/journal').then(r => r.json()).then(setJournal); });
              }}}
              placeholder="Why are you making this decision?"
              className="flex-1 min-w-0 rounded-[8px] border border-divider bg-parchment px-3 py-1.5 text-[12px] placeholder:text-ink-muted" />
          </div>
          <button onClick={() => {
            if (!journalReason.trim()) return;
            fetch('/api/analysis/journal', { method: 'POST', headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ action_type: journalType, reason: journalReason }) })
              .then(() => { setJournalReason(''); fetch('/api/analysis/journal').then(r => r.json()).then(setJournal); });
          }} disabled={!journalReason.trim()} className="apple-btn-secondary w-full">
            Save to Journal
          </button>
        </div>

        {journal && journal.entries.length > 0 && (
          <div className="space-y-2">
            {journal.entries.slice(0, 10).map(e => {
              const typeLabels: Record<string, string> = { buy: 'Buy', sell: 'Sell', rebalance: 'Rebalance', tax_harvest: 'Tax Harvest', allocation_change: 'Allocation', other: 'Other' };
              const isOld = e.days_ago > 90;
              return (
                <div key={e.id} className={`rounded-xl px-3 py-2 ${isOld ? 'bg-parchment-deep border border-amber' : 'bg-parchment-deep'}`}>
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="text-[12px] font-medium text-ink">{typeLabels[e.action_type] || e.action_type}</span>
                    <span className="text-[11px] text-ink-muted">{e.date} ({e.days_ago}d ago)</span>
                  </div>
                  <p className="text-[12px] text-ink-muted mt-0.5">{e.reason}</p>
                  {e.outcome && <p className="text-[12px] text-sage mt-0.5">Outcome: {e.outcome}</p>}
                  {isOld && !e.outcome && (
                    <p className="text-[11px] text-amber mt-1">Time for a retrospective — how did this decision play out?</p>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>

      </>}

      {/* --- TAX TAB --- */}
      {activeTab === 'tax' && <>

      {/* Sub-tab navigation — splits the tax page (7 sections, ~7 screens) into
          Overview/Tools/Filing so each view fits roughly 2 screens. */}
      <TabBar
        tabs={TAX_TABS}
        activeTab={taxSubTab}
        onTabChange={(t) => { setTaxSubTab(t); localStorage.setItem('tax_subtab', t); }}
      />

      {taxSubTab === 'overview' && (<>
      {/* Empty-state guard — when the user has no taxable events for the
          selected year (no realized gains/losses, no dividends), the
          Overview section would otherwise render nothing. */}
      {(!taxData || !taxData.summary || (taxData.summary.realized_gains <= 0 && taxData.summary.realized_losses >= 0 && taxData.summary.dividend_income <= 0)) && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-2 px-1 md:px-0">Tax Overview</h2>
          <p className="text-[13px] text-ink-muted mb-3 px-1 md:px-0">
            German Abgeltungssteuer (26.375%) with Teilfreistellung for equity ETFs.
          </p>
          <div className="rounded-lg bg-parchment-deep border border-divider p-4 text-[13px] text-ink-muted">
            No realized gains, losses, or dividend income recorded for the current tax year.
            Once you sell positions or receive dividends, this view will show your projected
            Abgeltungssteuer liability and FSA utilization.
            <span className="block mt-2 text-ink-muted">Use the <span className="text-forest font-medium">Tools</span> tab to preview the tax impact of a hypothetical sale.</span>
          </div>
        </div>
      )}
      {/* Tax Summary */}
      {taxData && taxData.summary && (taxData.summary.realized_gains > 0 || taxData.summary.realized_losses < 0 || taxData.summary.dividend_income > 0) && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-3 md:mb-4 px-1 md:px-0">
            <div>
              <h2 className="font-serif text-heading text-ink">Tax Overview</h2>
              <p className="text-[13px] text-ink-muted mt-0.5">
                German Abgeltungssteuer (26.375%) with Teilfreistellung for equity ETFs
              </p>
            </div>
            <div className="flex items-center gap-2">
              {taxData.available_years && taxData.available_years.length > 1 && (
                <select
                  aria-label="Tax year"
                  value={taxYear}
                  onChange={e => {
                    const y = parseInt(e.target.value);
                    setTaxYear(y);
                    api.getTax(y).then(setTaxData).catch(() => {});
                  }}
                  className="rounded-[8px] border border-divider bg-parchment-deep text-ink px-2 py-1 text-[15px]"
                >
                  {taxData.available_years.map(y => (
                    <option key={y} value={y}>{y}</option>
                  ))}
                </select>
              )}
              <a
                href={`/api/analysis/export-tax?year=${taxYear}`}
                className="rounded-[8px] bg-parchment-deep px-3 py-1 text-[13px] font-medium text-ink-body hover:bg-divider"
                download
              >Export CSV</a>
            </div>
          </div>

          {/* Tax KPIs */}
          <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-3 mb-4">
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Realized Gains</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-sage">{fmt(taxData.summary.realized_gains)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Realized Losses</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(taxData.summary.realized_losses)}</p>
            </div>
            {taxData.summary.dividend_income > 0 && (
              <div className="rounded-xl bg-parchment-deep p-3">
                <p className="text-[12px] text-ink-muted mb-1">Dividend Income</p>
                <p className="font-serif text-[20px] font-semibold tabular-nums text-forest">{fmt(taxData.summary.dividend_income)}</p>
              </div>
            )}
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Net Taxable</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{fmt(taxData.summary.taxable_gain)}</p>
              {taxData.summary.teilfreistellung_amt > 0 && (
                <p className="text-[11px] text-ink-muted mt-0.5">
                  After {fmt(taxData.summary.teilfreistellung_amt)} Teilfreistellung
                </p>
              )}
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Estimated Tax</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(taxData.summary.estimated_tax)}</p>
              <p className="text-[11px] text-ink-muted mt-0.5">
                Effective rate: {taxData.summary.effective_rate}%
              </p>
            </div>
          </div>

          {/* Freistellungsauftrag progress */}
          <div className="mb-4 px-1 md:px-0">
            <div className="flex items-center justify-between mb-1">
              <span className="text-[15px] font-medium text-ink" title="Annual tax-free allowance for capital income (1,000 EUR)">Sparerpauschbetrag</span>
              <span className="text-[13px] tabular-nums text-ink-muted">
                {fmt(taxData.summary.freistellung_used)} / {fmt(1000)} used
              </span>
            </div>
            <div className="h-2.5 rounded-full bg-parchment-deep overflow-hidden">
              <div
                className={`h-full rounded-full transition-all duration-300 ${
                  taxData.summary.freistellung_used >= 1000 ? 'bg-claret' :
                  taxData.summary.freistellung_used >= 800 ? 'bg-amber' : 'bg-sage'
                }`}
                style={{ width: `${Math.min((taxData.summary.freistellung_used / 1000) * 100, 100)}%` }}
              />
            </div>
            <p className="text-[11px] text-ink-muted mt-1">
              {taxData.summary.freistellung_remaining > 0
                ? `${fmt(taxData.summary.freistellung_remaining)} remaining tax-free allowance`
                : 'Fully utilized — all gains above this are taxed'}
            </p>
          </div>

          {/* Dividend income */}
          {taxData.summary.dividend_income > 0 && (
            <div className="mb-4 px-1 md:px-0">
              <p className="text-[15px] font-medium text-ink mb-1">
                Dividend Income: <span className="text-sage">{fmt(taxData.summary.dividend_income)}</span>
              </p>
            </div>
          )}

          {/* FSA Per-Account Breakdown */}
          {fsaStatus && fsaStatus.accounts.length > 0 && (
            <div className="mb-4 px-1 md:px-0">
              <div className="flex items-baseline justify-between mb-2 gap-2 flex-wrap">
                <p className="text-[15px] font-medium text-ink">Freistellungsauftrag <span className="text-ink-muted font-normal">(tax-free allowance allocation)</span></p>
                <label className="flex items-center gap-1.5 text-[12px] text-ink-body cursor-pointer select-none" title="Married filing jointly (Zusammenveranlagung) — Sparerpauschbetrag doubles to 2.000 €">
                  <input type="checkbox" checked={fsaJoint} onChange={e => setFsaJoint(e.target.checked)} className="accent-forest" />
                  Joint filing (×2)
                </label>
              </div>
              <div className="space-y-1.5">
                {fsaStatus.accounts.map(a => (
                  <div key={a.account_name} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-2">
                    <div>
                      <p className="text-[13px] font-medium text-ink">{a.account_name}</p>
                      <p className="text-[11px] text-ink-muted">
                        {a.realized_gains > 0 && `Gains ${fmt(a.realized_gains)} `}
                        {a.dividends > 0 && `Div ${fmt(a.dividends)} `}
                        {a.interest > 0 && `Interest ${fmt(a.interest)}`}
                      </p>
                    </div>
                    <span className="text-[13px] font-semibold tabular-nums">{fmt(a.total_income)}</span>
                  </div>
                ))}
              </div>
              <p className="text-[11px] text-ink-muted mt-2">
                Utilization: {fsaStatus.utilization_pct}% ({fmt(fsaStatus.used)} of {fmt(fsaStatus.allowance)})
                {fsaStatus.remaining > 0 && ` · ${fmt(fsaStatus.remaining)} unused = ${fmt(fsaStatus.remaining * 0.26375)} forfeited tax savings`}
              </p>
              {fsaStatus.recommendation && fsaStatus.recommendation.length > 1 && (
                <div className="mt-2 p-2 rounded-lg bg-inset border-l-[3px] border-forest">
                  <p className="text-[11px] font-medium text-forest mb-1">Recommended FSA Split for {fsaStatus.year + 1}</p>
                  {fsaStatus.recommendation.map(r => (
                    <div key={r.account} className="flex items-center justify-between text-[11px]">
                      <span className="text-ink-body">{r.account}</span>
                      <span className="tabular-nums text-ink font-medium">{fmt(r.recommended_fsa)} (projected {fmt(r.projected_income)})</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Vorabpauschale */}
          {taxData.vorabpauschale && taxData.vorabpauschale.length > 0 && (
            <div className="mb-4">
              <p className="text-[15px] font-medium text-ink mb-2">Vorabpauschale <span className="text-ink-muted font-normal">(advance flat-rate tax on accumulating ETFs)</span></p>
              {/* Mobile: card layout */}
              <div className="md:hidden space-y-2">
                {taxData.vorabpauschale.map(vp => (
                  <div key={vp.isin} className="rounded-lg bg-parchment-deep px-3 py-2">
                    <div className="flex items-center gap-1.5 mb-1.5">
                      <span className={`w-2 h-2 rounded-full shrink-0 ${vp.tax_on_vp <= 0 ? 'bg-sage' : vp.tax_on_vp < 50 ? 'bg-amber' : 'bg-claret'}`} />
                      <p className="text-[13px] font-medium text-ink truncate">{vp.name}</p>
                    </div>
                    <div className="grid grid-cols-2 gap-x-3 gap-y-0.5 text-[11px] tabular-nums">
                      <span className="text-ink-muted">Jan 1 Value</span><span className="text-right">{fmt(vp.jan1_value)}</span>
                      <span className="text-ink-muted">Basiszins</span><span className="text-right">{vp.basiszins}%</span>
                      <span className="text-ink-muted">Basisertrag</span><span className="text-right">{fmt(vp.basisertrag)}</span>
                      <span className="text-ink-muted">Vorabpauschale</span><span className="text-right font-medium">{fmt(vp.vorabpauschale)}</span>
                      <span className="text-ink-muted">Tax</span><span className="text-right text-claret">{fmt(vp.tax_on_vp)}</span>
                    </div>
                  </div>
                ))}
              </div>
              {/* Desktop: table */}
              <div className="hidden md:block overflow-x-auto">
                <table className="w-full text-[12px]">
                  <thead>
                    <tr className="text-left text-ink-muted border-b border-divider">
                      <th className="pb-1.5 font-medium">ETF</th>
                      <th className="pb-1.5 font-medium text-right">Jan 1 Value</th>
                      <th className="pb-1.5 font-medium text-right">Basiszins</th>
                      <th className="pb-1.5 font-medium text-right">Basisertrag</th>
                      <th className="pb-1.5 font-medium text-right">Vorabpauschale</th>
                      <th className="pb-1.5 font-medium text-right">Tax</th>
                    </tr>
                  </thead>
                  <tbody>
                    {taxData.vorabpauschale.map(vp => (
                      <tr key={vp.isin} className="border-b border-divider">
                        <td className="py-1.5 font-medium text-ink truncate max-w-[200px]">
                          <span className={`inline-block w-2 h-2 rounded-full mr-1.5 ${vp.tax_on_vp <= 0 ? 'bg-sage' : vp.tax_on_vp < 50 ? 'bg-amber' : 'bg-claret'}`} />
                          {vp.name}
                        </td>
                        <td className="py-1.5 text-right tabular-nums">{fmt(vp.jan1_value)}</td>
                        <td className="py-1.5 text-right tabular-nums">{vp.basiszins}%</td>
                        <td className="py-1.5 text-right tabular-nums">{fmt(vp.basisertrag)}</td>
                        <td className="py-1.5 text-right tabular-nums font-medium">{fmt(vp.vorabpauschale)}</td>
                        <td className="py-1.5 text-right tabular-nums text-claret">{fmt(vp.tax_on_vp)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Tax-loss harvesting hints */}
          {taxData.summary.tax_loss_hints && taxData.summary.tax_loss_hints.length > 0 && (
            <div className="px-1 md:px-0">
              <p className="text-[15px] font-medium text-ink mb-2">Tax-Loss Harvesting Opportunities</p>
              <div className="space-y-1.5">
                {taxData.summary.tax_loss_hints.map(h => (
                  <div key={h.isin} className="flex items-center justify-between rounded-lg bg-inset border-l-[3px] border-amber px-3 py-2">
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium text-ink truncate">{h.name}</p>
                      <p className="text-[11px] text-ink-muted">
                        Unrealized loss: <span className="text-claret">{fmt(h.unrealized_pl)}</span>
                      </p>
                    </div>
                    <span className="text-[13px] text-sage font-medium shrink-0 ml-2">
                      Save ~{fmt(h.potential_saving)}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
      </>)}

      {taxSubTab === 'tools' && (<>
      {/* Loss Pots (Verlusttöpfe) */}
      {lossPots.length > 0 && (
        <div className="border-t border-divider pt-6 p-4 space-y-3">
          <h3 className="font-serif text-heading font-semibold text-ink">Loss Offset Pots (Verlusttöpfe)</h3>
          <p className="text-[12px] text-ink-muted">
            German tax law maintains two separate loss pots. Equity losses (Aktienverlusttopf) can only offset equity gains. General losses offset any capital income.
          </p>
          {/* Mobile: card per year */}
          <div className="md:hidden space-y-2">
            {lossPots.map(y => (
              <div key={y.year} className="rounded-lg bg-parchment-deep px-3 py-2">
                <p className="text-[13px] font-semibold text-ink mb-1">{y.year}</p>
                <div className="grid grid-cols-[1fr_auto_auto] gap-x-3 gap-y-0.5 text-[11px] tabular-nums">
                  <span className="text-ink-muted">Equity</span>
                  <span className="text-right text-sage">{y.equity_gains > 0 ? fmt(y.equity_gains) : '—'}</span>
                  <span className="text-right text-claret">{y.equity_losses < 0 ? fmt(y.equity_losses) : '—'}</span>
                  <span className="text-ink-muted">General</span>
                  <span className="text-right text-sage">{y.general_gains > 0 ? fmt(y.general_gains) : '—'}</span>
                  <span className="text-right text-claret">{y.general_losses < 0 ? fmt(y.general_losses) : '—'}</span>
                  {(y.carry_forward_equity < 0 || y.carry_forward_general < 0) && (<>
                    <span className="text-ink-muted">Carry fwd</span>
                    <span className={`text-right font-medium ${y.carry_forward_equity < 0 ? 'text-claret' : 'text-ink-muted'}`}>
                      {y.carry_forward_equity < 0 ? fmt(y.carry_forward_equity) : '—'}
                    </span>
                    <span className={`text-right font-medium ${y.carry_forward_general < 0 ? 'text-claret' : 'text-ink-muted'}`}>
                      {y.carry_forward_general < 0 ? fmt(y.carry_forward_general) : '—'}
                    </span>
                  </>)}
                </div>
              </div>
            ))}
          </div>
          {/* Desktop: full table */}
          <div className="hidden md:block overflow-x-auto">
            <table className="w-full text-[12px]">
              <thead>
                <tr className="text-ink-muted text-left border-b border-divider">
                  <th className="px-2 py-1 font-medium">Year</th>
                  <th className="px-2 py-1 font-medium text-right">Equity Gains</th>
                  <th className="px-2 py-1 font-medium text-right">Equity Losses</th>
                  <th className="px-2 py-1 font-medium text-right">Equity Carry</th>
                  <th className="px-2 py-1 font-medium text-right">General Gains</th>
                  <th className="px-2 py-1 font-medium text-right">General Losses</th>
                  <th className="px-2 py-1 font-medium text-right">General Carry</th>
                </tr>
              </thead>
              <tbody>
                {lossPots.map(y => (
                  <tr key={y.year} className="border-b border-divider">
                    <td className="px-2 py-1.5 font-medium">{y.year}</td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-sage">{y.equity_gains > 0 ? fmt(y.equity_gains) : '—'}</td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-claret">{y.equity_losses < 0 ? fmt(y.equity_losses) : '—'}</td>
                    <td className={`px-2 py-1.5 text-right tabular-nums font-medium ${y.carry_forward_equity < 0 ? 'text-claret' : 'text-ink-muted'}`}>
                      {y.carry_forward_equity < 0 ? fmt(y.carry_forward_equity) : '—'}
                    </td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-sage">{y.general_gains > 0 ? fmt(y.general_gains) : '—'}</td>
                    <td className="px-2 py-1.5 text-right tabular-nums text-claret">{y.general_losses < 0 ? fmt(y.general_losses) : '—'}</td>
                    <td className={`px-2 py-1.5 text-right tabular-nums font-medium ${y.carry_forward_general < 0 ? 'text-claret' : 'text-ink-muted'}`}>
                      {y.carry_forward_general < 0 ? fmt(y.carry_forward_general) : '—'}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* FIFO Tax Lot Inventory */}
      {taxLots.length > 0 && (() => {
        // Aggregate lots by ISIN
        const grouped = taxLots.reduce<Record<string, { name: string; isEquity: boolean; lots: TaxLot[]; totalCost: number; totalValue: number; totalPL: number; totalTax: number }>>((acc, lot) => {
          if (!acc[lot.isin]) acc[lot.isin] = { name: lot.name, isEquity: lot.is_equity_fund, lots: [], totalCost: 0, totalValue: 0, totalPL: 0, totalTax: 0 };
          acc[lot.isin].lots.push(lot);
          acc[lot.isin].totalCost += lot.cost_basis;
          acc[lot.isin].totalValue += lot.current_value;
          acc[lot.isin].totalPL += lot.unrealized_pl;
          acc[lot.isin].totalTax += lot.tax_if_sold;
          return acc;
        }, {});
        const isins = Object.keys(grouped).sort((a, b) => grouped[b].totalValue - grouped[a].totalValue);
        const grandTotalTax = isins.reduce((s, k) => s + grouped[k].totalTax, 0);
        const grandTotalPL = isins.reduce((s, k) => s + grouped[k].totalPL, 0);
        return (
          <div className="border-t border-divider pt-6 p-4 space-y-3">
            <div className="flex items-center justify-between">
              <h3 className="font-serif text-heading font-semibold text-ink">FIFO Tax Lot Inventory</h3>
              <div className="text-right">
                <p className="text-[11px] text-ink-muted">Unrealized P&L</p>
                <p className={`text-[13px] font-semibold tabular-nums ${grandTotalPL >= 0 ? 'text-sage' : 'text-claret'}`}>{fmt(grandTotalPL)}</p>
              </div>
            </div>
            <p className="text-[12px] text-ink-muted">
              Per-lot cost basis (FIFO) and estimated tax if sold. Total tax liability: <span className="text-claret font-medium">{fmt(grandTotalTax)}</span>
            </p>
            <div className="space-y-3">
              {isins.map(isin => {
                const g = grouped[isin];
                return (
                  <details key={isin} className="group">
                    <summary className="flex items-center justify-between cursor-pointer select-none rounded-lg bg-parchment-deep px-3 py-2 hover:bg-divider transition-colors">
                      <div className="min-w-0 flex-1">
                        <p className="text-[13px] font-medium text-ink truncate">{g.name}</p>
                        <p className="text-[11px] text-ink-muted">{isin} · {g.lots.length} lot{g.lots.length > 1 ? 's' : ''}{g.isEquity ? ' · 30% Teilfreistellung' : ''}</p>
                      </div>
                      <div className="text-right shrink-0 ml-3">
                        <p className={`text-[13px] font-semibold tabular-nums ${g.totalPL >= 0 ? 'text-sage' : 'text-claret'}`}>{fmt(g.totalPL)}</p>
                        {g.totalTax > 0 && <p className="text-[11px] text-claret">Tax: {fmt(g.totalTax)}</p>}
                      </div>
                    </summary>
                    <div className="mt-1 overflow-x-auto">
                      <table className="w-full text-[12px]">
                        <thead>
                          <tr className="text-ink-muted text-left">
                            <th className="px-2 py-1 font-medium">Buy Date</th>
                            <th className="px-2 py-1 font-medium text-right">Qty</th>
                            <th className="px-2 py-1 font-medium text-right">Cost Basis</th>
                            <th className="px-2 py-1 font-medium text-right">Current</th>
                            <th className="px-2 py-1 font-medium text-right">P&L</th>
                            <th className="px-2 py-1 font-medium text-right">Tax</th>
                            <th className="px-2 py-1 font-medium text-right">Net</th>
                          </tr>
                        </thead>
                        <tbody>
                          {g.lots.map((lot, i) => (
                            <tr key={i} className="border-t border-divider">
                              <td className="px-2 py-1.5 tabular-nums">{lot.buy_date}</td>
                              <td className="px-2 py-1.5 text-right tabular-nums">{lot.quantity.toFixed(3)}</td>
                              <td className="px-2 py-1.5 text-right tabular-nums">{fmt(lot.cost_basis)}</td>
                              <td className="px-2 py-1.5 text-right tabular-nums">{fmt(lot.current_value)}</td>
                              <td className={`px-2 py-1.5 text-right tabular-nums font-medium ${lot.unrealized_pl >= 0 ? 'text-sage' : 'text-claret'}`}>{fmt(lot.unrealized_pl)}</td>
                              <td className="px-2 py-1.5 text-right tabular-nums text-claret">{lot.tax_if_sold > 0 ? fmt(lot.tax_if_sold) : '—'}</td>
                              <td className="px-2 py-1.5 text-right tabular-nums">{fmt(lot.net_proceeds)}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </details>
                );
              })}
            </div>
          </div>
        );
      })()}

      {/* Sell Simulator */}
      {taxLots.length > 0 && (() => {
        const isins = [...new Set(taxLots.map(l => l.isin))];
        const nameMap: Record<string, string> = {};
        const valueMap: Record<string, number> = {};
        const plMap: Record<string, number> = {};
        taxLots.forEach(l => {
          nameMap[l.isin] = l.name;
          valueMap[l.isin] = (valueMap[l.isin] || 0) + l.current_value;
          plMap[l.isin] = (plMap[l.isin] || 0) + l.unrealized_pl;
        });

        const losingISINs = isins.filter(isin => plMap[isin] < 0);
        const totalUnrealizedLoss = losingISINs.reduce((s, isin) => s + Math.abs(plMap[isin]), 0);

        const runSimulation = () => {
          const requests = isins
            .filter(isin => parseFloat(sellAmounts[isin] || '0') > 0)
            .map(isin => ({ isin, amount_eur: parseFloat(sellAmounts[isin]) }));
          if (requests.length === 0) return;
          api.simulateSell(requests, churchTaxRate).then(setSellResults).catch(console.error);
        };

        const harvestLosses = () => {
          // Auto-fill losing positions with their full value
          const newAmounts: Record<string, string> = {};
          losingISINs.forEach(isin => { newAmounts[isin] = String(Math.round(valueMap[isin])); });
          setSellAmounts(newAmounts);
          // Auto-run simulation
          const requests = losingISINs.map(isin => ({ isin, amount_eur: Math.round(valueMap[isin]) }));
          if (requests.length > 0) api.simulateSell(requests, churchTaxRate).then(setSellResults).catch(console.error);
        };

        return (
          <div className="border-t border-divider pt-6 p-4 space-y-3">
            <div className="flex items-center justify-between">
              <h3 className="font-serif text-heading font-semibold text-ink">What-If Sell Simulator</h3>
              {losingISINs.length > 0 && (
                <button
                  onClick={harvestLosses}
                  className="rounded-[8px] bg-parchment-deep border border-amber text-amber px-3 py-1.5 text-[12px] font-medium hover:bg-parchment-deep transition-colors"
                >
                  Harvest Losses ({fmt(totalUnrealizedLoss)})
                </button>
              )}
            </div>
            <p className="text-[12px] text-ink-muted">
              Enter EUR amounts to sell per security and preview the FIFO tax impact.
              {losingISINs.length > 0 && ` ${losingISINs.length} position${losingISINs.length > 1 ? 's' : ''} with unrealized losses available for harvesting.`}
            </p>
            <div className="flex items-center gap-2 text-[12px] text-ink-body">
              <label htmlFor="church-tax-rate">Kirchensteuer:</label>
              <select
                id="church-tax-rate"
                value={churchTaxRate}
                onChange={e => {
                  const v = parseFloat(e.target.value);
                  setChurchTaxRate(v);
                  localStorage.setItem('church_tax_rate', String(v));
                }}
                className="rounded border border-divider bg-parchment px-2 py-1 text-[12px]"
              >
                <option value={0}>None (26.375%)</option>
                <option value={0.08}>8% — BY / BW (28.375%)</option>
                <option value={0.09}>9% — other Länder (28.625%)</option>
              </select>
            </div>
            <div className="space-y-2">
              {isins.sort((a, b) => (valueMap[b] || 0) - (valueMap[a] || 0)).map(isin => (
                <div key={isin} className="rounded-lg bg-parchment-deep px-3 py-2">
                  <div className="flex items-center justify-between mb-1.5">
                    <span className="text-[13px] font-medium text-ink truncate min-w-0">{nameMap[isin]}</span>
                    <span className={`text-[12px] shrink-0 tabular-nums ml-2 ${plMap[isin] >= 0 ? 'text-sage' : 'text-claret'}`}>
                      {plMap[isin] >= 0 ? '+' : ''}{fmt(plMap[isin])}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-[11px] text-ink-muted shrink-0">Sell</span>
                    <input
                      aria-label={`Sell amount for ${nameMap[isin] || isin}`}
                      type="number"
                      placeholder="0"
                      value={sellAmounts[isin] || ''}
                      onChange={e => setSellAmounts(prev => ({ ...prev, [isin]: e.target.value }))}
                      className="flex-1 rounded-[8px] border border-divider bg-parchment px-2 py-1 text-[13px] tabular-nums text-right"
                    />
                    <span className="text-[11px] text-ink-muted shrink-0">€</span>
                  </div>
                </div>
              ))}
            </div>
            <button
              onClick={runSimulation}
              className="rounded-[8px] bg-forest text-white dark:text-parchment-deep px-4 py-2 text-[13px] font-medium"
            >
              Preview Tax Impact
            </button>

            {sellResults && sellResults.results && sellResults.results.length > 0 && (
              <div className="mt-3 space-y-3">
                <div className="grid grid-cols-2 md:grid-cols-3 gap-3">
                  <div className="rounded-xl bg-parchment-deep p-3 text-center">
                    <p className="text-[12px] text-ink-muted mb-1">Total Proceeds</p>
                    <p className="font-serif text-[20px] font-semibold tabular-nums text-sage">{fmt(sellResults.total_proceeds)}</p>
                  </div>
                  <div className="rounded-xl bg-parchment-deep p-3 text-center">
                    <p className="text-[12px] text-ink-muted mb-1">Total Tax</p>
                    <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(sellResults.total_tax)}</p>
                  </div>
                  <div className="rounded-xl bg-parchment-deep p-3 text-center">
                    <p className="text-[12px] text-ink-muted mb-1">Effective Rate</p>
                    <p className="font-serif text-[20px] font-semibold tabular-nums">
                      {sellResults.total_proceeds + sellResults.total_tax > 0
                        ? ((sellResults.total_tax / (sellResults.total_proceeds + sellResults.total_tax)) * 100).toFixed(1)
                        : '0'}%
                    </p>
                  </div>
                </div>
                <div className="overflow-x-auto">
                  <table className="w-full text-[12px] min-w-[500px]">
                    <thead>
                      <tr className="text-ink-muted text-left border-b border-divider">
                        <th className="px-2 py-1 font-medium">Security</th>
                        <th className="px-2 py-1 font-medium text-right">Sell</th>
                        <th className="px-2 py-1 font-medium text-right">Cost Basis</th>
                        <th className="px-2 py-1 font-medium text-right">Gain</th>
                        <th className="px-2 py-1 font-medium text-right">Tax</th>
                        <th className="px-2 py-1 font-medium text-right">Net</th>
                      </tr>
                    </thead>
                    <tbody>
                      {sellResults.results.map(r => (
                        <tr key={r.isin} className="border-b border-divider">
                          <td className="px-2 py-1.5">
                            <span className="font-medium text-ink">{r.name}</span>
                            {r.is_equity_fund && <span className="text-[11px] text-ink-muted ml-1">30% TF</span>}
                          </td>
                          <td className="px-2 py-1.5 text-right tabular-nums">{fmt(r.sell_amount)}</td>
                          <td className="px-2 py-1.5 text-right tabular-nums">{fmt(r.cost_basis)}</td>
                          <td className={`px-2 py-1.5 text-right tabular-nums font-medium ${r.realized_gain >= 0 ? 'text-sage' : 'text-claret'}`}>{fmt(r.realized_gain)}</td>
                          <td className="px-2 py-1.5 text-right tabular-nums text-claret">{fmt(r.estimated_tax)}</td>
                          <td className="px-2 py-1.5 text-right tabular-nums font-medium">{fmt(r.net_proceeds)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            )}
          </div>
        );
      })()}
      </>)}

      {taxSubTab === 'filing' && (<>
      {/* Tax Compliance Calendar */}
      {taxCalendar && taxCalendar.events.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-3">
            <h3 className="font-serif text-heading text-ink">Tax Calendar {taxCalendar.year}</h3>
            {taxCalendar.upcoming > 0 && (
              <span className="text-[12px] text-amber font-medium">{taxCalendar.upcoming} upcoming</span>
            )}
          </div>
          <div className="space-y-2">
            {taxCalendar.events.map((ev, i) => {
              const now = new Date();
              const evDate = new Date(ev.date);
              const daysUntil = Math.floor((evDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
              const isPast = daysUntil < 0;
              // Time-based urgency bands per TASKS spec:
              //   <14d  → claret (deadline imminent)
              //   <60d  → amber (planning window)
              //   ≥60d  → muted (later in the year)
              //   past  → muted + opacity
              const urgencyBand: 'urgent' | 'soon' | 'distant' | 'past' =
                isPast ? 'past' :
                daysUntil < 14 ? 'urgent' :
                daysUntil < 60 ? 'soon' :
                'distant';
              const borderColor = urgencyBand === 'urgent' ? 'border-claret'
                : urgencyBand === 'soon' ? 'border-amber'
                : 'border-divider';
              const dateColor = urgencyBand === 'urgent' ? 'text-claret'
                : urgencyBand === 'soon' ? 'text-amber'
                : 'text-ink-muted';
              const icons: Record<string, string> = { vorabpauschale: 'V', steuererklaerung: 'S', fsa: 'F', verlust: 'B', harvest: 'H', schenkung: 'G' };
              const daysLabel = isPast ? 'past' : daysUntil === 0 ? 'today' : `${daysUntil}d`;
              return (
                <div key={i} className={`rounded-r-xl bg-inset px-3 py-2.5 border-l-[3px] ${borderColor} ${isPast ? 'opacity-60' : ''}`}>
                  <div className="flex items-start gap-2">
                    <span className="w-6 h-6 rounded-full border border-divider flex items-center justify-center text-[11px] font-serif text-ink-muted shrink-0 mt-0.5">{icons[ev.category] || 'T'}</span>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-baseline justify-between gap-2">
                        <p className="text-[15px] font-medium text-ink truncate">{ev.title}</p>
                        <span className={`text-[11px] font-medium shrink-0 ${dateColor}`}>
                          {new Date(ev.date).toLocaleDateString('de-DE', { day: '2-digit', month: 'short' })} · {daysLabel}
                        </span>
                      </div>
                      <p className="text-[12px] text-ink-muted mt-0.5">{ev.description}</p>
                      {ev.amount != null && ev.amount > 0 && (
                        <p className="text-[12px] font-medium text-claret mt-0.5 tabular-nums">{fmt(ev.amount)}</p>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Anlage KAP Preparation */}
      {anlageKAP && anlageKAP.anlage_kap.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-3">
            <h3 className="font-serif text-heading text-ink">Anlage KAP {anlageKAP.year}</h3>
            <div className="flex gap-2">
              <a href={`/api/analysis/export-tax?year=${anlageKAP.year}`} className="text-forest text-[12px] font-medium px-2 py-2 rounded-lg hover:bg-parchment-deep transition-colors">CSV</a>
              <a href={`/api/analysis/export-datev?year=${anlageKAP.year}`} className="text-forest text-[12px] font-medium px-2 py-2 rounded-lg hover:bg-parchment-deep transition-colors">DATEV</a>
            </div>
          </div>

          {anlageKAP.cross_broker_note && (
            <div className="rounded-xl bg-inset border-l-[3px] border-sage px-3 py-2 mb-3 text-[12px] text-sage font-medium">
              {anlageKAP.cross_broker_note}
            </div>
          )}

          {/* Per-broker breakdown */}
          {anlageKAP.brokers && anlageKAP.brokers.length > 0 && (
            <div className="space-y-2 mb-3">
              {anlageKAP.brokers.map((b, i) => (
                <div key={i} className="rounded-xl bg-parchment-deep px-3 py-2">
                  <p className="text-[15px] font-medium text-ink mb-1.5">{b.broker_name}</p>
                  <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-[12px]">
                    {b.dividends !== 0 && <><span className="text-ink-muted">Dividends</span><span className="tabular-nums text-right">{fmt(b.dividends)}</span></>}
                    {b.interest !== 0 && <><span className="text-ink-muted">Interest</span><span className="tabular-nums text-right">{fmt(b.interest)}</span></>}
                    {b.realized_gains !== 0 && <><span className="text-ink-muted">Gains</span><span className="tabular-nums text-right text-sage">{fmt(b.realized_gains)}</span></>}
                    {b.realized_losses !== 0 && <><span className="text-ink-muted">Losses</span><span className="tabular-nums text-right text-claret">{fmt(b.realized_losses)}</span></>}
                    {b.teilfreistellung !== 0 && <><span className="text-ink-muted">Teilfreistellung</span><span className="tabular-nums text-right">{fmt(b.teilfreistellung)}</span></>}
                    {b.withheld_tax !== 0 && <><span className="text-ink-muted">Withheld tax</span><span className="tabular-nums text-right">{fmt(b.withheld_tax)}</span></>}
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* Anlage KAP line mapping */}
          <div className="rounded-xl bg-parchment-deep px-3 py-2">
            <p className="text-[12px] font-medium text-ink mb-1.5">Anlage KAP Zeilen</p>
            <div className="space-y-1">
              {anlageKAP.anlage_kap.map((line, i) => (
                <div key={i} className="flex items-baseline justify-between text-[12px]">
                  <div className="min-w-0 flex-1">
                    <span className="text-ink-muted mr-1.5">Z.{line.line}</span>
                    <span className="text-ink">{line.description}</span>
                  </div>
                  <span className={`tabular-nums shrink-0 ml-2 font-medium ${line.amount < 0 ? 'text-claret' : 'text-ink'}`}>{fmt(line.amount)}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Tax Reconciliation */}
      {anlageKAP && anlageKAP.anlage_kap.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h3 className="font-serif text-heading text-ink mb-2">Steuerbescheid Reconciliation</h3>
          <p className="text-[13px] text-ink-muted mb-3">
            Enter the amounts from your Steuerbescheid to compare with the app's computation.
          </p>
          <div className="space-y-1.5">
            {anlageKAP.anlage_kap.map((line) => {
              const key = `${anlageKAP.year}-Z${line.line}`;
              const entered = parseFloat(reconAmounts[key] || '') || 0;
              const diff = entered !== 0 ? entered - line.amount : 0;
              const hasDiff = entered !== 0 && Math.abs(diff) > 1;
              return (
                <div key={line.line} className={`rounded-lg px-2.5 py-2 ${hasDiff ? 'bg-inset border-l-[3px] border-claret' : 'bg-parchment-deep'}`}>
                  <p className="text-[11px] text-ink-muted mb-1">
                    <span className="text-ink-muted mr-1">Z.{line.line}</span>
                    {line.description.split('(')[0].trim()}
                  </p>
                  <div className="flex items-center gap-2">
                    <span className="text-[12px] text-ink-muted shrink-0">App:</span>
                    <span className="text-[12px] tabular-nums text-ink font-medium">{fmt(line.amount)}</span>
                    <span className="text-[12px] text-ink-muted shrink-0 ml-auto">FA:</span>
                    <input type="number" step="0.01" value={reconAmounts[key] || ''} placeholder="—"
                      onChange={e => {
                        const updated = { ...reconAmounts, [key]: e.target.value };
                        setReconAmounts(updated);
                        localStorage.setItem('recon_amounts', JSON.stringify(updated));
                      }}
                      className={`w-24 rounded-[6px] border bg-parchment text-ink px-1.5 py-0.5 text-[12px] tabular-nums text-right ${hasDiff ? 'border-claret bg-inset border-l-[3px] border-claret' : 'border-divider'}`} />
                    {hasDiff && (
                      <span className={`text-[11px] font-medium shrink-0 ${diff > 0 ? 'text-claret' : 'text-sage'}`}>
                        {diff > 0 ? '+' : ''}{fmt(diff)}
                      </span>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
          {Object.values(reconAmounts).some(v => v !== '') && (() => {
            const diffs = anlageKAP.anlage_kap.filter(line => {
              const key = `${anlageKAP.year}-Z${line.line}`;
              const entered = parseFloat(reconAmounts[key] || '') || 0;
              return entered !== 0 && Math.abs(entered - line.amount) > 1;
            });
            return diffs.length > 0 ? (
              <div className="mt-2 rounded-xl bg-inset border-l-[3px] border-claret px-3 py-2 text-[12px] text-claret font-medium">
                {diffs.length} discrepanc{diffs.length === 1 ? 'y' : 'ies'} found — review with your Steuerberater.
              </div>
            ) : (
              <div className="mt-2 rounded-xl bg-inset border-l-[3px] border-sage px-3 py-2 text-[12px] text-sage font-medium">
                All amounts match — no discrepancies.
              </div>
            );
          })()}
        </div>
      )}
      </>)}

      </>}

      {/* --- ALLOCATION TAB --- */}
      {activeTab === 'allocation' && <>

      {/* Concentration alerts */}
      {alerts.length > 0 && (
        <div className="space-y-2">
          {alerts.map((alert, i) => (
            <div
              key={i}
              className={`border-t border-divider pt-6 px-4 py-3 flex items-start gap-3 ${
                alert.level === 'critical'
                  ? 'border-l-[3px] border-claret'
                  : 'border-l-[3px] border-amber'
              }`}
            >
              <div className="shrink-0 mt-0.5">
                {alert.level === 'critical' ? (
                  <svg className="w-4 h-4 text-claret" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
                  </svg>
                ) : (
                  <svg className="w-4 h-4 text-amber" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
                  </svg>
                )}
              </div>
              <div className="min-w-0 flex-1">
                <p className="text-[15px] text-ink">{alert.message}</p>
                <p className="text-[12px] text-ink-muted mt-0.5">
                  {alert.type === 'overlap' ? 'High ETF overlap may indicate redundancy' : 'Concentrated single-stock exposure'}
                </p>
              </div>
            </div>
          ))}
        </div>
      )}

      {!hasData && holdings.length === 0 && alerts.length === 0 && (
        <div className="border-t border-divider pt-6 p-12 text-center text-[16px] text-ink-muted">
          No analysis data available yet. Import transactions and wait for ETF metadata to be fetched.
        </div>
      )}

      {sectorEntries.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Sector Allocation</h2>
          <EChartWrapper option={sectorChartOption} height="320px" />
        </div>
      )}

      {sectorDriftOption && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <div className="flex items-center justify-between mb-1 px-1 md:px-0">
            <h2 className="font-serif text-heading text-ink">Sector Drift Over Time</h2>
            <PeriodSelector value={sectorDriftPeriod} onChange={setSectorDriftPeriod} />
          </div>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">How your sector exposure has shifted month by month.</p>
          <EChartWrapper option={sectorDriftOption} height="380px" />
        </div>
      )}

      {treemapChartOption && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Portfolio Allocation</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">Click to zoom into ETF → sector → stock hierarchy.</p>
          <EChartWrapper option={treemapChartOption} height="450px" />
        </div>
      )}

      {countryEntries.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Country Allocation</h2>
          <EChartWrapper option={countryChartOption} height="400px" />
        </div>
      )}

      {currencyChartOption && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Currency Exposure</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Underlying currency exposure based on geographic allocation of holdings.
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <EChartWrapper option={currencyChartOption} height="300px" />
            <div className="space-y-2">
              {currencies.map(c => (
                <div key={c.currency} className="flex items-center gap-3">
                  <span className="text-[15px] font-semibold w-10 shrink-0">{c.currency}</span>
                  <div className="flex-1 h-5 rounded-full bg-parchment-deep overflow-hidden">
                    <div
                      className="h-full rounded-full bg-forest transition-all duration-300"
                      style={{ width: `${Math.min(c.pct, 100)}%` }}
                    />
                  </div>
                  <span className="text-[13px] tabular-nums text-ink-muted w-16 text-right shrink-0">
                    {c.pct.toFixed(1)}%
                  </span>
                  <span className="text-[13px] tabular-nums text-ink-muted w-20 text-right shrink-0">
                    {fmt(c.value)}
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}

      {/* Historical FX Rates */}
      {fxHistory && Object.keys(fxHistory).length > 0 && (() => {
        const pairs = Object.keys(fxHistory).filter(k => fxHistory[k].length > 10);
        if (pairs.length === 0) return null;
        const allDates = new Set<string>();
        for (const p of pairs) for (const pt of fxHistory[p]) allDates.add(pt.date);
        const dates = [...allDates].sort();
        const byDate: Record<string, Record<string, number>> = {};
        for (const p of pairs) for (const pt of fxHistory[p]) { byDate[pt.date] = byDate[pt.date] || {}; byDate[pt.date][p] = pt.rate; }
        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Exchange Rates</h2>
            <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
              Historical EUR exchange rates (1 EUR = X foreign currency).
            </p>
            <EChartWrapper option={{
              tooltip: { trigger: 'axis' as const },
              legend: { data: pairs.map(p => `EUR/${p}`), bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
              xAxis: { type: 'category' as const, data: dates, axisLabel: { fontSize: 11, color: tc.inkMuted, interval: Math.max(Math.floor(dates.length / 8) - 1, 1) }, axisLine: { show: false }, axisTick: { show: false }, boundaryGap: false },
              yAxis: pairs.map((p, i) => ({ type: 'value' as const, position: i === 0 ? ('left' as const) : ('right' as const), axisLabel: { fontSize: 11, color: tc.inkMuted }, splitLine: { show: i === 0 }, name: p, nameTextStyle: { fontSize: 10, color: tc.inkMuted } })),
              series: pairs.map((p, i) => ({
                name: `EUR/${p}`, type: 'line' as const, yAxisIndex: Math.min(i, 1),
                data: dates.map(d => byDate[d]?.[p] ?? null),
                smooth: 0.3, showSymbol: false, lineStyle: { width: 1.5 },
              })),
              grid: { left: 55, right: 55, top: 30, bottom: 40 },
            }} height="280px" />
          </div>
        );
      })()}

      {/* Cash Flow */}
      {cashFlow && cashFlow.history.length > 3 && (() => {
        const allMonths = [...cashFlow.history.map(h => h.month), ...cashFlow.projection.map(p => p.month)];
        const allIncome = [...cashFlow.history.map(h => h.income), ...cashFlow.projection.map(p => p.income)];
        const allExpenses = [...cashFlow.history.map(h => -h.expenses), ...cashFlow.projection.map(p => -p.expenses)];
        const histLen = cashFlow.history.length;
        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Cash Flow</h2>
            <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
              Monthly income vs expenses. Avg net: <span className={cashFlow.avg_net >= 0 ? 'text-sage font-medium' : 'text-claret font-medium'}>{fmt(cashFlow.avg_net)}/mo</span>
            </p>
            <EChartWrapper option={{
              tooltip: { trigger: 'axis' as const },
              legend: { data: ['Income', 'Expenses'], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
              xAxis: { type: 'category' as const, data: allMonths, axisLabel: { fontSize: 11, color: tc.inkMuted, interval: Math.max(Math.floor(allMonths.length / 10) - 1, 1) }, axisLine: { show: false }, axisTick: { show: false } },
              yAxis: { type: 'value' as const, axisLabel: { fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
              series: [
                { name: 'Income', type: 'bar' as const, stack: 'flow', data: allIncome, itemStyle: { color: (p: { dataIndex: number }) => p.dataIndex >= histLen ? `color-mix(in srgb, ${tc.sage} 30%, transparent)` : tc.sage }, barMaxWidth: 16 },
                { name: 'Expenses', type: 'bar' as const, stack: 'flow', data: allExpenses, itemStyle: { color: (p: { dataIndex: number }) => p.dataIndex >= histLen ? `color-mix(in srgb, ${tc.claret} 30%, transparent)` : tc.claret }, barMaxWidth: 16 },
              ],
              grid: { left: 55, right: 16, top: 10, bottom: 40 },
            }} height="260px" />
          </div>
        );
      })()}

      {/* Purchasing Power / Inflation */}
      {inflation && inflation.history.length > 3 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Purchasing Power</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Nominal vs inflation-adjusted net worth (German HVPI).
            {inflation.nominal_return != null && inflation.real_return != null && (
              <span className="ml-1">
                Nominal: <span className="text-sage font-medium">+{inflation.nominal_return}%</span>,
                Real: <span className={inflation.real_return >= 0 ? 'text-sage font-medium' : 'text-claret font-medium'}>
                  {inflation.real_return >= 0 ? '+' : ''}{inflation.real_return}%
                </span>
                {inflation.purchasing_power_lost != null && inflation.purchasing_power_lost > 0 && (
                  <span className="text-claret ml-1">({fmt(inflation.purchasing_power_lost)} lost to inflation)</span>
                )}
              </span>
            )}
          </p>
          <EChartWrapper option={{
            tooltip: { trigger: 'axis' as const },
            legend: { data: ['Nominal', 'Real (today\'s EUR)'], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
            xAxis: { type: 'category' as const, data: inflation.history.map(h => h.date), axisLabel: { fontSize: 11, color: tc.inkMuted, interval: Math.max(Math.floor(inflation.history.length / 8) - 1, 1) }, axisLine: { show: false }, axisTick: { show: false }, boundaryGap: false },
            yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => v >= 1000 ? `${(v/1000).toFixed(0)}k` : `${v}`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
            series: [
              { name: 'Nominal', type: 'line' as const, data: inflation.history.map(h => h.nominal), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.forest, width: 2 } },
              { name: 'Real (today\'s EUR)', type: 'line' as const, data: inflation.history.map(h => h.real), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.gold, width: 2, type: 'dashed' as const } },
            ],
            grid: { left: 55, right: 16, top: 10, bottom: 40 },
          }} height="260px" />
        </div>
      )}

      {/* Benchmark Comparison */}
      {benchComp && benchComp.comparison.length > 3 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">What If I Had Bought {benchComp.benchmark_name}?</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Replays your actual deposit history through a single benchmark.
            Difference: <span className={benchComp.difference >= 0 ? 'text-sage font-medium' : 'text-claret font-medium'}>
              {benchComp.difference >= 0 ? '+' : ''}{fmt(benchComp.difference)}
            </span>
            {' '}(You: {fmt(benchComp.actual_value)} vs Benchmark: {fmt(benchComp.benchmark_value)})
          </p>
          <EChartWrapper option={{
            tooltip: { trigger: 'axis' as const },
            legend: { data: ['Your Portfolio', benchComp.benchmark_name], bottom: 0, textStyle: { fontSize: 11, color: tc.inkMuted } },
            xAxis: { type: 'category' as const, data: benchComp.comparison.map(c => c.date), axisLabel: { fontSize: 11, color: tc.inkMuted, interval: Math.max(Math.floor(benchComp.comparison.length / 8) - 1, 1) }, axisLine: { show: false }, axisTick: { show: false }, boundaryGap: false },
            yAxis: { type: 'value' as const, axisLabel: { formatter: (v: number) => v >= 1000 ? `${(v/1000).toFixed(0)}k` : `${v}`, fontSize: 11, color: tc.inkMuted }, splitLine: { lineStyle: { color: tc.divider } } },
            series: [
              { name: 'Your Portfolio', type: 'line' as const, data: benchComp.comparison.map(c => c.actual), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.forest, width: 2 } },
              { name: benchComp.benchmark_name, type: 'line' as const, data: benchComp.comparison.map(c => c.benchmark), smooth: 0.3, showSymbol: false, lineStyle: { color: tc.walnut, width: 2, type: 'dashed' as const } },
            ],
            grid: { left: 55, right: 16, top: 10, bottom: 40 },
          }} height="280px" />
        </div>
      )}

      </>}

      {/* --- COSTS TAB --- */}
      {activeTab === 'costs' && <>

      {/* ETF Cost Analysis */}
      {costData && costData.holdings.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Portfolio Costs</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Annual fee drag from ETF expense ratios (TER).
          </p>

          <div className="grid grid-cols-2 md:grid-cols-4 gap-3 mb-4">
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Weighted Avg TER</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{costData.weighted_ter.toFixed(2)}%</p>
              {costData.coverage_pct < 99.5 && (
                <p className="text-[11px] text-amber mt-0.5">
                  {costData.coverage_pct.toFixed(1)}% priced · {costData.weighted_ter_covered_only.toFixed(2)}% on priced
                </p>
              )}
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Annual Cost</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(costData.annual_cost)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">Daily Cost</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{fmt(costData.daily_cost)}</p>
            </div>
            <div className="rounded-xl bg-parchment-deep p-3">
              <p className="text-[12px] text-ink-muted mb-1">10-Year Drag</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">
                {fmt(costData.projection.find(p => p.year === 10)?.cumulative ?? costData.annual_cost * 10)}
              </p>
            </div>
          </div>

          {/* Cost Projection Chart */}
          {costData.projection.length > 0 && (
            <div className="mb-4 px-1 md:px-0">
              <p className="text-[13px] font-medium text-ink mb-2">Cumulative Fee Drag</p>
              <EChartWrapper option={{
                grid: { top: 20, right: 16, bottom: 30, left: 50 },
                xAxis: { type: 'category', data: costData.projection.map(p => `${p.year}Y`), axisLabel: { fontSize: 11 } },
                yAxis: { type: 'value', axisLabel: { fontSize: 11, formatter: (v: number) => `${(v/1000).toFixed(1)}K` } },
                series: [{
                  type: 'bar',
                  data: costData.projection.map(p => p.cumulative),
                  itemStyle: { color: tc.claret, borderRadius: [4, 4, 0, 0] },
                  label: { show: true, position: 'top', fontSize: 10, formatter: (p: unknown) => `${Math.round((p as { value: number }).value)}€` },
                }],
                tooltip: { trigger: 'axis', formatter: (p: unknown) => { const a = p as { name: string; value: number }[]; return `${a[0].name}: ${a[0].value.toFixed(0)} €`; } },
              }} height="192px" />
            </div>
          )}

          {/* Cost Benchmarking */}
          {costData.benchmark && (
            <div className="mb-4 px-1 md:px-0">
              <div className="flex items-center gap-3 rounded-xl bg-inset border-l-[3px] border-sage p-4">
                <div className={`w-12 h-12 rounded-full flex items-center justify-center text-white dark:text-parchment-deep text-lg font-bold shrink-0 ${
                  costData.benchmark.grade.startsWith('A') ? 'bg-sage' :
                  costData.benchmark.grade.startsWith('B') ? 'bg-forest' :
                  costData.benchmark.grade.startsWith('C') ? 'bg-amber' : 'bg-claret'
                }`}>
                  {costData.benchmark.grade}
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-[15px] font-medium text-ink">Cost Efficiency</p>
                  <p className="text-[12px] text-ink-muted">{costData.benchmark.detail}</p>
                  <p className="text-[12px] text-ink-muted mt-1">
                    Your TER: <span className="font-medium">{costData.benchmark.your_ter}%</span> vs avg German investor: <span className="font-medium">{costData.benchmark.avg_ter}%</span>
                    {costData.benchmark.annual_saving > 0 && (
                      <span className="text-sage font-medium"> — saving {fmt(costData.benchmark.annual_saving)}/yr</span>
                    )}
                  </p>
                </div>
              </div>
            </div>
          )}

          {/* Total Cost of Ownership */}
          {costData.total_cost_ownership && (
            <div className="mb-4 px-1 md:px-0">
              <p className="text-[13px] font-medium text-ink mb-2">Total Cost of Ownership</p>
              <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
                <div className="rounded-lg bg-parchment-deep p-3.5 text-center">
                  <p className="text-[11px] text-ink-muted">Fund Fees (TER)</p>
                  <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(costData.total_cost_ownership.ter_cost)}/yr</p>
                </div>
                <div className="rounded-lg bg-parchment-deep p-3.5 text-center">
                  <p className="text-[11px] text-ink-muted">Transaction Fees</p>
                  <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(costData.total_cost_ownership.transaction_fees)}/yr</p>
                </div>
                <div className="rounded-lg bg-parchment-deep p-3.5 text-center">
                  <p className="text-[11px] text-ink-muted">Spread (est.)</p>
                  <p className="text-[13px] font-semibold tabular-nums text-amber">{fmt(costData.total_cost_ownership.spread_estimate)}/yr</p>
                </div>
                <div className="rounded-lg bg-inset border-l-[3px] border-claret p-2.5 text-center">
                  <p className="text-[11px] text-ink-muted">Total Annual</p>
                  <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(costData.total_cost_ownership.total_annual)}/yr</p>
                  <p className="text-[11px] text-ink-muted">{costData.total_cost_ownership.total_annual_pct}% of portfolio</p>
                </div>
              </div>
              <p className="text-[11px] text-ink-muted mt-2">
                Lifetime: {fmt(costData.total_cost_ownership.lifetime_fees)} fees across {costData.total_cost_ownership.transaction_count} transactions ({fmt(costData.total_cost_ownership.lifetime_volume)} volume). Spread estimated at 0.05%.
              </p>
            </div>
          )}

          <div className="space-y-1.5 px-1 md:px-0">
            {costData.holdings.map(h => (
              <div key={h.isin} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="text-[15px] font-medium text-ink truncate">{h.name}</p>
                  <p className="text-[12px] text-ink-muted">
                    TER {h.ter.toFixed(2)}% · {h.weight.toFixed(1)}% of portfolio
                  </p>
                </div>
                <span className="text-[13px] font-medium tabular-nums text-claret shrink-0 ml-2">
                  {fmt(h.annual_cost)}/yr
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Cheaper Alternatives */}
      {alternatives.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Cheaper Alternatives</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">Lower-cost ETFs tracking similar indices.</p>
          <div className="space-y-3 px-1 md:px-0">
            {alternatives.map(h => (
              <div key={h.isin}>
                <p className="text-[15px] font-medium text-ink mb-1">{h.name} <span className="text-[12px] text-ink-muted">TER {h.current_ter}%</span></p>
                {h.alternatives.map(a => (
                  <div key={a.name} className="flex items-center justify-between rounded-lg bg-inset border-l-[3px] border-sage px-3 py-2 mb-1">
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium text-ink">{a.name}</p>
                      <p className="text-[12px] text-ink-muted">TER {a.ter}% (save {(h.current_ter - a.ter).toFixed(2)}%)</p>
                    </div>
                    <div className="text-right shrink-0 ml-2">
                      <p className="text-[13px] font-medium text-sage tabular-nums">{fmt(a.annual_saving)}/yr</p>
                      <p className="text-[11px] text-ink-muted tabular-nums">{fmt(a.ten_year_saving)} over 10yr</p>
                    </div>
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}

      {overlapChartOption && (
        <div className="border-t border-divider pt-6 py-3 md:py-5 overflow-x-auto">
          <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">ETF Overlap Matrix</h2>
          <div className="min-w-[350px]">
            <EChartWrapper option={overlapChartOption} height="400px" />
          </div>
        </div>
      )}

      {/* Correlation Matrix */}
      {correlation && correlation.labels.length >= 2 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5 overflow-x-auto">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Price Correlation</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Pairwise correlation of daily returns. Low correlation = better diversification.
          </p>
          <div className="min-w-[350px]">
            <EChartWrapper option={{
              tooltip: {
                formatter: (params: unknown) => {
                  const p = params as { data: number[] };
                  const [x, y, val] = p.data;
                  return `${correlation.labels[x]} vs ${correlation.labels[y]}: ${val.toFixed(2)}`;
                },
              },
              xAxis: { type: 'category' as const, data: correlation.labels, axisLabel: { rotate: 45, fontSize: 11, color: tc.inkBody } },
              yAxis: { type: 'category' as const, data: correlation.labels, axisLabel: { fontSize: 11, color: tc.inkBody } },
              visualMap: {
                min: -1, max: 1, calculable: true, orient: 'horizontal' as const, left: 'center', bottom: 0,
                inRange: { color: [tc.claret, tc.divider, tc.sage] },
                textStyle: { color: tc.inkMuted },
              },
              series: [{
                type: 'heatmap' as const,
                data: correlation.matrix.flatMap((row, i) => row.map((val, j) => [i, j, val])),
                label: { show: true, fontSize: 11, formatter: (p: unknown) => `${(p as { data: number[] }).data[2].toFixed(2)}` },
                itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
              }],
              grid: { left: 100, right: 20, top: 10, bottom: 80 },
            }} height="400px" />
          </div>
        </div>
      )}

      {topHoldingsChartOption && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Top Shared Holdings</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">Individual stocks with highest aggregate exposure across all ETFs.</p>
          <EChartWrapper option={topHoldingsChartOption} height={`${Math.max(250, Math.min(topHoldings.length, 15) * 28)}px`} />
        </div>
      )}

      {holdings.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">ETF Holdings Breakdown</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">Select an ETF to view its constituent positions.</p>

          {/* ETF selector — scrollable row on mobile */}
          <div className="flex flex-nowrap md:flex-wrap gap-2 mb-4 md:mb-5 overflow-x-auto pb-1 -mx-1 px-1">
            {holdings.map((h) => (
              <button
                key={h.security_isin}
                onClick={() => loadETFHoldings(h.security_isin)}
                className={`shrink-0 rounded-[8px] px-3 md:px-3.5 py-[6px] md:py-[7px] text-[13px] md:text-[15px] font-medium transition-all duration-150 ${
                  selectedETF === h.security_isin
                    ? 'bg-forest text-white dark:text-parchment-deep'
                    : 'bg-parchment-deep text-ink-body hover:bg-divider active:bg-divider'
                }`}
              >
                {h.name}
              </button>
            ))}
          </div>

          {etfLoading && (
            <div className="py-8 text-center text-[16px] text-ink-muted">Loading holdings...</div>
          )}

          {selectedETF && !etfLoading && etfHoldings.length === 0 && (
            <div className="py-8 text-center text-[16px] text-ink-muted">
              No constituent data available for this ETF yet.
            </div>
          )}

          {selectedETF && !etfLoading && etfHoldings.length > 0 && (
            <div>
              <h3 className="text-[13px] md:font-serif text-heading text-ink mb-3 px-1 md:px-0">
                {etfName} — {etfHoldings.length} positions
              </h3>

              {/* Mobile: compact list */}
              <div className="md:hidden space-y-1">
                {etfHoldings.map((entry, i) => (
                  <div key={entry.isin} className="flex items-center gap-3 px-1 py-2 border-b border-divider last:border-0">
                    <span className="text-[12px] text-ink-muted tabular-nums w-5 shrink-0 text-right">{i + 1}</span>
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium text-ink truncate">{entry.name}</p>
                      <p className="text-[11px] text-ink-muted">{entry.sector || '—'} · {entry.country || '—'}</p>
                    </div>
                    <div className="shrink-0 text-right">
                      <span className="text-[13px] font-medium tabular-nums">{entry.weight.toFixed(2)}%</span>
                    </div>
                  </div>
                ))}
              </div>

              {/* Desktop: full table */}
              <div className="hidden md:block overflow-x-auto -mx-5">
                <table className="w-full">
                  <thead>
                    <tr className="text-left font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em]">
                      <th className="px-5 pb-2 font-medium">#</th>
                      <th className="px-5 pb-2 font-medium">Name</th>
                      <th className="px-5 pb-2 font-medium">ISIN</th>
                      <th className="px-5 pb-2 font-medium text-right">Weight</th>
                      <th className="px-5 pb-2 font-medium">Sector</th>
                      <th className="px-5 pb-2 font-medium">Country</th>
                    </tr>
                  </thead>
                  <tbody>
                    {etfHoldings.map((entry, i) => (
                      <tr
                        key={entry.isin}
                        className={`transition-colors hover:bg-parchment-deep ${
                          i < etfHoldings.length - 1 ? 'border-b border-divider' : ''
                        }`}
                      >
                        <td className="px-5 py-2.5 text-[12px] text-ink-muted tabular-nums">{i + 1}</td>
                        <td className="px-5 py-2.5 text-[15px] font-medium text-ink">{entry.name}</td>
                        <td className="px-5 py-2.5 font-mono text-[12px] text-ink-muted">{entry.isin.startsWith('XX_') ? '—' : entry.isin}</td>
                        <td className="px-5 py-2.5 text-right">
                          <span className="text-[15px] font-medium tabular-nums">{entry.weight.toFixed(2)}%</span>
                          <div className="mt-1 h-1 w-full rounded-full bg-divider">
                            <div
                              className="h-1 rounded-full bg-forest transition-all duration-300"
                              style={{ width: `${Math.min(entry.weight * 2, 100)}%` }}
                            />
                          </div>
                        </td>
                        <td className="px-5 py-2.5 text-[15px] text-ink-muted">{entry.sector || '—'}</td>
                        <td className="px-5 py-2.5 text-[15px] text-ink-muted">{entry.country || '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}

      </>}

      {/* ESG Classification — on Allocation tab */}
      {activeTab === 'allocation' && holdings.length > 0 && (
        <div className="border-t border-divider pt-6 py-3 md:py-5">
          <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">ESG Classification</h2>
          <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
            Sustainability strategy identified from fund name and index methodology.
          </p>

          {(() => {
            // Classify each holding by ESG strategy based on name patterns
            const classifyESG = (name: string): { strategy: string; label: string; color: string } => {
              const n = name.toUpperCase();
              if (n.includes('SRI') || n.includes('SUSTAINABLE')) return { strategy: 'SRI', label: 'Socially Responsible', color: 'text-sage' };
              if (n.includes('PAB') || n.includes('PARIS')) return { strategy: 'PAB', label: 'Paris-Aligned', color: 'text-sage' };
              if (n.includes('CTB') || n.includes('CLIMATE')) return { strategy: 'CTB', label: 'Climate Transition', color: 'text-sage' };
              if (n.includes('ESG') && (n.includes('ENHANCED') || n.includes('LEADERS'))) return { strategy: 'Enhanced', label: 'ESG Enhanced', color: 'text-forest' };
              if (n.includes('ESG') && n.includes('SCREENED')) return { strategy: 'Screened', label: 'ESG Screened', color: 'text-forest' };
              if (n.includes('ESG')) return { strategy: 'ESG', label: 'ESG Integrated', color: 'text-forest' };
              return { strategy: 'None', label: 'No ESG Strategy', color: 'text-ink-muted' };
            };

            const classified = holdings.map(h => ({
              name: h.name,
              isin: h.security_isin,
              value: h.market_value ?? (h.avg_cost_basis * h.quantity),
              weight: h.weight_pct ?? 0,
              ...classifyESG(h.name),
            }));

            const totalValue = classified.reduce((s, h) => s + h.value, 0);
            const esgValue = classified.filter(h => h.strategy !== 'None').reduce((s, h) => s + h.value, 0);
            const esgPct = totalValue > 0 ? Math.round(esgValue / totalValue * 100) : 0;

            // Group by strategy
            const strategies = new Map<string, { label: string; color: string; value: number; count: number }>();
            classified.forEach(h => {
              const existing = strategies.get(h.strategy);
              if (existing) { existing.value += h.value; existing.count++; }
              else strategies.set(h.strategy, { label: h.label, color: h.color, value: h.value, count: 1 });
            });

            return (
              <>
                <div className="grid grid-cols-2 gap-2 mb-4 px-1 md:px-0">
                  <div className="rounded-xl bg-parchment-deep p-3 text-center">
                    <p className="text-[12px] text-ink-muted mb-1">ESG Coverage</p>
                    <p className={`font-serif text-[20px] font-semibold tabular-nums ${esgPct >= 50 ? 'text-sage' : esgPct > 0 ? 'text-amber' : 'text-ink-muted'}`}>{esgPct}%</p>
                    <p className="text-[11px] text-ink-muted">of portfolio value</p>
                  </div>
                  <div className="rounded-xl bg-parchment-deep p-3 text-center">
                    <p className="text-[12px] text-ink-muted mb-1">ESG Holdings</p>
                    <p className="font-serif text-[20px] font-semibold tabular-nums">{classified.filter(h => h.strategy !== 'None').length}/{classified.length}</p>
                    <p className="text-[11px] text-ink-muted">funds with ESG strategy</p>
                  </div>
                </div>

                <div className="space-y-1.5 px-1 md:px-0">
                  {classified.map(h => (
                    <div key={h.isin} className="flex items-center justify-between rounded-lg bg-parchment-deep px-3 py-1.5">
                      <div className="min-w-0 flex-1">
                        <p className="text-[13px] text-ink truncate">{h.name}</p>
                        <p className="text-[11px] text-ink-muted">{h.weight.toFixed(1)}% · {fmt(h.value)}</p>
                      </div>
                      <span className={`text-[12px] font-medium shrink-0 ml-3 ${h.color}`}>{h.label}</span>
                    </div>
                  ))}
                </div>
              </>
            );
          })()}
        </div>
      )}

      {/* Sustainability Dashboard — on Allocation tab */}
      {activeTab === 'allocation' && holdings.length > 0 && (() => {
        // ESG classify — must match classifyESG in ESG Classification section above
        const classifyESG = (name: string) => {
          const n = name.toUpperCase();
          if (n.includes('SRI') || n.includes('SUSTAINABLE')) return 'SRI';
          if (n.includes('PAB') || n.includes('PARIS')) return 'PAB';
          if (n.includes('CTB') || n.includes('CLIMATE')) return 'CTB';
          if (n.includes('ESG') && (n.includes('ENHANCED') || n.includes('LEADERS'))) return 'Enhanced';
          if (n.includes('ESG') && n.includes('SCREENED')) return 'Screened';
          if (n.includes('ESG')) return 'ESG';
          return 'None';
        };

        const classified = holdings.map(h => ({
          name: h.name, isin: h.security_isin,
          value: h.market_value ?? (h.avg_cost_basis * h.quantity),
          strategy: classifyESG(h.name),
        }));
        const totalVal = classified.reduce((s, h) => s + h.value, 0);

        // Strategy breakdown for donut
        const strategyMap = new Map<string, number>();
        classified.forEach(h => strategyMap.set(h.strategy, (strategyMap.get(h.strategy) || 0) + h.value));
        const donutData = Array.from(strategyMap.entries()).map(([name, value]) => ({ name, value: Math.round(value) }));

        // Controversial holdings detection — check fund names against exclusion keywords
        // Uses the fund names themselves (not ETF constituents which require per-ETF loading)
        const controversialKeywords = ['tobacco', 'weapons', 'gambling', 'coal', 'arctic', 'palm oil', 'nuclear', 'cluster munitions', 'thermal coal', 'controversial', 'defense', 'arms'];
        // First check loaded ETF holdings (if any ETF was drilled into)
        const controversialFromETF = etfHoldings.filter(h =>
          controversialKeywords.some(c => h.name.toLowerCase().includes(c))
        );
        // Also flag any portfolio holdings with controversial names
        const controversialFromHoldings = holdings.filter(h =>
          controversialKeywords.some(c => h.name.toLowerCase().includes(c))
        );
        const controversialFound = [...controversialFromETF, ...controversialFromHoldings.map(h => ({ name: h.name }))];

        // Carbon intensity estimate (rough: non-ESG = 200 tCO2/MEUR, ESG = 100, SRI/PAB = 50)
        const carbonIntensity = classified.reduce((s, h) => {
          const intensity = h.strategy === 'None' ? 200 : h.strategy === 'SRI' || h.strategy === 'PAB' || h.strategy === 'CTB' ? 50 : 100;
          return s + intensity * (h.value / totalVal);
        }, 0);

        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">Sustainability Dashboard</h2>
            <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
              ESG allocation breakdown, controversial exposure, and estimated carbon intensity.
            </p>

            <div className="grid grid-cols-2 md:grid-cols-3 gap-2 mb-4 px-1 md:px-0">
              <div className="rounded-xl bg-parchment-deep p-3 text-center">
                <p className="text-[12px] text-ink-muted mb-1">Carbon Intensity</p>
                <p className={`font-serif text-[20px] font-semibold tabular-nums ${carbonIntensity < 100 ? 'text-sage' : carbonIntensity < 200 ? 'text-amber' : 'text-claret'}`}>{Math.round(carbonIntensity)}</p>
                <p className="text-[11px] text-ink-muted">tCO2e / M EUR (est.)</p>
              </div>
              <div className="rounded-xl bg-parchment-deep p-3 text-center">
                <p className="text-[12px] text-ink-muted mb-1">Controversial Exposure</p>
                <p className={`font-serif text-[20px] font-semibold tabular-nums ${controversialFound.length === 0 ? 'text-sage' : 'text-claret'}`}>{controversialFound.length}</p>
                <p className="text-[11px] text-ink-muted">flagged holdings</p>
              </div>
              {(() => {
                // Sustainability Score: A-E composite
                // ESG coverage: 0-50 points (100% ESG = 50, 0% = 0)
                const esgVal = classified.filter(h => h.strategy !== 'None').reduce((s, h) => s + h.value, 0);
                const esgPct = totalVal > 0 ? esgVal / totalVal * 100 : 0;
                const esgScore = Math.min(50, esgPct / 2); // 0-50

                // Controversial: 0-30 points (0 controversials = 30, 5+ = 0)
                const controScore = Math.max(0, 30 - controversialFound.length * 6);

                // Carbon: 0-20 points (<100 = 20, >250 = 0)
                const carbonScore = Math.max(0, Math.min(20, (250 - carbonIntensity) / 7.5));

                const total = Math.round(esgScore + controScore + carbonScore);
                const grade = total >= 80 ? 'A' : total >= 60 ? 'B' : total >= 40 ? 'C' : total >= 20 ? 'D' : 'E';
                const gradeColor = grade === 'A' ? 'text-sage' : grade === 'B' ? 'text-forest' : grade === 'C' ? 'text-amber' : 'text-claret';

                return (
                  <div className="rounded-xl bg-parchment-deep p-3 text-center">
                    <p className="text-[12px] text-ink-muted mb-1">Sustainability Score</p>
                    <p className={`font-serif text-[28px] font-semibold ${gradeColor}`}>{grade}</p>
                    <p className="text-[11px] text-ink-muted">{total}/100</p>
                  </div>
                );
              })()}
            </div>

            {/* ESG Allocation Donut */}
            <EChartWrapper option={{
              tooltip: { trigger: 'item' as const },
              series: [{
                type: 'pie' as const, radius: ['40%', '70%'],
                data: donutData,
                label: { fontSize: 11, color: tc.inkBody },
                itemStyle: { borderColor: tc.parchmentDeep, borderWidth: 2 },
              }],
              graphic: [{ type: 'text', left: 'center', top: 'center', style: { text: `${donutData.length} ${donutData.length === 1 ? 'strategy' : 'strategies'}`, fontSize: 13, fontWeight: 600, fill: tc.inkMuted } }],
            }} height="250px" />

            {/* Controversial holdings */}
            {controversialFound.length > 0 && (
              <div className="mt-3 px-1 md:px-0">
                <p className="font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em] mb-2">Controversial Holdings Detected</p>
                <div className="space-y-1">
                  {controversialFound.slice(0, 10).map((h, i) => (
                    <div key={i} className="flex items-center gap-2 rounded-lg bg-inset border-l-[3px] border-claret px-3 py-1.5">
                      <span className="w-2 h-2 rounded-full bg-claret shrink-0" />
                      <span className="text-[12px] text-ink">{h.name}</span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        );
      })()}

      {/* ESG-Aligned Alternatives — on Allocation tab */}
      {activeTab === 'allocation' && holdings.length > 0 && (() => {
        // Static ESG alternative mapping: conventional ISIN -> ESG equivalent
        const esgAlternatives: Record<string, { isin: string; name: string; ter: number; strategy: string }> = {
          'IE00BK5BQT80': { isin: 'IE00BNG8L278', name: 'Vanguard ESG Global All Cap', ter: 0.24, strategy: 'ESG Screened' },
          'IE00B4L5Y983': { isin: 'IE00BYX2JD69', name: 'iShares MSCI World SRI', ter: 0.20, strategy: 'SRI' },
          'IE00B4L5YC18': { isin: 'IE00BFNM3P36', name: 'iShares MSCI EM SRI', ter: 0.25, strategy: 'SRI' },
          'IE00BJ0KDQ92': { isin: 'IE00BG36TC12', name: 'Xtrackers MSCI World ESG', ter: 0.20, strategy: 'ESG Screened' },
          'LU0392494562': { isin: 'LU0629459743', name: 'UBS MSCI World SRI', ter: 0.22, strategy: 'SRI' },
          'IE00B6R52259': { isin: 'IE00BHZPJ569', name: 'iShares MSCI ACWI SRI', ter: 0.20, strategy: 'SRI' },
          'DE0005933931': { isin: 'IE00BZ02LR44', name: 'iShares MSCI Europe SRI', ter: 0.20, strategy: 'SRI' },
        };

        const classifyESG = (name: string) => {
          const n = name.toUpperCase();
          if (n.includes('SRI') || n.includes('SUSTAINABLE') || n.includes('PAB') || n.includes('CTB') || n.includes('ESG')) return true;
          return false;
        };

        const nonESG = holdings.filter(h => !classifyESG(h.name));
        const withAlts = nonESG.filter(h => esgAlternatives[h.security_isin]);
        const withoutAlts = nonESG.filter(h => !esgAlternatives[h.security_isin]);

        if (nonESG.length === 0) return null;

        return (
          <div className="border-t border-divider pt-6 py-3 md:py-5">
            <h2 className="font-serif text-heading text-ink mb-1 px-1 md:px-0">ESG Alternatives</h2>
            <p className="text-[13px] text-ink-muted mb-3 md:mb-4 px-1 md:px-0">
              Suggested ESG-screened equivalents for your non-ESG holdings.
            </p>

            {withAlts.length > 0 && (
              <div className="space-y-2 px-1 md:px-0">
                {withAlts.map(h => {
                  const alt = esgAlternatives[h.security_isin];
                  const currentTER = 0.22; // approximate
                  const terDiff = alt.ter - currentTER;
                  return (
                    <div key={h.security_isin} className="rounded-xl bg-parchment-deep p-3">
                      <div className="flex items-center justify-between mb-1.5">
                        <p className="text-[13px] font-medium text-ink truncate">{h.name}</p>
                        <span className="text-[11px] text-ink-muted shrink-0 ml-2">{h.security_isin}</span>
                      </div>
                      <div className="flex items-center gap-2 text-[12px]">
                        <span className="text-ink-muted">Switch to:</span>
                        <span className="text-forest font-medium">{alt.name}</span>
                      </div>
                      <div className="flex items-center gap-3 mt-1 text-[11px] text-ink-muted">
                        <span>TER: {alt.ter.toFixed(2)}%{terDiff !== 0 ? ` (${terDiff > 0 ? '+' : ''}${(terDiff * 100).toFixed(0)}bp)` : ''}</span>
                        <span className="text-sage">{alt.strategy}</span>
                        <span>{alt.isin}</span>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}

            {withoutAlts.length > 0 && (
              <div className="mt-3 px-1 md:px-0">
                <p className="text-[11px] text-ink-muted">
                  No ESG alternative mapped for: {withoutAlts.map(h => h.name).join(', ')}
                </p>
              </div>
            )}
          </div>
        );
      })()}

      {/* --- SPENDING TAB --- */}
      {activeTab === 'spending' && <>

      {spendingData && spendingData.months_analyzed > 0 ? (
        <>
          {/* KPI cards */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-2 md:gap-3">
            <div className="border-t border-divider pt-6 p-3">
              <p className="text-[12px] text-ink-muted mb-1">Avg Monthly Expense</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums">{fmt(spendingData.avg_monthly_expense)}</p>
            </div>
            <div className="border-t border-divider pt-6 p-3">
              <p className="text-[12px] text-ink-muted mb-1">Savings Rate{spendingData.window_months > 0 && spendingData.window_months < spendingData.months_analyzed && <span className="text-ink-muted"> ({spendingData.window_months}m)</span>}</p>
              <p className={`font-serif text-[20px] font-semibold tabular-nums ${spendingData.savings_rate_pct_12m >= 20 ? 'text-sage' : spendingData.savings_rate_pct_12m >= 0 ? 'text-amber' : 'text-claret'}`}>
                {spendingData.savings_rate_pct_12m.toFixed(1)}%
              </p>
              {spendingData.window_months > 0 && spendingData.window_months < spendingData.months_analyzed && (
                <p className="text-[11px] text-ink-muted mt-0.5">Lifetime: {spendingData.savings_rate_pct.toFixed(1)}%</p>
              )}
            </div>
            <div className="border-t border-divider pt-6 p-3">
              <p className="text-[12px] text-ink-muted mb-1">Total Income</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-sage">{fmt(spendingData.total_income)}</p>
            </div>
            <div className="border-t border-divider pt-6 p-3">
              <p className="text-[12px] text-ink-muted mb-1">Total Expenses</p>
              <p className="font-serif text-[20px] font-semibold tabular-nums text-claret">{fmt(spendingData.total_expenses)}</p>
            </div>
          </div>

          {/* Category breakdown donut */}
          {spendingData.categories.length > 0 && (
            <div className="border-t border-divider pt-6 py-3 md:py-5">
              <h3 className="font-serif text-heading text-ink mb-2">Expense Categories</h3>
              <div className="md:flex md:gap-4">
                <div className="md:w-1/2">
                  <EChartWrapper option={{
                    tooltip: { trigger: 'item' as const, formatter: (p: unknown) => { const d = p as { name: string; value: number; percent: number }; return `${d.name}: ${fmt(d.value)} (${d.percent.toFixed(1)}%)`; } },
                    series: [{ type: 'pie' as const, radius: ['40%', '70%'], data: spendingData.categories.slice(0, 8).map(c => ({ name: c.category, value: c.total })), label: { fontSize: 10 }, itemStyle: { borderRadius: 4, borderColor: tc.parchmentDeep, borderWidth: 2 } }],
                  }} height="200px" />
                </div>
                <div className="md:w-1/2 space-y-1 mt-2 md:mt-0">
                  {spendingData.categories.map((c, i) => (
                    <div key={i} className="flex items-center justify-between text-[12px]">
                      <span className="text-ink">{c.category}</span>
                      <span className="tabular-nums text-ink-muted">{fmt(c.avg_monthly)}/mo</span>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}

          {/* Monthly income vs expenses trend */}
          {spendingData.monthly.length > 1 && (
            <div className="border-t border-divider pt-6 py-3 md:py-5">
              <h3 className="font-serif text-heading text-ink mb-2">Monthly Cash Flow</h3>
              <EChartWrapper option={{
                tooltip: { trigger: 'axis' as const },
                legend: { data: ['Income', 'Expenses'], bottom: 0, textStyle: { fontSize: 10 } },
                grid: { top: 10, right: 10, bottom: spendingData.monthly.length > 12 ? 50 : 30, left: 45 },
                xAxis: { type: 'category' as const, data: spendingData.monthly.map(m => { const d = new Date(m.month + '-01'); return d.toLocaleDateString('de-DE', { month: 'short', year: '2-digit' }); }), axisLabel: { fontSize: 9, interval: Math.max(Math.floor(spendingData.monthly.length / 6) - 1, 0), rotate: spendingData.monthly.length > 12 ? 30 : 0 } },
                yAxis: { type: 'value' as const, axisLabel: { fontSize: 10, formatter: (v: number) => `${Math.round(v/1000)}K` } },
                series: [
                  { name: 'Income', type: 'bar' as const, data: spendingData.monthly.map(m => Math.round(m.income)), itemStyle: { color: tc.sage } },
                  { name: 'Expenses', type: 'bar' as const, data: spendingData.monthly.map(m => Math.round(m.expenses)), itemStyle: { color: tc.claret } },
                ],
              }} height="220px" />
            </div>
          )}

          {/* Subscriptions */}
          {(spendingData.subscriptions ?? []).length > 0 && (
            <div className="border-t border-divider pt-6 py-3 md:py-5">
              <h3 className="font-serif text-heading text-ink mb-2">Detected Subscriptions</h3>
              <div className="space-y-1.5">
                {spendingData.subscriptions.map((s, i) => (
                  <div key={i} className="flex items-center justify-between text-[12px]">
                    <div className="min-w-0 flex-1">
                      <span className="font-medium text-ink capitalize">{s.name}</span>
                      <span className="text-ink-muted ml-1">×{s.occurrences}</span>
                    </div>
                    <div className="text-right shrink-0 ml-2 tabular-nums">
                      <span className="text-ink">{fmt(s.amount)}/mo</span>
                      <span className="text-ink-muted ml-1">({fmt(s.annual_cost)}/yr)</span>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      ) : (
        <div className="border-t border-divider pt-6 p-5 text-center text-[16px] text-ink-muted">
          No spending data available. Import checking account transactions (N26, Sparkasse) to see expense analytics.
        </div>
      )}

      </>}
    </div>
  );
}
