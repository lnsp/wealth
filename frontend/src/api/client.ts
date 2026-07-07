const BASE = '/api';

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const resp = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    const detail = err.error || resp.statusText || 'Unknown error';
    throw new Error(resp.status >= 500
      ? `Server error (${resp.status}): ${detail}`
      : `Request failed (${resp.status}): ${detail}`);
  }
  return resp.json();
}

export interface Account {
  id: string;
  name: string;
  institution: string;
  type: string;
  currency: string;
  iban: string | null;
  is_active: boolean;
  tax_treatment: string;
  employer_match_pct: number | null;
  import_security_isin?: string | null;
  balance?: number;
  cash_balance?: number;
  holdings_value?: number;
}

export interface TransactionRow {
  id: string;
  account_id: string;
  date: string;
  type: string;
  security_isin: string | null;
  quantity: number | null;
  price: number | null;
  amount: number;
  fee: number;
  tax: number;
  currency: string;
  counterparty: string | null;
  reference: string | null;
  category: string | null;
  account_name: string;
  institution: string;
}

export interface CashflowMonth {
  month: string; // YYYY-MM
  income: number;
  fixed: number;
  variable: number;
  transfer: number;
  net: number;
}
export interface CashflowBucket {
  month: string;
  category: string;
  bucket: 'income' | 'fixed' | 'variable' | 'transfer';
  amount: number;
  count: number;
}
export interface CashflowData {
  from: string;
  to: string;
  months: CashflowMonth[];
  buckets: CashflowBucket[];
  medians: {
    monthly_income: number;
    monthly_spend: number;
    monthly_surplus: number;
    annual_gross_income: number;
  };
}

export interface HoldingRow {
  account_id: string;
  account_name: string;
  security_isin: string;
  quantity: number;
  avg_cost_basis: number;
  total_dividends: number;
  name: string;
  symbol: string | null;
  asset_class: string;
  currency: string;
  current_price: number | null;
  market_value: number | null;
  unrealized_pl: number | null;
  weight_pct: number | null;
  fx_exposure?: string;
  fx_impact_pct?: number;
  asset_return_pct?: number;
}

export interface NetWorthSnapshot {
  date: string;
  total: number;
  cash_component: number;
  investment_component: number;
}

export interface ImportResult {
  imported: number;
  skipped: number;
  new_securities: string[];
  institution: string;
  errors?: string[];
  warnings?: string[];
  account_type?: string;
  rsu_vests?: number;
}

export interface UnvestedVest {
  vest_date: string;
  grant_number: string;
  gross_quantity: number;
  net_estimate: number;
  value_estimate: number;
  value_estimate_eur: number;
}

export interface UnvestedAccount {
  account_id: string;
  account_name: string;
  security_isin: string;
  symbol?: string;
  currency: string;
  ratio: number;
  default_ratio_used: boolean;
  total_gross: number;
  total_net_estimate: number;
  current_price: number;
  current_price_currency: string;
  total_value_estimate: number;
  total_value_eur: number;
  by_vest: UnvestedVest[];
}

export interface UnvestedResponse {
  accounts: UnvestedAccount[];
  total_value_by_currency: Record<string, number>;
  total_value_eur: number;
}

export interface PerformanceData {
  irr: number;
  twr: number;
  total_invested: number;
  total_withdrawn: number;
  transferred_in: number;
  current_value: number;
  total_return: number;
  total_return_pct: number;
  realized_pl: number;
  unrealized_pl: number;
}

export interface PerformanceHistoryPoint {
  date: string;
  portfolio_value: number;
  cash_invested: number;
  in_kind_invested: number;
  return_pct: number;
  benchmark_pct?: number;
}

export interface DividendData {
  total: number;
  monthly: { month: string; amount: number }[];
  yearly: { year: string; amount: number }[];
  by_security: { isin: string; name: string; amount: number }[];
  cumulative: { month: string; amount: number; cumulative: number }[];
  trailing_12m: number;
  yield_on_cost: number;
  dividend_growth: number;
  calendar?: { month: string; isin: string; name: string; expected: number }[];
}

export interface ETFHoldingEntry {
  isin: string;
  name: string;
  weight: number;
  sector?: string;
  country?: string;
}

export interface ConcentrationAlert {
  type: 'overlap' | 'concentration';
  level: 'warning' | 'critical';
  message: string;
  value: number;
}

export interface TopHolding {
  isin: string;
  name: string;
  exposure_pct: number;
}

export interface TreemapNode {
  name: string;
  value?: number;
  children?: TreemapNode[];
}

export interface ImportHistoryEntry {
  id: string;
  account_id: string;
  imported_at: string;
  institution: string;
  filename: string;
  total_rows: number;
  imported: number;
  skipped: number;
  new_securities: string[];
  account_name: string;
}

export interface Security {
  isin: string;
  wkn: string | null;
  symbol: string | null;
  name: string;
  asset_class: string;
  currency: string;
  ter: number | null;
}

export interface SectorHistoryPoint {
  date: string;
  sectors: Record<string, number>;
}

export interface DrawdownPoint {
  date: string;
  drawdown: number;
}

export interface RiskMetrics {
  annualized_volatility: number;
  sharpe_ratio: number;
  sortino_ratio: number;
  max_drawdown: number;
  max_drawdown_start: string;
  max_drawdown_end: string;
  max_drawdown_days: number;
  value_at_risk_95: number;
  current_drawdown: number;
  all_time_high: number;
  ath_date: string;
  drawdown_series: DrawdownPoint[];
}

export interface RollingMetric {
  date: string;
  volatility: number;
  sharpe: number;
}

export interface RiskData {
  risk: RiskMetrics;
  benchmark_risk?: RiskMetrics;
  rolling?: Record<string, RollingMetric[]>;
}

export interface TaxBySecurity {
  isin: string;
  name: string;
  realized_pl: number;
  teilfreistellung: number;
  taxable_pl: number;
  is_equity_fund: boolean;
}

export interface TaxLossHint {
  isin: string;
  name: string;
  unrealized_pl: number;
  potential_saving: number;
}

export interface TaxSummary {
  year: number;
  realized_gains: number;
  realized_losses: number;
  net_gain: number;
  teilfreistellung_amt: number;
  taxable_gain: number;
  freistellung_used: number;
  freistellung_remaining: number;
  estimated_tax: number;
  effective_rate: number;
  dividend_income: number;
  by_security: TaxBySecurity[];
  tax_loss_hints?: TaxLossHint[];
}

export interface VorabpauschaleEntry {
  isin: string;
  name: string;
  jan1_value: number;
  year_end_value: number;
  basiszins: number;
  basisertrag: number;
  vorabpauschale: number;
  tax_on_vp: number;
}

export interface TaxData {
  summary: TaxSummary;
  available_years: number[];
  vorabpauschale?: VorabpauschaleEntry[];
}

export interface WealthReportSummary {
  id: string;
  report_type: string;
  period_label: string;
  generated_at: string;
}

export interface WealthReportDetail {
  id: string;
  report_type: string;
  period_label: string;
  period_start: string;
  period_end: string;
  generated_at: string;
  data: {
    net_worth_start: number;
    net_worth_end: number;
    net_worth_change: number;
    net_worth_change_pct: number;
    total_dividends: number;
    new_transactions: number;
    holdings: { isin: string; name: string; value: number; weight: number; return_pct: number }[];
    top_gainer?: { isin: string; name: string; return_pct: number };
    top_loser?: { isin: string; name: string; return_pct: number };
  };
}

export interface PriceAlertEntry {
  id: string;
  alert_type: string;
  security_isin?: string;
  security_name?: string;
  threshold: number;
  is_active: boolean;
  created_at: string;
}

export interface NotificationEntry {
  id: string;
  alert_type: string;
  message: string;
  value: number;
  is_read: boolean;
  triggered_at: string;
}

export interface CostEntry {
  isin: string;
  name: string;
  ter: number;
  value: number;
  weight: number;
  annual_cost: number;
}

export interface CostData {
  holdings: CostEntry[];
  total_value: number;
  weighted_ter: number;
  weighted_ter_covered_only: number;
  coverage_pct: number;
  annual_cost: number;
  daily_cost: number;
  projection: { year: number; cumulative: number }[];
  benchmark?: {
    avg_ter: number;
    your_ter: number;
    grade: string;
    detail: string;
    annual_saving: number;
    ten_year_saving: number;
  };
  total_cost_ownership?: {
    ter_cost: number;
    transaction_fees: number;
    spread_estimate: number;
    total_annual: number;
    total_annual_pct: number;
    lifetime_fees: number;
    lifetime_volume: number;
    transaction_count: number;
  };
}

export interface TaxLot {
  isin: string;
  name: string;
  buy_date: string;
  quantity: number;
  cost_basis: number;
  current_value: number;
  unrealized_pl: number;
  is_equity_fund: boolean;
  tax_if_sold: number;
  net_proceeds: number;
  effective_rate: number;
}

export interface CurrencyExposureEntry {
  currency: string;
  value: number;
  pct: number;
}

export interface AllocationEntry {
  isin: string;
  name: string;
  actual_pct: number;
  target_pct: number;
  drift_pct: number;
  value: number;
  status: 'on_target' | 'underweight' | 'overweight';
}

export interface SavingsRateData {
  monthly: { month: string; deposits: number; withdrawals: number; net_savings: number; rate: number }[];
  avg_savings_rate: number;
  trailing_12m_rate: number;
  total_deposits: number;
  total_withdrawals: number;
  total_net_savings: number;
}

export interface SensitivityBar {
  label: string;
  low: number;
  high: number;
  base: number;
}

export interface ProjectionData {
  history: { date: string; value: number }[];
  projection: { date: string; value: number; p10?: number; p90?: number }[];
  current_value: number;
  target_amount: number;
  target_date: string;
  contribution: number;
  return_pct: number;
  horizon_years?: number;
  contrib_growth_pct?: number;
  has_confidence?: boolean;
  sensitivity?: SensitivityBar[];
  projection_end?: number;
  milestones?: { year: number; years_from_now: number; projected_value: number; contributions: number; growth: number; real_value: number }[];
  projection_accuracy?: {
    months_ago: number;
    past_value: number;
    projected_value: number;
    actual_value: number;
    diff_eur: number;
    diff_pct: number;
  };
  drawdown?: {
    series: { date: string; value: number }[];
    fire_number: number;
    fire_date: string;
    years_to_fire: number;
    longevity_years: number;
    success_rate: number;
    tax_breakdown?: { year: number; gross_withdrawal: number; estimated_tax: number; net_received: number; effective_rate_pct: number; remaining_value: number }[];
    cumulative_tax: number;
  };
}

export interface RebalanceTrade {
  isin: string;
  name: string;
  action: 'buy' | 'sell';
  amount: number;
  shares?: number;
  current_pct: number;
  target_pct: number;
}

export interface AllocationData {
  allocations: AllocationEntry[];
  has_targets: boolean;
  max_drift: number;
  total_value: number;
}

export interface GoalProgress {
  id: string;
  name: string;
  target_amount: number;
  target_date: string;
  current_value: number;
  progress_pct: number;
  projected_value: number;
  monthly_contribution: number;
  assumed_return_pct: number;
  status: 'on_track' | 'behind' | 'complete' | 'ahead';
  months_remaining: number;
  funding_account_id: string | null;
  priority: number;
}

export interface PasskeyEntry {
  id: string;
  name: string;
  created_at: string;
}

// Minimal WebAuthn JSON types (browser API returns ArrayBuffers, server uses base64url JSON)
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type PublicKeyCredentialCreationOptionsJSON = any;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
type PublicKeyCredentialRequestOptionsJSON = any;

export const api = {
  // Accounts
  listAccounts: () => request<{ accounts: Account[] }>('/portfolio/accounts'),
  createAccount: (data: { name: string; institution: string; type: string; currency?: string; iban?: string; tax_treatment?: string; employer_match_pct?: number; import_security_isin?: string }) =>
    request<Account>('/settings/accounts', { method: 'POST', body: JSON.stringify(data) }),
  updateAccount: (id: string, data: { name?: string; iban?: string; is_active?: boolean; tax_treatment?: string; employer_match_pct?: number; currency?: string; import_security_isin?: string }) =>
    request<{ status: string }>(`/settings/accounts/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
  deleteAccount: (id: string) =>
    request<{ status: string }>(`/settings/accounts/${id}`, { method: 'DELETE' }),

  // Import
  importCSV: async (file: File, accountId: string): Promise<ImportResult> => {
    const form = new FormData();
    form.append('file', file);
    form.append('account_id', accountId);
    const resp = await fetch(`${BASE}/import`, { method: 'POST', body: form });
    if (!resp.ok) {
      const err = await resp.json().catch(() => ({ error: resp.statusText }));
      throw new Error(err.error || 'Import failed');
    }
    return resp.json();
  },

  // Transactions
  listTransactions: (limit = 50, offset = 0, type = '', search = '', from = '', to = '') => {
    const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (type) params.set('type', type);
    if (search) params.set('search', search);
    if (from) params.set('from', from);
    if (to) params.set('to', to);
    return request<{ transactions: TransactionRow[]; total: number }>(`/transactions?${params}`);
  },
  updateTransactionCategory: (id: string, category: string) =>
    request<{ ok: boolean }>(`/transactions/${id}/category`, { method: 'PATCH', body: JSON.stringify({ category }) }),

  // Cashflow
  getCashflow: (months = 12) => request<CashflowData>(`/cashflow?months=${months}`),

  // Portfolio
  listHoldings: (accountId?: string) =>
    request<{ holdings: HoldingRow[]; price_as_of?: string }>(
      `/portfolio/holdings${accountId ? `?account_id=${accountId}` : ''}`
    ),
  getNetWorth: (days = 365) => request<{ snapshots: NetWorthSnapshot[] }>(`/portfolio/networth?days=${days}`),
  getNetWorthIntraday: () => request<{ points: { recorded_at: string; total: number; cash_component: number; investment_component: number }[] }>('/portfolio/networth/intraday'),
  getPerformance: (accountId?: string) =>
    request<PerformanceData>(`/portfolio/performance${accountId ? `?account_id=${accountId}` : ''}`),
  getPerformanceHistory: () => request<{ history: PerformanceHistoryPoint[] }>('/portfolio/performance-history'),
  getDividends: () => request<DividendData>('/portfolio/dividends'),
  getAllocation: () => request<AllocationData>('/portfolio/allocation'),
  getSavingsRate: () => request<SavingsRateData>('/portfolio/savings-rate'),
  getRebalance: (deposit?: number) =>
    request<{ trades: RebalanceTrade[]; message: string; deposit: number }>(
      `/portfolio/rebalance${deposit ? `?deposit=${deposit}` : ''}`
    ),
  setAllocation: (allocations: { isin: string; target_pct: number }[]) =>
    request<{ status: string }>('/portfolio/allocation', {
      method: 'PUT',
      body: JSON.stringify({ allocations }),
    }),

  // Analysis
  getAllocationSummary: () => request<{ sectors: Record<string, number>; countries: Record<string, number>; currencies: CurrencyExposureEntry[] }>('/analysis/summary'),
  getSectors: () => request<{ sectors: Record<string, number> }>('/analysis/sectors'),
  getCountries: () => request<{ countries: Record<string, number> }>('/analysis/countries'),
  getOverlap: () => request<{ labels: string[]; matrix: number[][] }>('/analysis/overlap'),
  getAlerts: () => request<{ alerts: ConcentrationAlert[] }>('/analysis/alerts'),
  getTopHoldings: () => request<{ holdings: TopHolding[] }>('/analysis/top-holdings'),
  getTreemap: () => request<{ children: TreemapNode[] }>('/analysis/treemap'),
  getSectorHistory: () => request<{ history: SectorHistoryPoint[]; sectors: string[] }>('/analysis/sector-history'),
  getETFHoldings: (isin: string) =>
    request<{ etf_isin: string; etf_name: string; holdings: ETFHoldingEntry[] }>(`/analysis/etf/${isin}/holdings`),
  getRisk: () => request<RiskData>('/analysis/risk'),
  getTax: (year?: number) => request<TaxData>(`/analysis/tax${year ? `?year=${year}` : ''}`),
  getCurrency: () => request<{ currencies: CurrencyExposureEntry[] }>('/analysis/currency'),
  getCosts: () => request<CostData>('/analysis/costs'),
  getAlternatives: () => request<{ alternatives: { isin: string; name: string; current_ter: number; value: number; alternatives: { isin: string; name: string; ter: number; annual_saving: number; ten_year_saving: number }[] }[] }>('/analysis/alternatives'),
  getTaxLots: () => request<{ lots: TaxLot[] }>('/analysis/tax-lots'),
  simulateSell: (requests: { isin: string; amount_eur: number }[], churchTaxRate?: number) => {
    const qs = churchTaxRate != null && churchTaxRate > 0 ? `?church_tax=${churchTaxRate}` : '';
    return request<{ results: { isin: string; name: string; sell_amount: number; cost_basis: number; realized_gain: number; teilfreistellung: number; taxable_gain: number; estimated_tax: number; net_proceeds: number; lots_consumed: number; is_equity_fund: boolean }[]; total_tax: number; total_proceeds: number; effective_rate: number; church_tax_rate: number }>(
      `/analysis/sell-simulator${qs}`, { method: 'POST', body: JSON.stringify(requests) });
  },
  getCorrelation: () => request<{ labels: string[]; matrix: number[][] }>('/analysis/correlation'),
  getAllocationHistory: () => request<{ history: { date: string; weights: Record<string, number> }[]; holdings: string[] }>('/analysis/allocation-history'),
  getFXHistory: () => request<{ rates: Record<string, { date: string; rate: number }[]> }>('/analysis/fx-history'),
  // Users
  listUsers: () => request<{ users: { id: string; username: string; role: string; is_active: boolean; created_at: string; totp_enabled: boolean }[] }>('/settings/users'),
  setupTOTP: (userId: string) => request<{ secret: string; url: string }>(`/settings/users/${userId}/totp/setup`, { method: 'POST' }),
  verifyTOTP: (userId: string, code: string) => request<{ status: string }>(`/settings/users/${userId}/totp/verify`, { method: 'POST', body: JSON.stringify({ code }) }),
  disableTOTP: (userId: string) => request<{ status: string }>(`/settings/users/${userId}/totp`, { method: 'DELETE' }),
  createUser: (data: { username: string; password: string; role?: string }) =>
    request<{ id: string; username: string; role: string }>('/settings/users', { method: 'POST', body: JSON.stringify(data) }),
  deleteUser: (id: string) => request<{ status: string }>(`/settings/users/${id}`, { method: 'DELETE' }),
  toggleUser: (id: string) => request<{ is_active: boolean }>(`/settings/users/${id}/toggle`, { method: 'PUT' }),

  getUnvested: () => request<UnvestedResponse>('/portfolio/unvested'),

  getAttribution: () => request<{ contributions: { isin: string; name: string; change: number }[]; total_change: number; summary: string }>('/portfolio/attribution'),
  getSavingsPlans: () => request<{ plans: { isin: string; name: string; monthly_amount: number; executions: number; total_invested: number; total_shares: number; avg_cost_basis: number; first_date: string; last_date: string; months_active: number; current_value: number; dca_return_pct: number; lump_sum_value: number; lump_sum_return_pct: number; dca_advantage_eur: number }[]; total_monthly: number; plan_count: number }>('/portfolio/savings-plans'),
  getSecurityDetail: (isin: string) => request<{
    isin: string; name: string; symbol: string; asset_class: string; ter: number; price: number;
    positions: { account: string; quantity: number; cost_basis: number; value: number }[];
    total_quantity: number; total_cost: number; total_value: number; unrealized_pl: number;
    weight_pct: number; first_buy: string;
    sparkline: { date: string; price: number }[];
    transactions: { date: string; type: string; quantity: number; amount: number; running_qty: number }[];
  }>(`/portfolio/security/${isin}`),
  getHealthScore: () => request<{ score: number; subscores: { name: string; score: number; weight: number; status: string; detail: string }[] }>('/analysis/health-score'),
  getBenchmarkComparison: (isin?: string) => request<{ benchmark_name: string; actual_value: number; benchmark_value: number; difference: number; comparison: { date: string; actual: number; benchmark: number }[] }>(`/analysis/benchmark-comparison${isin ? `?isin=${isin}` : ''}`),
  getInflation: () => request<{ history: { date: string; nominal: number; real: number }[]; nominal_return?: number; real_return?: number; purchasing_power_lost?: number }>('/analysis/inflation'),
  getCashFlow: () => request<{ history: { month: string; income: number; expenses: number; net: number }[]; projection: { month: string; income: number; expenses: number; net: number }[]; avg_income: number; avg_expenses: number; avg_net: number }>('/analysis/cash-flow'),

  // Settings
  listAllAccounts: () => request<{ accounts: Account[] }>('/settings/accounts'),
  getImportHistory: () => request<{ history: ImportHistoryEntry[] }>('/import-history'),

  // Goals
  listGoals: () => request<{ goals: GoalProgress[] }>('/settings/goals'),
  getGoalsProgress: () => request<{ goals: GoalProgress[] }>('/portfolio/goals'),
  getProjection: (contribution?: number, returnPct?: number, expenses?: number, swr?: number, marginalRate?: number, taxPortion?: number, horizonYears?: number, contribGrowth?: number) => {
    const params = new URLSearchParams();
    if (contribution != null) params.set('contribution', String(contribution));
    if (returnPct != null) params.set('return_pct', String(returnPct));
    if (expenses != null && expenses > 0) params.set('expenses', String(expenses));
    if (swr != null && swr > 0) params.set('swr', String(swr));
    if (marginalRate != null && marginalRate >= 0 && marginalRate <= 1) params.set('marginal_rate', String(marginalRate));
    if (taxPortion != null && taxPortion >= 0 && taxPortion <= 1) params.set('tax_portion', String(taxPortion));
    if (horizonYears != null && horizonYears >= 1 && horizonYears <= 50) params.set('horizon_years', String(horizonYears));
    if (contribGrowth != null && contribGrowth >= 0 && contribGrowth <= 20) params.set('contrib_growth', String(contribGrowth));
    const qs = params.toString();
    return request<ProjectionData>(`/portfolio/projection${qs ? `?${qs}` : ''}`);
  },
  createGoal: (data: { name: string; target_amount: number; target_date: string; monthly_contribution?: number; assumed_return_pct?: number }) =>
    request<{ id: string; name: string }>('/settings/goals', { method: 'POST', body: JSON.stringify(data) }),
  deleteGoal: (id: string) =>
    request<{ status: string }>(`/settings/goals/${id}`, { method: 'DELETE' }),

  // Alerts & Notifications
  listAlerts: () => request<{ alerts: PriceAlertEntry[] }>('/settings/alerts'),
  createAlert: (data: { alert_type: string; security_isin?: string; threshold: number }) =>
    request<{ id: string }>('/settings/alerts', { method: 'POST', body: JSON.stringify(data) }),
  deleteAlert: (id: string) =>
    request<{ status: string }>(`/settings/alerts/${id}`, { method: 'DELETE' }),
  toggleAlert: (id: string) =>
    request<{ status: string }>(`/settings/alerts/${id}/toggle`, { method: 'PUT' }),
  listNotifications: () => request<{ notifications: NotificationEntry[]; unread_count: number }>('/notifications'),
  markNotificationsRead: () =>
    request<{ status: string }>('/notifications/read', { method: 'POST' }),

  // Reports
  listReports: () => request<{ reports: WealthReportSummary[] }>('/settings/reports'),
  getReport: (id: string) => request<WealthReportDetail>(`/settings/reports/${id}`),
  generateReport: (data: { report_type: string; year: number; month?: number }) =>
    request<{ status: string; period: string }>('/settings/reports', { method: 'POST', body: JSON.stringify(data) }),
  listSecurities: () => request<{ securities: Security[] }>('/settings/securities'),
  updateSecuritySymbol: (isin: string, symbol: string) =>
    request<{ status: string }>(`/settings/securities/${isin}/symbol`, {
      method: 'PUT',
      body: JSON.stringify({ symbol }),
    }),

  // WebAuthn / Passkeys
  listPasskeys: () => request<{ passkeys: PasskeyEntry[] }>('/auth/webauthn/passkeys'),
  deletePasskey: (id: string) => request<{ status: string }>(`/auth/webauthn/passkeys/${id}`, { method: 'DELETE' }),
  webauthnRegisterBegin: (name?: string) => {
    const qs = name ? `?name=${encodeURIComponent(name)}` : '';
    return request<PublicKeyCredentialCreationOptionsJSON>(`/auth/webauthn/register/begin${qs}`, { method: 'POST' });
  },
  webauthnRegisterFinish: (body: unknown, name?: string) => {
    const qs = name ? `?name=${encodeURIComponent(name)}` : '';
    return request<{ status: string }>(`/auth/webauthn/register/finish${qs}`, { method: 'POST', body: JSON.stringify(body) });
  },
  webauthnLoginBegin: (username?: string) =>
    request<PublicKeyCredentialRequestOptionsJSON>('/auth/webauthn/login/begin', { method: 'POST', body: JSON.stringify({ username: username || '' }) }),
  webauthnLoginFinish: (body: unknown) =>
    request<{ status: string }>('/auth/webauthn/login/finish', { method: 'POST', body: JSON.stringify(body) }),
};
