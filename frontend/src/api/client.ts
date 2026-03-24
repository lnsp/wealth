const BASE = '/api';

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const resp = await fetch(`${BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...options,
  });
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: resp.statusText }));
    throw new Error(err.error || 'Request failed');
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
  balance?: number;
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

export interface HoldingRow {
  account_id: string;
  security_isin: string;
  quantity: number;
  avg_cost_basis: number;
  total_dividends: number;
  name: string;
  symbol: string | null;
  asset_class: string;
  currency: string;
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
}

export interface ETFHoldingEntry {
  isin: string;
  name: string;
  weight: number;
  sector?: string;
  country?: string;
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

export const api = {
  // Accounts
  listAccounts: () => request<{ accounts: Account[] }>('/portfolio/accounts'),
  createAccount: (data: { name: string; institution: string; type: string; currency?: string; iban?: string }) =>
    request<Account>('/settings/accounts', { method: 'POST', body: JSON.stringify(data) }),

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
  listTransactions: (limit = 50, offset = 0) =>
    request<{ transactions: TransactionRow[]; total: number }>(`/transactions?limit=${limit}&offset=${offset}`),

  // Portfolio
  listHoldings: () => request<{ holdings: HoldingRow[] }>('/portfolio/holdings'),
  getNetWorth: (days = 365) => request<{ snapshots: NetWorthSnapshot[] }>(`/portfolio/networth?days=${days}`),

  // Analysis
  getSectors: () => request<{ sectors: Record<string, number> }>('/analysis/sectors'),
  getCountries: () => request<{ countries: Record<string, number> }>('/analysis/countries'),
  getOverlap: () => request<{ labels: string[]; matrix: number[][] }>('/analysis/overlap'),
  getETFHoldings: (isin: string) =>
    request<{ etf_isin: string; etf_name: string; holdings: ETFHoldingEntry[] }>(`/analysis/etf/${isin}/holdings`),

  // Settings
  listSecurities: () => request<{ securities: Security[] }>('/settings/securities'),
  updateSecuritySymbol: (isin: string, symbol: string) =>
    request<{ status: string }>(`/settings/securities/${isin}/symbol`, {
      method: 'PUT',
      body: JSON.stringify({ symbol }),
    }),
};
