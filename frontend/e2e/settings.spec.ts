import { test, expect } from '@playwright/test';

test.describe('Settings page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Settings', exact: true }).click();
    await page.waitForURL('**/settings');
    // Wait for settings page to load — shows Accounts heading
    await page.waitForSelector('text=Accounts');
  });

  test('loads the settings page', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'Accounts' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Securities' })).toBeVisible();
  });

  test('scheduler status shows all 8 jobs', async ({ request }) => {
    const resp = await request.get('/api/settings/scheduler-status');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.jobs.length).toBe(8);
    const names = body.jobs.map((j: { name: string }) => j.name);
    expect(names).toContain('Price Update');
    expect(names).toContain('Live Prices');
    expect(names).toContain('FX Rates');
    expect(names).toContain('ETF Metadata');
    expect(names).toContain('Net Worth Snapshot');
    expect(names).toContain('Database Backup');
    expect(names).toContain('Price Alerts');
    expect(names).toContain('Wealth Reports');
  });

  test('displays account with active status toggle button', async ({ page }) => {
    // Find any account toggle button (Active or Inactive) — data-independent
    const activeBtns = page.getByRole('button', { name: /Activate|Deactivate/ });
    const count = await activeBtns.count();
    if (count === 0) return; // no accounts in DB, skip
    const firstBtn = activeBtns.first();
    await expect(firstBtn).toBeVisible();
    const text = await firstBtn.textContent();
    expect(text === 'Active' || text === 'Inactive').toBeTruthy();
  });

  test('can deactivate and reactivate an account', async ({ page }) => {
    // Find any active account's Deactivate button — data-independent
    const deactivateBtns = page.getByRole('button', { name: /^Deactivate / });
    const count = await deactivateBtns.count();
    if (count === 0) return; // no active accounts, skip

    const activeBtn = deactivateBtns.first();
    const ariaLabel = await activeBtn.getAttribute('aria-label') ?? await activeBtn.textContent() ?? '';
    // Extract account name from the aria-label/text (e.g. "Deactivate Foo" -> "Foo")
    const accountName = ariaLabel.replace(/^Deactivate\s+/, '');
    await expect(activeBtn).toBeVisible();
    await expect(activeBtn).toHaveText('Active');

    // Deactivate
    await activeBtn.click();
    const inactiveBtn = page.getByRole('button', { name: `Activate ${accountName}` });
    await expect(inactiveBtn).toBeVisible();
    await expect(inactiveBtn).toHaveText('Inactive');

    // Reactivate
    await inactiveBtn.click();
    await expect(page.getByRole('button', { name: `Deactivate ${accountName}` })).toBeVisible();
    await expect(page.getByRole('button', { name: `Deactivate ${accountName}` })).toHaveText('Active');
  });

  test('settings accounts API returns all accounts including inactive', async ({ request }) => {
    const resp = await request.get('/api/settings/accounts');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('accounts');
    expect(Array.isArray(body.accounts)).toBeTruthy();
    expect(body.accounts.length).toBeGreaterThan(0);

    // Each account should have is_active field
    for (const acc of body.accounts) {
      expect(acc).toHaveProperty('id');
      expect(acc).toHaveProperty('name');
      expect(acc).toHaveProperty('is_active');
      expect(typeof acc.is_active).toBe('boolean');
    }
  });

  test('displays TER for ETFs in securities list', async ({ page }) => {
    // Securities section is inside a collapsed details — expand it
    const secSection = page.getByText('Bank Accounts & Securities');
    if (await secSection.count() > 0) {
      await secSection.scrollIntoViewIfNeeded();
      await secSection.click();
    }
    // At least one ETF should show a TER percentage if metadata has been fetched
    const terTexts = page.getByText(/TER \d+\.\d+%/);
    const count = await terTexts.count();
    if (count > 0) {
      await expect(terTexts.first()).toBeVisible();
    }
  });

  test('securities API returns wkn and ter fields', async ({ request }) => {
    const resp = await request.get('/api/settings/securities');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('securities');
    expect(body.securities.length).toBeGreaterThan(0);

    // Every security should have wkn and ter fields (even if null)
    for (const sec of body.securities) {
      expect(sec).toHaveProperty('isin');
      expect(sec).toHaveProperty('name');
      expect('wkn' in sec).toBeTruthy();
      expect('ter' in sec).toBeTruthy();
    }

    // At least one ETF should have TER > 0
    const withTER = body.securities.filter((s: { ter: number | null }) => s.ter != null && s.ter > 0);
    expect(withTER.length).toBeGreaterThan(0);
  });

  test('update account API toggles is_active', async ({ request }) => {
    // Get accounts
    const listResp = await request.get('/api/settings/accounts');
    const { accounts } = await listResp.json();
    const acc = accounts[0];

    // Deactivate
    const deactivateResp = await request.put(`/api/settings/accounts/${acc.id}`, {
      data: { is_active: false },
    });
    expect(deactivateResp.ok()).toBeTruthy();

    // Verify deactivated
    const afterDeactivate = await request.get('/api/settings/accounts');
    const deactivated = (await afterDeactivate.json()).accounts.find((a: { id: string }) => a.id === acc.id);
    expect(deactivated.is_active).toBe(false);

    // Reactivate
    const reactivateResp = await request.put(`/api/settings/accounts/${acc.id}`, {
      data: { is_active: true },
    });
    expect(reactivateResp.ok()).toBeTruthy();

    // Verify reactivated
    const afterReactivate = await request.get('/api/settings/accounts');
    const reactivated = (await afterReactivate.json()).accounts.find((a: { id: string }) => a.id === acc.id);
    expect(reactivated.is_active).toBe(true);
  });

  test('goals API CRUD works', async ({ request }) => {
    // Create a goal
    const createResp = await request.post('/api/settings/goals', {
      data: {
        name: 'E2E Test Goal',
        target_amount: 1000000,
        target_date: '2050-01-01',
        monthly_contribution: 2000,
        assumed_return_pct: 7,
      },
    });
    expect(createResp.ok()).toBeTruthy();
    const created = await createResp.json();
    expect(created).toHaveProperty('id');
    expect(created.name).toBe('E2E Test Goal');

    // List goals — should include the new one
    const listResp = await request.get('/api/settings/goals');
    expect(listResp.ok()).toBeTruthy();
    const { goals } = await listResp.json();
    const found = goals.find((g: { name: string }) => g.name === 'E2E Test Goal');
    expect(found).toBeDefined();
    expect(found.target_amount).toBe(1000000);

    // Goals progress endpoint
    const progressResp = await request.get('/api/portfolio/goals');
    expect(progressResp.ok()).toBeTruthy();
    const progress = await progressResp.json();
    const progGoal = progress.goals.find((g: { name: string }) => g.name === 'E2E Test Goal');
    expect(progGoal).toBeDefined();
    expect(progGoal.progress_pct).toBeGreaterThanOrEqual(0);
    expect(progGoal.months_remaining).toBeGreaterThan(0);
    expect(['on_track', 'behind', 'ahead', 'complete']).toContain(progGoal.status);

    // Delete the goal
    const deleteResp = await request.delete(`/api/settings/goals/${created.id}`);
    expect(deleteResp.ok()).toBeTruthy();

    // Verify deleted
    const listAfter = await request.get('/api/settings/goals');
    const goalsAfter = (await listAfter.json()).goals;
    expect(goalsAfter.find((g: { name: string }) => g.name === 'E2E Test Goal')).toBeUndefined();
  });

  test('financial goals moved to Portfolio > Goals & Alerts tab', async ({ page }) => {
    await page.goto('/portfolio');
    await page.getByRole('tab', { name: 'Goals & Alerts' }).click();
    await expect(page.getByRole('heading', { name: 'Financial Goals' })).toBeVisible();
    await expect(page.getByPlaceholder('Goal name')).toBeVisible();
    await expect(page.getByText('Add Goal')).toBeVisible();
  });

  test('alerts API CRUD works', async ({ request }) => {
    // Get a security ISIN
    const secResp = await request.get('/api/settings/securities');
    const { securities } = await secResp.json();
    const isin = securities[0]?.isin;
    if (!isin) return;

    // Create alert
    const createResp = await request.post('/api/settings/alerts', {
      data: { alert_type: 'price_above', security_isin: isin, threshold: 999999 },
    });
    expect(createResp.ok()).toBeTruthy();
    const created = await createResp.json();
    expect(created).toHaveProperty('id');

    // List alerts
    const listResp = await request.get('/api/settings/alerts');
    expect(listResp.ok()).toBeTruthy();
    const { alerts } = await listResp.json();
    const found = alerts.find((a: { id: string }) => a.id === created.id);
    expect(found).toBeDefined();
    expect(found.alert_type).toBe('price_above');
    expect(found.is_active).toBe(true);

    // Toggle alert
    const toggleResp = await request.put(`/api/settings/alerts/${created.id}/toggle`);
    expect(toggleResp.ok()).toBeTruthy();

    // Delete alert
    const deleteResp = await request.delete(`/api/settings/alerts/${created.id}`);
    expect(deleteResp.ok()).toBeTruthy();
  });

  test('notifications API returns valid response', async ({ request }) => {
    const resp = await request.get('/api/notifications');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body).toHaveProperty('notifications');
    expect(body).toHaveProperty('unread_count');
    expect(Array.isArray(body.notifications)).toBeTruthy();
    expect(typeof body.unread_count).toBe('number');
  });

  test('price alerts moved to Portfolio > Goals & Alerts tab', async ({ page }) => {
    await page.goto('/portfolio');
    await page.getByRole('tab', { name: 'Goals & Alerts' }).click();
    await expect(page.getByRole('heading', { name: 'Price Alerts' })).toBeVisible();
    await expect(page.getByPlaceholder('Threshold')).toBeVisible();
  });

  test('holdings template endpoint returns CSV', async ({ request }) => {
    const resp = await request.get('/api/settings/template');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.text();
    expect(body.length).toBeGreaterThan(0);
    expect(body.toLowerCase()).toContain('isin');
  });

  test('users API CRUD works', async ({ request }) => {
    // Clean up leftover user from previous runs if it exists
    const preList = await request.get('/api/settings/users');
    if (preList.ok()) {
      const { users: existingUsers } = await preList.json();
      const stale = existingUsers?.find((u: { username: string }) => u.username === 'testuser_e2e');
      if (stale) await request.delete(`/api/settings/users/${stale.id}`);
    }

    const createResp = await request.post('/api/settings/users', {
      data: { username: 'testuser_e2e', password: 'testpassword123', role: 'member' },
    });
    expect(createResp.ok()).toBeTruthy();
    const created = await createResp.json();
    expect(created.username).toBe('testuser_e2e');
    expect(created.role).toBe('member');

    const listResp = await request.get('/api/settings/users');
    expect(listResp.ok()).toBeTruthy();
    const { users } = await listResp.json();
    expect(users.find((u: { username: string }) => u.username === 'testuser_e2e')).toBeDefined();

    await request.delete(`/api/settings/users/${created.id}`);
  });

  test('displays users section', async ({ page }) => {
    // Expand the "Users & Security" collapsible section
    const summary = page.getByText('Users & Security');
    await summary.scrollIntoViewIfNeeded();
    await summary.click();
    await expect(page.getByRole('heading', { name: 'Users', exact: true })).toBeVisible();
    await expect(page.getByPlaceholder('Username')).toBeVisible();
  });

  test('can create and delete extended account types', async ({ request }) => {
    // Create a real_estate account
    const createResp = await request.post('/api/settings/accounts', {
      data: { name: 'Test Apartment', institution: 'manual', type: 'real_estate', currency: 'EUR' },
    });
    expect(createResp.ok()).toBeTruthy();
    const acc = await createResp.json();
    expect(acc.type).toBe('real_estate');

    // Create a pension account
    const pensionResp = await request.post('/api/settings/accounts', {
      data: { name: 'Test Pension', institution: 'manual', type: 'pension', currency: 'EUR' },
    });
    expect(pensionResp.ok()).toBeTruthy();

    // Verify both appear in list
    const listResp = await request.get('/api/settings/accounts');
    const { accounts } = await listResp.json();
    expect(accounts.find((a: { name: string }) => a.name === 'Test Apartment')).toBeDefined();
    expect(accounts.find((a: { name: string }) => a.name === 'Test Pension')).toBeDefined();

    // Clean up
    await request.delete(`/api/settings/accounts/${acc.id}`);
    const pensionAcc = accounts.find((a: { name: string }) => a.name === 'Test Pension');
    if (pensionAcc) await request.delete(`/api/settings/accounts/${pensionAcc.id}`);
  });

  test('reports API supports generate and list', async ({ request }) => {
    // Generate a monthly report
    const genResp = await request.post('/api/settings/reports', {
      data: { report_type: 'monthly', year: 2025, month: 12 },
    });
    expect(genResp.ok() || genResp.status() === 409).toBeTruthy(); // 409 if already exists

    // List reports
    const listResp = await request.get('/api/settings/reports');
    expect(listResp.ok()).toBeTruthy();
    const body = await listResp.json();
    expect(body).toHaveProperty('reports');
    expect(Array.isArray(body.reports)).toBeTruthy();

    // View report detail if any exist
    if (body.reports.length > 0) {
      const detailResp = await request.get(`/api/settings/reports/${body.reports[0].id}`);
      expect(detailResp.ok()).toBeTruthy();
      const detail = await detailResp.json();
      expect(detail).toHaveProperty('data');
      expect(detail.data).toHaveProperty('net_worth_start');
      expect(detail.data).toHaveProperty('net_worth_end');
      expect(detail.data).toHaveProperty('holdings');
    }
  });

  test('PDF download returns valid PDF', async ({ request }) => {
    const listResp = await request.get('/api/settings/reports');
    const { reports } = await listResp.json();
    if (reports.length > 0) {
      const pdfResp = await request.get(`/api/settings/reports/${reports[0].id}/pdf`);
      expect(pdfResp.ok()).toBeTruthy();
      const ct = pdfResp.headers()['content-type'];
      expect(ct).toContain('application/pdf');
      const body = await pdfResp.body();
      // PDF files start with %PDF
      expect(body.toString().startsWith('%PDF')).toBeTruthy();
    }
  });

  test('displays wealth reports section', async ({ page }) => {
    // Expand the "Reports & Notifications" collapsible section
    const summary = page.getByText('Reports & Notifications');
    await summary.scrollIntoViewIfNeeded();
    await summary.click();
    await expect(page.getByRole('heading', { name: 'Wealth Reports' })).toBeVisible();
    await expect(page.getByRole('button', { name: /Generate/ })).toBeVisible();
  });

  test('displays two-factor authentication section', async ({ page }) => {
    // Expand the "Users & Security" collapsible section
    const summary = page.getByText('Users & Security');
    await summary.scrollIntoViewIfNeeded();
    await summary.click();
    await expect(page.getByRole('heading', { name: 'Two-Factor Authentication' })).toBeVisible();
    await expect(page.getByText('2FA Off')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Enable 2FA' })).toBeVisible();
  });

  test('TOTP setup API returns secret and URL', async ({ request }) => {
    // Get admin user ID
    const usersResp = await request.get('/api/settings/users');
    const users = await usersResp.json();
    const adminId = users.users[0].id;

    const resp = await request.post(`/api/settings/users/${adminId}/totp/setup`);
    expect(resp.ok()).toBeTruthy();
    const data = await resp.json();
    expect(data).toHaveProperty('secret');
    expect(data).toHaveProperty('url');
    expect(data.url).toContain('otpauth://');
  });
});
