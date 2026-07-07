import { test, expect } from '@playwright/test';

test.describe('Analysis page', () => {
  test.beforeEach(async ({ page }) => {
    // Navigate via the SPA — go to root first, then click Analysis tab
    await page.goto('/');
    await page.getByRole('link', { name: 'Analysis', exact: true }).click();
    await page.waitForURL('**/analysis');
  });

  test('loads the analysis page', async ({ page }) => {
    // Tab bar should be visible (Risk, Costs, etc.)
    await expect(page.getByRole('tab', { name: 'Risk' })).toBeVisible();
  });

  test('displays sector allocation chart when data exists', async ({ page }) => {
    const sectorHeading = page.getByRole('heading', { name: 'Sector Allocation' });
    const count = await sectorHeading.count();
    if (count > 0) {
      await expect(sectorHeading.first()).toBeVisible();
    }
  });

  test('displays sector drift chart when history data exists', async ({ page }) => {
    const driftHeading = page.getByRole('heading', { name: 'Sector Drift Over Time' });
    const count = await driftHeading.count();
    if (count > 0) {
      await expect(driftHeading.first()).toBeVisible();
      await expect(page.getByText('How your sector exposure has shifted month by month')).toBeVisible();
    }
  });

  test('sector history API returns valid response', async ({ request }) => {
    const response = await request.get('/api/analysis/sector-history');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body).toHaveProperty('history');
    expect(body).toHaveProperty('sectors');
    expect(Array.isArray(body.history)).toBeTruthy();
    expect(Array.isArray(body.sectors)).toBeTruthy();

    // Each history entry should have date and sectors
    for (const entry of body.history) {
      expect(entry).toHaveProperty('date');
      expect(entry).toHaveProperty('sectors');
      expect(typeof entry.date).toBe('string');
      expect(typeof entry.sectors).toBe('object');
    }

    // Sectors list should match sectors found in history entries
    if (body.history.length > 0) {
      const allSectorsInHistory = new Set<string>();
      for (const entry of body.history) {
        for (const sector of Object.keys(entry.sectors)) {
          allSectorsInHistory.add(sector);
        }
      }
      for (const sector of body.sectors) {
        expect(allSectorsInHistory.has(sector)).toBeTruthy();
      }
    }
  });

  test('sector history percentages are reasonable', async ({ request }) => {
    const response = await request.get('/api/analysis/sector-history');
    const body = await response.json();

    for (const entry of body.history) {
      const total = Object.values(entry.sectors as Record<string, number>).reduce((a: number, b: number) => a + b, 0);
      // Sector percentages should sum to roughly 100 (within tolerance for rounding and partial coverage)
      if (total > 0) {
        expect(total).toBeLessThanOrEqual(101);
        expect(total).toBeGreaterThan(0);
      }
    }
  });

  test('risk API returns valid response', async ({ request }) => {
    const response = await request.get('/api/analysis/risk');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body).toHaveProperty('risk');
    expect(body.risk).toHaveProperty('annualized_volatility');
    expect(body.risk).toHaveProperty('sharpe_ratio');
    expect(body.risk).toHaveProperty('sortino_ratio');
    expect(body.risk).toHaveProperty('max_drawdown');
    expect(body.risk).toHaveProperty('value_at_risk_95');
    expect(body.risk).toHaveProperty('drawdown_series');
    expect(body.risk).toHaveProperty('current_drawdown');
    expect(body.risk).toHaveProperty('all_time_high');
    expect(body.risk).toHaveProperty('ath_date');
    // All numeric fields should be numbers
    expect(typeof body.risk.annualized_volatility).toBe('number');
    expect(typeof body.risk.sharpe_ratio).toBe('number');
    expect(typeof body.risk.max_drawdown).toBe('number');
    expect(typeof body.risk.value_at_risk_95).toBe('number');
    expect(Array.isArray(body.risk.drawdown_series)).toBeTruthy();
  });

  test('risk metrics have reasonable values when data exists', async ({ request }) => {
    const response = await request.get('/api/analysis/risk');
    const body = await response.json();
    if (body.risk.annualized_volatility > 0) {
      // Volatility should be between 0 and 100%
      expect(body.risk.annualized_volatility).toBeGreaterThan(0);
      expect(body.risk.annualized_volatility).toBeLessThan(100);
      // Max drawdown should be between 0 and 100%
      expect(body.risk.max_drawdown).toBeGreaterThanOrEqual(0);
      expect(body.risk.max_drawdown).toBeLessThanOrEqual(100);
      // ATH should be positive
      expect(body.risk.all_time_high).toBeGreaterThan(0);
      // Current drawdown should be non-negative
      expect(body.risk.current_drawdown).toBeGreaterThanOrEqual(0);
      // Drawdown series entries should all be <= 0
      for (const pt of body.risk.drawdown_series) {
        expect(pt.drawdown).toBeLessThanOrEqual(0);
        expect(pt).toHaveProperty('date');
      }
    }
  });

  test('currency API returns valid response', async ({ request }) => {
    const response = await request.get('/api/analysis/currency');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body).toHaveProperty('currencies');
    expect(Array.isArray(body.currencies)).toBeTruthy();
    for (const entry of body.currencies) {
      expect(entry).toHaveProperty('currency');
      expect(entry).toHaveProperty('value');
      expect(entry).toHaveProperty('pct');
      expect(typeof entry.currency).toBe('string');
      expect(typeof entry.value).toBe('number');
      expect(typeof entry.pct).toBe('number');
    }
  });

  test('currency exposure percentages sum to ~100', async ({ request }) => {
    const response = await request.get('/api/analysis/currency');
    const body = await response.json();
    if (body.currencies.length > 0) {
      const total = body.currencies.reduce((sum: number, c: { pct: number }) => sum + c.pct, 0);
      expect(total).toBeGreaterThan(95);
      expect(total).toBeLessThan(105);
    }
  });

  test('currency exposure includes USD for US-heavy portfolio', async ({ request }) => {
    const response = await request.get('/api/analysis/currency');
    const body = await response.json();
    if (body.currencies.length > 0) {
      const usd = body.currencies.find((c: { currency: string }) => c.currency === 'USD');
      // Portfolio with S&P 500 and VWCE should have significant USD exposure
      expect(usd).toBeDefined();
      if (usd) {
        expect(usd.pct).toBeGreaterThan(20);
      }
    }
  });

  test('displays currency exposure section when data exists', async ({ page }) => {
    const heading = page.getByRole('heading', { name: 'Currency Exposure' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
      await expect(page.getByText('Underlying currency exposure')).toBeVisible();
      // Should show USD in the bar list
      await expect(page.getByText('USD')).toBeVisible();
    }
  });

  test('displays risk analytics section when data exists', async ({ page }) => {
    const riskHeading = page.getByRole('heading', { name: 'Risk Analytics' });
    const count = await riskHeading.count();
    if (count > 0) {
      await expect(riskHeading.first()).toBeVisible();
      await expect(page.getByText('Volatility (ann.)')).toBeVisible();
      await expect(page.getByText('Sharpe Ratio')).toBeVisible();
      await expect(page.getByText('Max Drawdown')).toBeVisible();
      await expect(page.getByText('VaR (95%, 1-day)')).toBeVisible();
      await expect(page.getByText('Drawdown Over Time')).toBeVisible();
    }
  });

  test('tax API returns valid response', async ({ request }) => {
    const response = await request.get('/api/analysis/tax');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body).toHaveProperty('summary');
    expect(body).toHaveProperty('available_years');
    const s = body.summary;
    expect(s).toHaveProperty('year');
    expect(s).toHaveProperty('realized_gains');
    expect(s).toHaveProperty('realized_losses');
    expect(s).toHaveProperty('taxable_gain');
    expect(s).toHaveProperty('estimated_tax');
    expect(s).toHaveProperty('freistellung_used');
    expect(s).toHaveProperty('freistellung_remaining');
    expect(s).toHaveProperty('effective_rate');
    expect(typeof s.realized_gains).toBe('number');
    expect(typeof s.estimated_tax).toBe('number');
    expect(s.freistellung_used + s.freistellung_remaining).toBeCloseTo(1000, 0);
  });

  test('tax API supports year parameter', async ({ request }) => {
    const resp = await request.get('/api/analysis/tax?year=2024');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.summary.year).toBe(2024);
  });

  test('tax API includes vorabpauschale data', async ({ request }) => {
    const resp = await request.get('/api/analysis/tax?year=2025');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('vorabpauschale');
    if (body.vorabpauschale && body.vorabpauschale.length > 0) {
      const vp = body.vorabpauschale[0];
      expect(vp).toHaveProperty('isin');
      expect(vp).toHaveProperty('basiszins');
      expect(vp).toHaveProperty('vorabpauschale');
      expect(vp).toHaveProperty('tax_on_vp');
      expect(vp.basiszins).toBeGreaterThan(0);
    }
  });

  test('tax export endpoint returns CSV', async ({ request }) => {
    const resp = await request.get('/api/analysis/export-tax?year=2025');
    expect(resp.ok()).toBeTruthy();
    const ct = resp.headers()['content-type'];
    expect(ct).toContain('text/csv');
    const body = await resp.text();
    expect(body).toContain('Steuerreport');
    expect(body).toContain('Sparerpauschbetrag');
  });

  test('displays tax overview section when data exists', async ({ page }) => {
    const heading = page.getByRole('heading', { name: 'Tax Overview' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
      await expect(page.getByText('Realized Gains')).toBeVisible();
      await expect(page.getByText('Estimated Tax')).toBeVisible();
      await expect(page.getByText('Sparerpauschbetrag')).toBeVisible();
    }
  });

  test('costs API returns valid response', async ({ request }) => {
    const response = await request.get('/api/analysis/costs');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body).toHaveProperty('holdings');
    expect(body).toHaveProperty('weighted_ter');
    expect(body).toHaveProperty('annual_cost');
    expect(body).toHaveProperty('daily_cost');
    expect(body).toHaveProperty('projection');
    expect(typeof body.weighted_ter).toBe('number');
    expect(typeof body.annual_cost).toBe('number');
    expect(body.holdings.length).toBeGreaterThan(0);
  });

  test('risk API includes rolling metrics', async ({ request }) => {
    const response = await request.get('/api/analysis/risk');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    if (body.rolling) {
      // Should have at least one window
      const windows = Object.keys(body.rolling);
      expect(windows.length).toBeGreaterThan(0);
      // Each window should have data points with volatility and sharpe
      for (const w of windows) {
        expect(body.rolling[w].length).toBeGreaterThan(0);
        expect(body.rolling[w][0]).toHaveProperty('volatility');
        expect(body.rolling[w][0]).toHaveProperty('sharpe');
      }
    }
  });

  test('FX history API returns rate data', async ({ request }) => {
    const resp = await request.get('/api/analysis/fx-history');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('rates');
    expect(body.rates).toHaveProperty('USD');
    expect(body.rates.USD.length).toBeGreaterThan(10);
    expect(body.rates.USD[0]).toHaveProperty('date');
    expect(body.rates.USD[0]).toHaveProperty('rate');
  });

  test('correlation API returns valid matrix', async ({ request }) => {
    const response = await request.get('/api/analysis/correlation');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body).toHaveProperty('labels');
    expect(body).toHaveProperty('matrix');
    if (body.labels.length >= 2) {
      expect(body.matrix.length).toBe(body.labels.length);
      // Diagonal should be 1.0
      expect(body.matrix[0][0]).toBe(1);
      // Off-diagonal should be between -1 and 1
      expect(body.matrix[0][1]).toBeGreaterThanOrEqual(-1);
      expect(body.matrix[0][1]).toBeLessThanOrEqual(1);
    }
  });

  test('benchmark comparison API returns valid data', async ({ request }) => {
    const resp = await request.get('/api/analysis/benchmark-comparison');
    // May return 400 if insufficient benchmark price data
    if (!resp.ok()) return;
    const body = await resp.json();
    expect(body).toHaveProperty('actual_value');
    expect(body).toHaveProperty('benchmark_value');
    expect(body).toHaveProperty('difference');
  });

  test('inflation API returns real vs nominal data', async ({ request }) => {
    const resp = await request.get('/api/analysis/inflation');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('history');
    expect(body.history.length).toBeGreaterThan(0);
    expect(body.history[0]).toHaveProperty('nominal');
    expect(body.history[0]).toHaveProperty('real');
  });

  test('cash flow API returns valid data', async ({ request }) => {
    const resp = await request.get('/api/analysis/cash-flow');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('history');
    expect(body).toHaveProperty('projection');
    expect(body).toHaveProperty('avg_income');
    expect(body).toHaveProperty('avg_net');
    expect(body.history.length).toBeGreaterThan(0);
    expect(body.projection.length).toBe(12);
  });

  test('displays portfolio costs section when data exists', async ({ page }) => {
    const heading = page.getByRole('heading', { name: 'Portfolio Costs' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
      await expect(page.getByText('Weighted Avg TER')).toBeVisible();
      await expect(page.getByText('Annual Cost')).toBeVisible();
    }
  });

  test('tax-lots API returns FIFO lot inventory', async ({ request }) => {
    const resp = await request.get('/api/analysis/tax-lots');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('lots');
    expect(Array.isArray(data.lots)).toBeTruthy();
    if (data.lots.length > 0) {
      const lot = data.lots[0];
      expect(lot).toHaveProperty('isin');
      expect(lot).toHaveProperty('cost_basis');
      expect(lot).toHaveProperty('current_value');
      expect(lot).toHaveProperty('unrealized_pl');
      expect(lot).toHaveProperty('tax_if_sold');
      expect(lot).toHaveProperty('net_proceeds');
      expect(lot).toHaveProperty('effective_rate');
    }
  });

  test('displays FIFO Tax Lot Inventory section', async ({ page }) => {
    const heading = page.getByRole('heading', { name: 'FIFO Tax Lot Inventory' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
      await expect(page.getByText('Unrealized P&L')).toBeVisible();
    }
  });

  test('displays cumulative fee drag chart in Portfolio Costs', async ({ page }) => {
    const heading = page.getByText('Cumulative Fee Drag');
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
    }
  });

  test('health score API returns distinct subscore names', async ({ request }) => {
    const resp = await request.get('/api/analysis/health-score');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('subscores');
    const names = data.subscores.map((s: { name: string }) => s.name);
    const unique = new Set(names);
    expect(unique.size).toBe(names.length); // no duplicates
    expect(names).toContain('Income');
  });

  test('costs API returns benchmark with grade', async ({ request }) => {
    const resp = await request.get('/api/analysis/costs');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('benchmark');
    expect(data.benchmark).toHaveProperty('grade');
    expect(data.benchmark).toHaveProperty('your_ter');
    expect(data.benchmark).toHaveProperty('avg_ter');
    expect(data.benchmark).toHaveProperty('annual_saving');
    expect(['A+', 'A', 'B+', 'B', 'C', 'D']).toContain(data.benchmark.grade);
  });

  test('displays cost efficiency benchmark card', async ({ page }) => {
    const card = page.getByText('Cost Efficiency');
    const count = await card.count();
    if (count > 0) {
      await expect(card.first()).toBeVisible();
      await expect(page.getByText('avg German investor')).toBeVisible();
    }
  });

  test('sell simulator API returns tax breakdown', async ({ request }) => {
    // Get a valid ISIN from holdings
    const holdingsResp = await request.get('/api/portfolio/holdings');
    const holdings = await holdingsResp.json();
    const isin = holdings.holdings?.[0]?.security_isin || 'IE00BK5BQT80';

    const resp = await request.post('/api/analysis/sell-simulator', {
      data: [{ isin, amount_eur: 5000 }],
    });
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('results');
    expect(data).toHaveProperty('total_tax');
    expect(data).toHaveProperty('total_proceeds');
    if (data.results.length > 0) {
      const r = data.results[0];
      expect(r).toHaveProperty('sell_amount');
      expect(r).toHaveProperty('realized_gain');
      expect(r).toHaveProperty('estimated_tax');
      expect(r).toHaveProperty('net_proceeds');
    }
  });

  test('displays sell simulator section', async ({ page }) => {
    const heading = page.getByRole('heading', { name: 'What-If Sell Simulator' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
      await expect(page.getByText('Preview Tax Impact')).toBeVisible();
    }
  });
});
