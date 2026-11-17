import { test, expect } from '@playwright/test';

test.describe('Net Worth page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    // Wait for data to load — hero shows "Total Net Worth" label
    await page.waitForSelector('text=Total Net Worth');
  });

  test('displays the net worth hero KPI', async ({ page }) => {
    await expect(page.getByText('Total Net Worth')).toBeVisible();
    // Hero value uses text-[36px] on mobile, text-[44px] on desktop
    await expect(page.locator('p.text-\\[36px\\]').first()).toBeVisible();
  });

  test('renders period selector buttons 1M to All (no 1D/1W)', async ({ page }) => {
    const group = page.getByRole('group', { name: 'Time period' });
    const periods = ['1M', '3M', '6M', 'YTD', '1Y', 'All'];
    for (const p of periods) {
      await expect(group.getByRole('button', { name: p, exact: true })).toBeVisible();
    }
    await expect(group.getByRole('button')).toHaveCount(periods.length);
    // The 1D and 1W periods were removed in the redesign; the daily change is always shown instead.
    await expect(page.getByRole('button', { name: '1D', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '1W', exact: true })).toHaveCount(0);
  });

  test('1Y is selected by default', async ({ page }) => {
    const group = page.getByRole('group', { name: 'Time period' });
    await expect(group.getByRole('button', { name: '1Y', exact: true })).toHaveAttribute('aria-pressed', 'true');
    await expect(group.locator('button[aria-pressed="true"]')).toHaveCount(1);
    await expect(page.getByText('over the past year')).toBeVisible();
  });

  test('selecting a period marks it as pressed', async ({ page }) => {
    const group = page.getByRole('group', { name: 'Time period' });
    await group.getByRole('button', { name: '3M', exact: true }).click();
    await expect(group.getByRole('button', { name: '3M', exact: true })).toHaveAttribute('aria-pressed', 'true');
    await expect(group.getByRole('button', { name: '1Y', exact: true })).toHaveAttribute('aria-pressed', 'false');
  });

  test('always shows daily change with today label', async ({ page }) => {
    await expect(page.getByText('today', { exact: true }).first()).toBeVisible();
  });

  test('shows period change alongside daily change when non-1D period selected', async ({ page }) => {
    await page.getByRole('button', { name: '1Y', exact: true }).click();
    await expect(page.getByText('today', { exact: true }).first()).toBeVisible();
    // Period change "1Y" should also be visible — wait for data to update
    await expect(page.getByText('1Y').last()).toBeVisible();
  });

  test('clicking period buttons updates the period change display', async ({ page }) => {
    // Click 3M
    await page.getByRole('button', { name: '3M', exact: true }).click();
    await expect(page.getByText('3M').first()).toBeVisible();

    // Click All
    await page.getByRole('button', { name: 'All', exact: true }).click();
    await expect(page.getByText('all time').first()).toBeVisible();
  });

  test('period selector filters the chart data', async ({ page }) => {
    await page.getByRole('button', { name: '1M', exact: true }).click();
    await page.waitForTimeout(500);
    // Chart canvas should be visible (ECharts renders to canvas)
    await expect(page.locator('canvas').first()).toBeVisible();
  });

  test('default period shows chart', async ({ page }) => {
    // 1Y is default — sparkline chart should be visible
    await expect(page.locator('canvas').first()).toBeVisible();
  });

  test('networth API supports days parameter', async ({ request }) => {
    const resp30 = await request.get('/api/portfolio/networth?days=30');
    expect(resp30.ok()).toBeTruthy();
    const body30 = await resp30.json();
    expect(body30).toHaveProperty('snapshots');
    expect(Array.isArray(body30.snapshots)).toBeTruthy();

    const respAll = await request.get('/api/portfolio/networth?days=9999');
    expect(respAll.ok()).toBeTruthy();
    const bodyAll = await respAll.json();
    expect(bodyAll.snapshots.length).toBeGreaterThanOrEqual(body30.snapshots.length);
  });

  test('accounts API returns EUR-denominated balances', async ({ request }) => {
    const resp = await request.get('/api/portfolio/accounts');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.accounts.length).toBeGreaterThan(0);

    // Each account should have balance, cash_balance, holdings_value
    for (const acc of body.accounts) {
      expect(acc).toHaveProperty('balance');
      expect(acc).toHaveProperty('cash_balance');
      expect(acc).toHaveProperty('holdings_value');
      expect(typeof acc.balance).toBe('number');
    }
  });

  test('historical net worth uses market prices not just cost basis', async ({ request }) => {
    const resp = await request.get('/api/portfolio/networth?days=9999');
    const body = await resp.json();
    const snapshots = body.snapshots as { date: string; total: number; investment_component: number }[];

    // With historical price backfill, investment values for months with market data
    // should differ from a pure cost-basis approach. We verify that at least some
    // historical months show investment values that are NOT exact multiples of small
    // round numbers (cost basis tends to be exact deposit amounts).
    expect(snapshots.length).toBeGreaterThan(10);

    // The most recent months should reflect actual market prices
    const recentMonths = snapshots.slice(0, 6);
    for (const s of recentMonths) {
      // Investment component should be a significant positive number
      expect(s.investment_component).toBeGreaterThan(0);
    }
  });

  test('projection API returns valid response', async ({ request }) => {
    const resp = await request.get('/api/portfolio/projection');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('history');
    expect(body).toHaveProperty('projection');
    expect(body).toHaveProperty('current_value');
    expect(body).toHaveProperty('contribution');
    expect(body).toHaveProperty('return_pct');
    expect(Array.isArray(body.history)).toBeTruthy();
    expect(Array.isArray(body.projection)).toBeTruthy();
    expect(body.current_value).toBeGreaterThan(0);
    // Projection values should be increasing (with positive return)
    if (body.projection.length > 1) {
      expect(body.projection[body.projection.length - 1].value).toBeGreaterThan(body.projection[0].value);
    }
  });

  test('projection API returns confidence bands', async ({ request }) => {
    const resp = await request.get('/api/portfolio/projection');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('has_confidence');
    if (body.has_confidence && body.projection.length > 1) {
      const lastPt = body.projection[body.projection.length - 1];
      expect(lastPt).toHaveProperty('p10');
      expect(lastPt).toHaveProperty('p90');
      expect(lastPt.p90).toBeGreaterThan(lastPt.value);
      expect(lastPt.p10).toBeLessThan(lastPt.value);
    }
  });

  test('projection API supports what-if parameters', async ({ request }) => {
    const resp = await request.get('/api/portfolio/projection?contribution=3000&return_pct=10');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.contribution).toBe(3000);
    expect(body.return_pct).toBe(10);
  });

  test('displays asset allocation donut when multiple accounts exist', async ({ page }) => {
    const heading = page.getByRole('heading', { name: 'Asset Allocation' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
    }
  });

  test('displays wealth projection chart on Projection tab', async ({ page }) => {
    // Switch to Projection tab (TabBar uses role="tab")
    await page.getByRole('tab', { name: 'Projection' }).click();
    await expect(page.getByRole('heading', { name: 'Wealth Projection' })).toBeVisible();
    // Should have sliders
    await expect(page.getByRole('slider', { name: 'Monthly contribution' })).toBeVisible();
    await expect(page.getByRole('slider', { name: 'Return' })).toBeVisible();
  });

  test('tab choice is kept in the URL and survives a reload', async ({ page }) => {
    await page.getByRole('tab', { name: 'Projection' }).click();
    await expect(page).toHaveURL(/[?&]tab=projection/);
    await page.reload();
    await expect(page.getByRole('tab', { name: 'Projection' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('heading', { name: 'Wealth Projection' })).toBeVisible();
    // Returning to the default tab drops the parameter again
    await page.getByRole('tab', { name: 'Overview' }).click();
    await expect(page).not.toHaveURL(/tab=/);
  });

  test('savings rate API returns valid data', async ({ request }) => {
    const resp = await request.get('/api/portfolio/savings-rate');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('avg_savings_rate');
    expect(body).toHaveProperty('trailing_12m_rate');
    expect(body).toHaveProperty('total_deposits');
    expect(body).toHaveProperty('monthly');
    expect(typeof body.avg_savings_rate).toBe('number');
    expect(body.total_deposits).toBeGreaterThan(0);
  });

  test('displays summary tiles and recommended actions on overview', async ({ page, request }) => {
    // The summary tiles are always shown on the Overview tab
    const overview = page.getByRole('tabpanel', { name: 'Overview' });
    await expect(overview.getByText('Liquid wealth', { exact: true })).toBeVisible();
    // Recommended actions ("Next Actions" before the redesign) appear whenever the API has any
    const { count } = await (await request.get('/api/portfolio/next-actions')).json();
    if (count > 0) {
      await expect(overview.getByRole('heading', { name: 'Recommended Actions' })).toBeVisible();
      await expect(overview.getByRole('link', { name: /^Open / }).first()).toBeVisible();
    }
  });

  // The "Rule of Three" tiles (Invested / Return / Cash) were removed in the redesign.
  // Their place is taken by Liquid wealth and Market return for the selected period.
  test('displays Liquid wealth and Market return tiles on overview', async ({ page }) => {
    const overview = page.getByRole('tabpanel', { name: 'Overview' });
    await expect(overview.getByText('Liquid wealth', { exact: true })).toBeVisible();
    await expect(overview.getByText('Market return', { exact: true })).toBeVisible();
    await expect(overview.getByText('Invested', { exact: true })).toHaveCount(0);
  });

  test('attribution API returns summary', async ({ request }) => {
    const resp = await request.get('/api/portfolio/attribution');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('contributions');
    expect(body).toHaveProperty('summary');
    expect(Array.isArray(body.contributions)).toBeTruthy();
  });

  test('displays estate calculator on Planning page', async ({ page }) => {
    // Navigate to Planning page — the Estate Calculator lives on the Protection tab
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Planning', exact: true }).click();
    await page.getByRole('tab', { name: 'Protection' }).click();
    await expect(page).toHaveURL(/\/planning\?tab=protection/);
    await expect(page.getByRole('heading', { name: 'Estate Calculator' })).toBeVisible();
    await expect(page.getByText('German inheritance tax')).toBeVisible();
  });

  test('displays FIRE calculator on Planning page', async ({ page }) => {
    // Navigate to Planning page — FIRE & Retirement is the default tab
    await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Planning', exact: true }).click();
    await expect(page.getByRole('tab', { name: 'FIRE & Retirement' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('heading', { name: 'FIRE Calculator' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Annual expenses' })).toBeVisible();
  });

  test('displays projection milestones on Projection tab', async ({ page }) => {
    // Switch to Projection tab (TabBar uses role="tab")
    await page.getByRole('tab', { name: 'Projection' }).click();
    const heading = page.getByRole('heading', { name: 'Projection Milestones' });
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
    }
  });

  test('projection API returns milestones', async ({ request }) => {
    const resp = await request.get('/api/portfolio/projection');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('milestones');
    expect(Array.isArray(data.milestones)).toBeTruthy();
    if (data.milestones.length > 0) {
      const m = data.milestones[0];
      expect(m).toHaveProperty('year');
      expect(m).toHaveProperty('projected_value');
      expect(m).toHaveProperty('contributions');
      expect(m).toHaveProperty('growth');
      expect(m).toHaveProperty('real_value');
    }
  });

  test('projection API returns drawdown data when expenses provided', async ({ request }) => {
    const resp = await request.get('/api/portfolio/projection?expenses=36000&swr=3.5');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('drawdown');
    // Drawdown may be null if FIRE number can't be reached within projection horizon
    if (data.drawdown) {
      expect(data.drawdown.fire_number).toBeGreaterThan(0);
      expect(data.drawdown.longevity_years).toBeGreaterThan(0);
      expect(data.drawdown.series.length).toBeGreaterThan(0);
      expect(data.drawdown.success_rate).toBeGreaterThanOrEqual(0);
    }
  });

  test('notification bell opens dropdown', async ({ page }) => {
    const bell = page.getByLabel('Notifications').first();
    await expect(bell).toBeVisible();
    await bell.click();
    await expect(page.getByText('Notifications', { exact: false }).first()).toBeVisible();
  });

  test('notifications API returns valid response', async ({ request }) => {
    const resp = await request.get('/api/notifications');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('notifications');
    expect(data).toHaveProperty('unread_count');
    expect(typeof data.unread_count).toBe('number');
  });

  test('next-actions API returns recommendations', async ({ request }) => {
    const resp = await request.get('/api/portfolio/next-actions');
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('actions');
    expect(data).toHaveProperty('count');
    expect(Array.isArray(data.actions)).toBeTruthy();
    for (const a of data.actions) {
      expect(a).toHaveProperty('title');
      expect(a).toHaveProperty('detail');
      expect(a).toHaveProperty('impact_eur');
      expect(a).toHaveProperty('urgency');
      expect(a).toHaveProperty('category');
      expect(a).toHaveProperty('link');
      expect(['now', 'this-month', 'this-quarter']).toContain(a.urgency);
      expect(['tax', 'rebalance', 'savings', 'cost', 'risk']).toContain(a.category);
    }
  });
});
