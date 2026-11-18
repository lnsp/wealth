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
  salary: number; // take-home pay, part of income
  fixed: number;
  variable: number;
  transfer: number;
  unclassified: number; // money in that matched no rule: neither income nor spend
  net: number;
  partial: boolean; // the running month, left out of the medians
  has_data: boolean;
}
export interface CashflowBucket {
  month: string;
  category: string;
  bucket: 'income' | 'fixed' | 'variable' | 'transfer' | 'unclassified';
  amount: number;
  count: number;
}
export interface CashflowData {
  from: string;
  to: string;
  months: CashflowMonth[];
  buckets: CashflowBucket[];
  /** Over the last 12 complete months that have income. */
  medians: {
    monthly_income: number;
    monthly_spend: number;
    monthly_surplus: number; // median of monthly net
    savings_rate_pct: number | null; // Σnet ÷ Σincome × 100
    annual_take_home: number; // net pay, not gross
    months: number;
    window_months: number;
  };
  unconverted_currencies: string[];
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
  price_currency?: string;
  current_price: number | null;
  market_value: number | null;
  value_eur: number;
  price_missing: boolean;
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
  physical_component: number;
  debt_component: number;
}

export interface AssetObservation {
  date: string;
  odometer_km: number | null;
  appraised_value: number | null;
  note: string;
}

export type AssetKind = 'car' | 'apartment' | 'house' | 'land' | 'other';

export interface PhysicalAsset {
  id: string;
  kind: AssetKind;
  name: string;
  details: string;
  list_price: number | null;
  purchase_price: number;
  purchase_costs: number;
  cost_basis: number;
  purchase_date: string;
  notarized_date: string | null;
  first_registration: string | null;
  purchase_odometer_km: number;
  annual_depreciation: number;
  km_depreciation: number;
  expected_km_per_year: number;
  land_value: number | null;
  year_built: number | null;
  useful_life_years: number;
  price_drift: number;
  price_index: string;
  self_used: boolean;
  living_area_m2: number | null;
  current_value: number;
  value_change: number;
  value_change_pct: number;
  vs_cost_basis: number;
  monthly_change: number;
  pct_of_list: number | null;
  owed: number;
  yours: number;
  yours_pct: number;
  loan_ids: string[];
  tax_free_from: string | null;
  tax_note: string;
  car: { odometer_estimate_km: number; effective_annual_rate: number } | null;
  property: {
    land_value_now: number;
    building_value_now: number;
    remaining_life_years: number;
    price_effect_per_year: number;
    aging_effect_per_year: number;
    has_building_aging: boolean;
  } | null;
  index: PriceIndexInfo | null;
  observations: AssetObservation[];
  improvements: { id: string; date: string; amount: number; description: string }[];
  linked_transactions: { id: string; date: string; amount: number; counterparty: string; purpose: string }[];
  curve: { date: string; value: number; owed: number; projected: boolean }[];
}

export interface LoanInput {
  asset_id?: string;
  name: string;
  lender: string;
  principal: number;
  payout_date: string;
  first_payment_date?: string;
  nominal_rate: number;
  monthly_payment?: number;
  initial_repayment?: number;
  fixed_until?: string;
  followup_rate?: number;
  extra_allowance_pct: number;
  payment_account_id?: string;
}

export interface AssetInput {
  kind: AssetKind;
  name: string;
  details?: string;
  list_price?: number | null;
  purchase_price: number;
  purchase_costs?: number;
  purchase_date: string;
  notarized_date?: string;
  first_registration?: string;
  purchase_odometer_km?: number;
  annual_depreciation?: number;
  km_depreciation?: number;
  expected_km_per_year?: number;
  land_value?: number;
  year_built?: number;
  useful_life_years?: number;
  price_drift?: number;
  price_index?: string;
  self_used?: boolean;
  living_area_m2?: number;
  transaction_ids?: string[];
  loan?: LoanInput;
}

export interface Loan {
  id: string;
  asset_id: string;
  asset_name: string;
  name: string;
  lender: string;
  principal: number;
  payout_date: string;
  first_payment_date: string;
  nominal_rate: number;
  initial_repayment: number;
  monthly_payment: number;
  fixed_until: string | null;
  followup_rate: number | null;
  extra_allowance_pct: number;
  payment_account_id: string | null;
  balance: number;
  repaid: number;
  repaid_pct: number;
  next_payment: { due: string; interest: number; principal: number } | null;
  remaining_at_fixed_end: number | null;
  months_left_fixed: number | null;
  interest_paid: number;
  interest_fixed_period: number;
  payoff_date: string;
  due_count: number;
  matched_count: number;
  extra_used_this_year: number;
  yearly?: { year: number; interest: number; principal: number; balance: number }[];
  payments?: { transaction_id: string; date: string; due: string; amount: number; interest: number; principal: number }[];
  extras?: { id: string; date: string; amount: number; transaction_id: string | null }[];
}

export interface LoanScenarioResult {
  remaining_at_fixed_end: number;
  payoff_date: string;
  total_interest: number;
  interest_after_fixed: number;
}

export interface PriceIndexInfo {
  id: string;
  label: string;
  latest_quarter: string;
  latest_value: number;
  year_over_year: number | null;
}

export interface AssetsOverview {
  price_indexes: PriceIndexInfo[];
  assets: PhysicalAsset[];
  loans: Loan[];
  totals: { worth: number; owed: number; yours: number };
  composition: { investments: number; cash: number; as_of?: string };
  defaults: {
    annual_depreciation: number;
    km_depreciation: number;
    expected_km_per_year: number;
    property_drift: number;
    useful_life_years: number;
    price_index: string;
  };
}

export interface ImportResult {
  imported: number;
  /** Rows already stored unchanged (same hash, or an equivalent older row). */
  skipped: number;
  /** Rows already stored whose type, security, quantity or price the re-import corrected. */
  updated?: number;
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
  /** Re-imported rows the import corrected (0 for imports before migration 032). */
  updated?: number;
  /** RSU vesting rows (vested or scheduled) from Morgan Stanley files. */
  rsu_vests?: number;
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
  /** Level of the return index on that date, for peak-to-trough within a sub-period. */
  value?: number;
}

export interface RiskMetrics {
  annualized_volatility: number;
  sharpe_ratio: number;
  sortino_ratio: number;
  max_drawdown: number;
  max_drawdown_start: string;
  max_drawdown_end: string;
  max_drawdown_days: number;
  /** One trading day, 95 %, EUR (negative = loss). */
  value_at_risk_95: number;
  /** % below the return index's peak on `peak_date`. */
  current_drawdown: number;
  peak_date?: string;
  /** Highest recorded value (not the index). */
  all_time_high: number;
  ath_date: string;
  series_start?: string;
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
  /** Average ECB deposit facility rate over the measured window, in %. */
  risk_free_rate?: number;
}

export interface TaxBySecurity {
  isin: string;
  name: string;
  realized_pl: number;
  teilfreistellung: number;
  taxable_pl: number;
  is_equity_fund: boolean;
  /** "share" (Aktienverlusttopf) or "general". */
  pot?: string;
  vorabpauschale_deducted?: number;
  account_id?: string;
  date?: string;
}

export interface TaxLossHint {
  isin: string;
  name: string;
  unrealized_pl: number;
  /** Reduction in this year's tax if the depot position were sold today. */
  potential_saving: number;
  account_id?: string;
  account?: string;
  /** Loss after Teilfreistellung. */
  taxable_loss?: number;
  /** "share" losses only offset share gains; "general" losses offset any capital income. */
  pot?: string;
}

/** §23 EStG private sales (crypto, physical gold): personal income-tax rate, not Abgeltungsteuer. */
export interface PrivateSaleSummary {
  year: number;
  taxable_gains: number;
  taxable_losses: number;
  net_taxable: number;
  tax_free_pl: number;
  freigrenze: number;
  below_freigrenze: boolean;
  taxable_amount: number;
  other_income: number;
  sales: { date: string; isin: string; name: string; quantity: number; proceeds: number; cost_basis: number; taxable_pl: number; tax_free_pl: number }[];
}

/** Filing settings the Tax page keeps in the browser and passes to the tax endpoints. */
export interface TaxFilingOptions {
  joint?: boolean;
  /** Kirchensteuer rate: 0, 0.08 or 0.09. */
  churchTax?: number;
}

function taxQuery(opts?: TaxFilingOptions, extra?: Record<string, string | number>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(extra ?? {})) q.set(k, String(v));
  if (opts?.joint) q.set('joint', '1');
  if (opts?.churchTax && opts.churchTax > 0) q.set('church_tax', String(opts.churchTax));
  const s = q.toString();
  return s ? `?${s}` : '';
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
  interest_income?: number;
  /** Vorabpauschale computed for the previous year, deemed received in January of this year. */
  vorabpauschale_income?: number;
  vorabpauschale_year?: number;
  share_gains?: number;
  share_losses?: number;
  general_gains?: number;
  general_losses?: number;
  loss_carry_in_share?: number;
  loss_carry_in_general?: number;
  loss_carry_out_share?: number;
  loss_carry_out_general?: number;
  /** Sparer-Pauschbetrag for the year and filing status. */
  allowance?: number;
  joint?: boolean;
  church_tax_rate?: number;
  /** Abgeltungsteuer + Soli (+ church tax) as a fraction. */
  tax_rate?: number;
  private_sales?: PrivateSaleSummary | null;
}

export interface VorabpauschaleEntry {
  year?: number;
  isin: string;
  name: string;
  units?: number;
  jan1_price?: number;
  year_end_price?: number;
  /** Units held at year end, valued at the 1 January price. */
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
  /** Vorabpauschale computed for calendar year `vorabpauschale_year`, taxed in January of `vorabpauschale_taxed_in`. */
  vorabpauschale?: VorabpauschaleEntry[];
  vorabpauschale_year?: number;
  vorabpauschale_taxed_in?: number;
  /** True for the running year: today's price stands in for the year-end price. */
  vorabpauschale_estimate?: boolean;
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
  /** Figures are recomputed from current data on every read. */
  computed_at?: string;
  data: {
    /** false when the period has no net worth history; the net worth figures are then 0. */
    has_net_worth?: boolean;
    /** Snapshot the opening value comes from: the previous period's close. Absent = no earlier history (start 0). */
    start_date?: string;
    end_date?: string;
    net_worth_start: number;
    net_worth_end: number;
    net_worth_change: number;
    net_worth_change_pct: number;
    /** External flows in the period: deposits and in-kind receipts (RSU vests) minus withdrawals. */
    net_deposits?: number;
    /** net_worth_change − net_deposits: price moves, income, fees, taxes, revaluations. */
    market_change?: number;
    /** Net: credited after tax and fees withheld at source, at the payment-date FX. */
    total_dividends: number;
    dividends_gross?: number;
    dividends_withheld?: number;
    warnings?: string[];
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
  account_id?: string;
  isin: string;
  name: string;
  buy_date: string;
  quantity: number;
  cost_basis: number;
  current_value: number;
  unrealized_pl: number;
  is_equity_fund: boolean;
  tfs_rate?: number;
  tax_if_sold: number;
  net_proceeds: number;
  effective_rate: number;
  asset_class?: string;
  /** "share", "general" or "private_sale". */
  pot?: string;
  private_sale?: boolean;
  /** §23: first day a sale of this lot is tax-free. */
  tax_free_from?: string;
  vorabpauschale_taxed?: number;
  /** Tax of selling this lot and every older lot of the same security in the same depot (FIFO), before allowance and loss pots. */
  fifo_tax_if_sold?: number;
}

/** Tax of selling every open lot today. */
export interface SellAllSummary {
  share_pl: number;
  general_pl: number;
  taxable_gain: number;
  /** Pots netted, before the Sparer-Pauschbetrag and carried losses. */
  gross_tax: number;
  /** Change in this year's tax after the remaining allowance and loss pots (negative = saving). */
  tax_this_year: number;
  private_sale_taxable_pl: number;
  private_sale_tax_free_pl: number;
  tax_rate: number;
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
  /** Projection at the target date: ahead ≥ 110 %, on_track ≥ 100 %, at_risk ≥ 80 % of target. */
  status: 'on_track' | 'at_risk' | 'behind' | 'complete' | 'ahead';
  months_remaining: number;
  funding_account_id: string | null;
  priority: number;
  /** What current_value measures (/portfolio/goals only): the funding account, or a share of liquid wealth. */
  funding_source?: 'account' | 'liquid';
  /** Month ("YYYY-MM") the projection first reaches the target; empty if reached or never (/portfolio/goals only). */
  estimated_date?: string;
}

/** /portfolio/attribution: the change between the two latest net-worth snapshots. */
export interface DailyAttribution {
  contributions: { isin: string; name: string; change: number }[];
  /** Market move on held securities (price and FX) — same as market_change. */
  total_change: number;
  summary: string;
  market_change?: number;
  /** Deposits and RSU vests minus withdrawals dated between the snapshots. */
  flows?: number;
  /** Dividends and interest minus fees and taxes dated between the snapshots. */
  income?: number;
  /** The rest: FX on foreign cash, property revaluation, loan steps. */
  other?: number;
  net_change?: number;
  from_date?: string;
  to_date?: string;
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
  listTransactions: (limit = 50, offset = 0, type = '', search = '', from = '', to = '', accountId = '') => {
    const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
    if (type) params.set('type', type);
    if (accountId) params.set('account_id', accountId);
    if (search) params.set('search', search);
    if (from) params.set('from', from);
    if (to) params.set('to', to);
    /** first_date: the oldest transaction's date ("" when there are none), whatever the filters. */
    return request<{ transactions: TransactionRow[]; total: number; first_date?: string }>(`/transactions?${params}`);
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
  getNetWorthIntraday: () => request<{ points: { recorded_at: string; total: number; cash_component: number; investment_component: number; physical_component: number; debt_component: number }[] }>('/portfolio/networth/intraday'),

  // Physical assets and the loans financing them
  listAssets: () => request<AssetsOverview>('/assets'),
  createAsset: (input: AssetInput) =>
    request<PhysicalAsset>('/assets', { method: 'POST', body: JSON.stringify(input) }),
  updateAsset: (id: string, input: AssetInput) =>
    request<PhysicalAsset>(`/assets/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteAsset: (id: string) => request<{ status: string }>(`/assets/${id}`, { method: 'DELETE' }),
  upsertAssetObservation: (id: string, obs: { date: string; odometer_km?: number; appraised_value?: number; note?: string }) =>
    request<PhysicalAsset>(`/assets/${id}/observations`, { method: 'PUT', body: JSON.stringify(obs) }),
  deleteAssetObservation: (id: string, date: string) =>
    request<PhysicalAsset>(`/assets/${id}/observations/${date}`, { method: 'DELETE' }),
  createAssetImprovement: (id: string, imp: { date: string; amount: number; description: string; transaction_id?: string }) =>
    request<PhysicalAsset>(`/assets/${id}/improvements`, { method: 'POST', body: JSON.stringify(imp) }),
  deleteAssetImprovement: (id: string, improvementId: string) =>
    request<PhysicalAsset>(`/assets/${id}/improvements/${improvementId}`, { method: 'DELETE' }),
  getLoan: (id: string) => request<Loan>(`/loans/${id}`),
  createLoan: (input: LoanInput) => request<Loan>('/loans', { method: 'POST', body: JSON.stringify(input) }),
  updateLoan: (id: string, input: LoanInput) =>
    request<Loan>(`/loans/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteLoan: (id: string) => request<{ status: string }>(`/loans/${id}`, { method: 'DELETE' }),
  getLoanScenario: (id: string, extraPerYear: number, followupRate?: number) =>
    request<{ base: LoanScenarioResult; scenario: LoanScenarioResult; interest_saved: number }>(
      `/loans/${id}/scenario?extra_per_year=${extraPerYear}${followupRate != null ? `&followup_rate=${followupRate}` : ''}`
    ),
  createExtraRepayment: (id: string, extra: { date: string; amount: number; transaction_id?: string }) =>
    request<Loan>(`/loans/${id}/extra-repayments`, { method: 'POST', body: JSON.stringify(extra) }),
  deleteExtraRepayment: (id: string, extraId: string) =>
    request<Loan>(`/loans/${id}/extra-repayments/${extraId}`, { method: 'DELETE' }),
  matchLoanPayments: () => request<{ matched: number }>('/loans/match', { method: 'POST' }),
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
  getTax: (year?: number, opts?: TaxFilingOptions) => request<TaxData>(`/analysis/tax${taxQuery(opts, year ? { year } : undefined)}`),
  getCurrency: () => request<{ currencies: CurrencyExposureEntry[] }>('/analysis/currency'),
  getCosts: () => request<CostData>('/analysis/costs'),
  getAlternatives: () => request<{ alternatives: { isin: string; name: string; current_ter: number; value: number; alternatives: { isin: string; name: string; ter: number; annual_saving: number; ten_year_saving: number }[] }[] }>('/analysis/alternatives'),
  getTaxLots: (opts?: TaxFilingOptions) => request<{ lots: TaxLot[]; sell_all?: SellAllSummary }>(`/analysis/tax-lots${taxQuery(opts)}`),
  simulateSell: (requests: { isin: string; amount_eur: number }[], churchTaxRate?: number, joint?: boolean) => {
    const qs = taxQuery({ churchTax: churchTaxRate, joint });
    return request<{
      results: { isin: string; name: string; sell_amount: number; cost_basis: number; realized_gain: number; teilfreistellung: number; taxable_gain: number; estimated_tax: number; net_proceeds: number; lots_consumed: number; is_equity_fund: boolean; tfs_rate?: number; pot?: string; vorabpauschale_deducted?: number; private_sale?: boolean; private_sale_taxable_pl?: number; private_sale_tax_free_pl?: number }[];
      /** Pots netted across the sale, before allowance and carried losses. */
      total_tax: number;
      total_proceeds: number;
      effective_rate: number;
      church_tax_rate: number;
      /** Change in this year's tax after the remaining allowance and loss pots. */
      tax_this_year?: number;
      net_proceeds_this_year?: number;
      allowance_remaining?: number;
      allowance?: number;
    }>(`/analysis/sell-simulator${qs}`, { method: 'POST', body: JSON.stringify(requests) });
  },
  getCorrelation: () => request<{ labels: string[]; matrix: number[][] }>('/analysis/correlation'),
  getAllocationHistory: () => request<{ history: { date: string; weights: Record<string, number> }[]; holdings: string[] }>('/analysis/allocation-history'),
  getFXHistory: () => request<{ rates: Record<string, { date: string; rate: number }[]> }>('/analysis/fx-history'),
  // Users
  listUsers: () => request<{ users: { id: string; username: string; role: string; is_active: boolean; created_at: string; totp_enabled: boolean }[] }>('/settings/users'),
  setupTOTP: (userId: string) => request<{ secret: string; url: string }>(`/settings/users/${userId}/totp/setup`, { method: 'POST' }),
  verifyTOTP: (userId: string, code: string) => request<{ status: string }>(`/settings/users/${userId}/totp/verify`, { method: 'POST', body: JSON.stringify({ code }) }),
  disableTOTP: (userId: string, opts?: { code?: string; password?: string }) =>
    request<{ status: string }>(`/settings/users/${userId}/totp`, {
      method: 'DELETE',
      body: opts ? JSON.stringify(opts) : undefined,
    }),
  createUser: (data: { username: string; password: string; role?: string }) =>
    request<{ id: string; username: string; role: string }>('/settings/users', { method: 'POST', body: JSON.stringify(data) }),
  deleteUser: (id: string) => request<{ status: string }>(`/settings/users/${id}`, { method: 'DELETE' }),
  toggleUser: (id: string) => request<{ is_active: boolean }>(`/settings/users/${id}/toggle`, { method: 'PUT' }),

  getUnvested: () => request<UnvestedResponse>('/portfolio/unvested'),

  getAttribution: () => request<DailyAttribution>('/portfolio/attribution'),
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
  getBenchmarkComparison: (isin?: string) => request<{ benchmark_name: string; benchmark_isin?: string; actual_value: number; benchmark_value: number; difference: number; comparison: { date: string; actual: number; benchmark: number }[]; /** false when the benchmark has too few price rows; `have` of `need` rows are loaded. */ available?: boolean; reason?: string; have?: number; need?: number; /** First day both lines share; the benchmark starts with the same value. */ start_date?: string }>(`/analysis/benchmark-comparison${isin ? `?isin=${isin}` : ''}`),
  /** Cash and securities (property, vehicles and their loans excluded) against the German VPI. Returns are time-weighted over window_start → window_end. */
  getInflation: () => request<{ history: { date: string; nominal: number; real: number; cpi_index?: number }[]; nominal_return?: number; real_return?: number; inflation_pct?: number; purchasing_power_lost?: number; window_start?: string; window_end?: string }>('/analysis/inflation'),
  getCashFlow: () => request<{ history: { month: string; income: number; expenses: number; net: number }[]; projection: { month: string; income: number; expenses: number; net: number }[]; avg_income: number; avg_expenses: number; avg_net: number }>('/analysis/cash-flow'),

  // Settings
  listAllAccounts: () => request<{ accounts: Account[] }>('/settings/accounts'),
  getImportHistory: (limit = 50, offset = 0) =>
    request<{ history: ImportHistoryEntry[]; total?: number }>(`/import-history?limit=${limit}&offset=${offset}`),

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
  createGoal: (data: { name: string; target_amount: number; target_date: string; monthly_contribution?: number; assumed_return_pct?: number; funding_account_id?: string; priority?: number }) =>
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
  /** Generates the report for a period, or regenerates it when it exists (regenerated: true). */
  generateReport: (data: { report_type: string; year: number; month?: number }) =>
    request<{ status: string; period: string; regenerated?: boolean }>('/settings/reports', { method: 'POST', body: JSON.stringify(data) }),
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
