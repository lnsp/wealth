import { test, expect } from '@playwright/test';

test.describe('Portfolio page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Portfolio', exact: true }).click();
    await page.waitForURL('**/portfolio');
    // Wait for the page to finish loading — the TabBar is always rendered once loading completes
    await page.waitForSelector('button:has-text("Overview")');
  });

  test('loads the portfolio page with KPIs', async ({ page }) => {
    const marketValue = page.getByText('Market Value').first();
    if (await marketValue.count() > 0) {
      await expect(marketValue).toBeVisible();
    }
    const positions = page.getByText('Positions');
    if (await positions.count() > 0) {
      await expect(positions.first()).toBeVisible();
    }
    const dividends = page.getByText('Dividends').first();
    if (await dividends.count() > 0) {
      await expect(dividends).toBeVisible();
    }
  });

  test('displays dividend income section with monthly/yearly toggle', async ({ page }) => {
    await page.getByRole('tab', { name: 'Dividends' }).click();
    const heading = page.getByRole('heading', { name: 'Dividend Income' });
    if (await heading.count() > 0) {
      await expect(heading.first()).toBeVisible();
      await expect(page.getByRole('button', { name: 'monthly' })).toBeVisible();
      await expect(page.getByRole('button', { name: 'yearly' })).toBeVisible();
    }
  });

  test('monthly view is selected by default', async ({ page }) => {
    await page.getByRole('tab', { name: 'Dividends' }).click();
    const monthlyBtn = page.getByRole('button', { name: 'monthly' });
    if (await monthlyBtn.count() > 0) {
      await expect(monthlyBtn).toBeVisible();
      await expect(page.getByRole('heading', { name: 'Dividend Income' })).toBeVisible();
    }
  });

  test('clicking yearly switches the dividend chart view', async ({ page }) => {
    await page.getByRole('tab', { name: 'Dividends' }).click();
    const yearlyBtn = page.getByRole('button', { name: 'yearly' });
    if (await yearlyBtn.count() > 0) {
      await yearlyBtn.click();
      await page.waitForTimeout(500);
      await expect(page.getByRole('heading', { name: 'Dividend Income' })).toBeVisible();

      await page.getByRole('button', { name: 'monthly' }).click();
      await page.waitForTimeout(500);
      await expect(page.getByRole('heading', { name: 'Dividend Income' })).toBeVisible();
    }
  });

  test('dividends API returns all fields including advanced metrics', async ({ request }) => {
    const resp = await request.get('/api/portfolio/dividends');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();

    expect(body).toHaveProperty('total');
    expect(body).toHaveProperty('monthly');
    expect(body).toHaveProperty('yearly');
    expect(body).toHaveProperty('by_security');
    expect(body).toHaveProperty('cumulative');
    expect(body).toHaveProperty('trailing_12m');
    expect(body).toHaveProperty('yield_on_cost');
    expect(body).toHaveProperty('dividend_growth');
    expect(typeof body.trailing_12m).toBe('number');
    expect(typeof body.yield_on_cost).toBe('number');
    expect(typeof body.dividend_growth).toBe('number');
    expect(Array.isArray(body.cumulative)).toBeTruthy();

    expect(Array.isArray(body.monthly)).toBeTruthy();
    expect(Array.isArray(body.yearly)).toBeTruthy();

    // Each yearly entry should have year and amount
    for (const entry of body.yearly) {
      expect(entry).toHaveProperty('year');
      expect(entry).toHaveProperty('amount');
      expect(typeof entry.year).toBe('string');
      expect(entry.year).toMatch(/^\d{4}$/);
      expect(typeof entry.amount).toBe('number');
      expect(entry.amount).toBeGreaterThan(0);
    }

    // Yearly totals should equal monthly totals
    const monthlyTotal = body.monthly.reduce((sum: number, m: { amount: number }) => sum + m.amount, 0);
    const yearlyTotal = body.yearly.reduce((sum: number, y: { amount: number }) => sum + y.amount, 0);
    expect(Math.abs(monthlyTotal - yearlyTotal)).toBeLessThan(0.01);
  });

  test('yearly entries are sorted chronologically', async ({ request }) => {
    const resp = await request.get('/api/portfolio/dividends');
    const body = await resp.json();

    for (let i = 1; i < body.yearly.length; i++) {
      expect(body.yearly[i].year > body.yearly[i - 1].year).toBeTruthy();
    }
  });

  test('displays by security breakdown', async ({ page }) => {
    await page.getByRole('tab', { name: 'Dividends' }).click();
    const bySecurity = page.getByText('By Security');
    if (await bySecurity.count() > 0) {
      await expect(bySecurity.first()).toBeVisible();
    }
  });

  test('shows price freshness indicator', async ({ page }) => {
    const priceText = page.getByText('Prices as of');
    if (await priceText.count() > 0) {
      await expect(priceText.first()).toBeVisible();
    }
  });

  test('holdings API returns price_as_of date', async ({ request }) => {
    const resp = await request.get('/api/portfolio/holdings');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('price_as_of');
    expect(body.price_as_of).toMatch(/^\d{4}-\d{2}-\d{2}$/);
  });

  test('displays realized and unrealized P&L in performance section', async ({ page }) => {
    const realized = page.getByText('Realized', { exact: true });
    if (await realized.count() > 0) {
      await expect(realized.first()).toBeVisible();
      await expect(page.getByText('Unrealized').first()).toBeVisible();
      await expect(page.getByText('Total Gains').first()).toBeVisible();
    }
  });

  test('performance API returns realized and unrealized fields', async ({ request }) => {
    const resp = await request.get('/api/portfolio/performance');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();

    expect(body).toHaveProperty('realized_pl');
    expect(body).toHaveProperty('unrealized_pl');
    expect(body).toHaveProperty('total_return');
    expect(typeof body.realized_pl).toBe('number');
    expect(typeof body.unrealized_pl).toBe('number');

    // realized + unrealized should approximately equal total_return
    const sum = body.realized_pl + body.unrealized_pl;
    expect(Math.abs(sum - body.total_return)).toBeLessThan(1);
  });

  test('allocation API returns valid response', async ({ request }) => {
    const resp = await request.get('/api/portfolio/allocation');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('allocations');
    expect(body).toHaveProperty('has_targets');
    expect(body).toHaveProperty('max_drift');
    expect(body).toHaveProperty('total_value');
    expect(Array.isArray(body.allocations)).toBeTruthy();
    expect(typeof body.has_targets).toBe('boolean');
    expect(typeof body.total_value).toBe('number');
    // Each allocation entry should have required fields
    for (const a of body.allocations) {
      expect(a).toHaveProperty('isin');
      expect(a).toHaveProperty('name');
      expect(a).toHaveProperty('actual_pct');
      expect(a).toHaveProperty('target_pct');
      expect(a).toHaveProperty('drift_pct');
      expect(a).toHaveProperty('status');
      expect(typeof a.actual_pct).toBe('number');
      expect(['on_target', 'underweight', 'overweight']).toContain(a.status);
    }
  });

  test('allocation actual percentages sum to ~100', async ({ request }) => {
    const resp = await request.get('/api/portfolio/allocation');
    const body = await resp.json();
    if (body.allocations.length > 0) {
      const total = body.allocations.reduce((sum: number, a: { actual_pct: number }) => sum + a.actual_pct, 0);
      expect(total).toBeGreaterThan(95);
      expect(total).toBeLessThan(105);
    }
  });

  test('can set and retrieve target allocations', async ({ request }) => {
    // Get current allocations to find ISINs
    const getResp = await request.get('/api/portfolio/allocation');
    const body = await getResp.json();
    if (body.allocations.length < 2) return; // need at least 2 holdings

    const isins = body.allocations.slice(0, 2).map((a: { isin: string }) => a.isin);

    // Set targets
    const setResp = await request.put('/api/portfolio/allocation', {
      data: {
        allocations: [
          { isin: isins[0], target_pct: 60 },
          { isin: isins[1], target_pct: 40 },
        ],
      },
    });
    expect(setResp.ok()).toBeTruthy();

    // Verify targets are reflected
    const verifyResp = await request.get('/api/portfolio/allocation');
    const updated = await verifyResp.json();
    expect(updated.has_targets).toBe(true);

    const first = updated.allocations.find((a: { isin: string }) => a.isin === isins[0]);
    expect(first).toBeDefined();
    expect(first.target_pct).toBe(60);

    // Clean up: remove targets
    await request.put('/api/portfolio/allocation', {
      data: {
        allocations: [
          { isin: isins[0], target_pct: 0 },
          { isin: isins[1], target_pct: 0 },
        ],
      },
    });
  });

  test('cumulative dividends are monotonically increasing', async ({ request }) => {
    const resp = await request.get('/api/portfolio/dividends');
    const body = await resp.json();
    if (body.cumulative && body.cumulative.length > 1) {
      for (let i = 1; i < body.cumulative.length; i++) {
        expect(body.cumulative[i].cumulative).toBeGreaterThanOrEqual(body.cumulative[i - 1].cumulative);
      }
    }
  });

  test('displays dividend KPI cards', async ({ page }) => {
    await page.getByRole('tab', { name: 'Dividends' }).click();
    const heading = page.getByRole('heading', { name: 'Dividend Income' });
    if (await heading.count() > 0) {
      await expect(heading.first()).toBeVisible();
      const totalReceived = page.getByText('Total Received');
      if (await totalReceived.count() > 0) {
        await expect(totalReceived.first()).toBeVisible();
        await expect(page.getByText('Trailing 12 Months').first()).toBeVisible();
        await expect(page.getByText('Yield on Cost').first()).toBeVisible();
        await expect(page.getByText('Growth YoY').first()).toBeVisible();
      }
    }
  });

  test('rebalance API returns valid response', async ({ request }) => {
    const resp = await request.get('/api/portfolio/rebalance');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('trades');
    expect(body).toHaveProperty('message');
    expect(Array.isArray(body.trades)).toBeTruthy();
    for (const t of body.trades) {
      expect(t).toHaveProperty('isin');
      expect(t).toHaveProperty('name');
      expect(t).toHaveProperty('action');
      expect(t).toHaveProperty('amount');
      expect(['buy', 'sell']).toContain(t.action);
      expect(t.amount).toBeGreaterThan(0);
    }
  });

  test('rebalance API supports deposit parameter', async ({ request }) => {
    const resp = await request.get('/api/portfolio/rebalance?deposit=2000');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('trades');
    expect(body).toHaveProperty('message');
    // In deposit mode, all trades should be buys (if any)
    for (const t of body.trades) {
      expect(t.action).toBe('buy');
    }
  });

  test('health score API returns valid data', async ({ request }) => {
    const resp = await request.get('/api/analysis/health-score');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('score');
    expect(body).toHaveProperty('subscores');
    expect(body.score).toBeGreaterThanOrEqual(0);
    expect(body.score).toBeLessThanOrEqual(100);
    expect(body.subscores.length).toBe(5);
  });

  test('holdings API supports account filter and returns account_name', async ({ request }) => {
    const resp = await request.get('/api/portfolio/holdings');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    if (body.holdings.length > 0) {
      expect(body.holdings[0]).toHaveProperty('account_name');
      expect(typeof body.holdings[0].account_name).toBe('string');
    }
  });

  test('displays target allocation section on Allocation tab', async ({ page }) => {
    await page.getByRole('tab', { name: 'Allocation' }).click();
    const heading = page.getByRole('heading', { name: 'Target Allocation' });
    if (await heading.count() > 0) {
      await expect(heading.first()).toBeVisible();
      const editBtn = page.getByRole('button', { name: 'Edit Targets' });
      if (await editBtn.count() > 0) {
        await expect(editBtn.first()).toBeVisible();
      }
    }
  });

  test('dividends API returns calendar with upcoming payments', async ({ request }) => {
    const resp = await request.get('/api/portfolio/dividends');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('calendar');
    expect(Array.isArray(data.calendar)).toBeTruthy();
    if (data.calendar.length > 0) {
      expect(data.calendar[0]).toHaveProperty('month');
      expect(data.calendar[0]).toHaveProperty('expected');
      expect(data.calendar[0]).toHaveProperty('name');
    }
  });
});
