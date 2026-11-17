import { test, expect, type Page } from '@playwright/test';

test.describe('Settings page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Settings', exact: true }).click();
    await page.waitForURL('**/settings');
    // Wait for settings page to load — the section tabs are always rendered
    await expect(page.getByRole('tablist', { name: 'Settings sections' })).toBeVisible();
  });

  /** Settings is split into tabs; each keeps its state in the URL (?tab=…). */
  async function openTab(page: Page, name: 'Data' | 'Accounts' | 'Security' | 'Notifications') {
    const tab = page.getByRole('tab', { name, exact: true });
    await tab.click();
    await expect(tab).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('tabpanel', { name })).toBeVisible();
  }

  test('loads the settings page', async ({ page }) => {
    await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible();
    // Four tabs, Data first and selected by default
    await expect(page.getByRole('tab')).toHaveText(['Data', 'Accounts', 'Security', 'Notifications']);
    await expect(page.getByRole('tab', { name: 'Data' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('heading', { name: 'Scheduled Jobs' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Data Export' })).toBeVisible();

    // Accounts and Securities live on the Accounts tab
    await openTab(page, 'Accounts');
    await expect(page.getByRole('heading', { name: 'Accounts', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Securities' })).toBeVisible();
  });

  test('tab choice is kept in the URL and survives a reload', async ({ page }) => {
    await openTab(page, 'Accounts');
    await expect(page).toHaveURL(/\/settings\?tab=accounts$/);
    await page.reload();
    await expect(page.getByRole('tab', { name: 'Accounts' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('heading', { name: 'Securities' })).toBeVisible();
    // Deep links open the right tab directly
    await page.goto('/settings?tab=notifications');
    await expect(page.getByRole('tab', { name: 'Notifications' })).toHaveAttribute('aria-selected', 'true');
  });

  test('scheduler status shows all 9 jobs', async ({ page, request }) => {
    const resp = await request.get('/api/settings/scheduler-status');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    // 9 since the House Price Index job was added with property valuation
    const expected = [
      'Price Update', 'Live Prices', 'FX Rates', 'ETF Metadata', 'Net Worth Snapshot',
      'Database Backup', 'Price Alerts', 'Wealth Reports', 'House Price Index',
    ];
    expect(body.jobs.length).toBe(expected.length);
    const names = body.jobs.map((j: { name: string }) => j.name);
    for (const name of expected) expect(names).toContain(name);

    // The Data tab lists every job
    const jobs = page.getByRole('tabpanel', { name: 'Data' }).getByRole('list').first();
    for (const name of expected) {
      await expect(jobs.getByText(name, { exact: true })).toBeVisible();
    }
  });

  test('displays account with active status toggle button', async ({ page }) => {
    await openTab(page, 'Accounts');
    // Every account row has an Activate/Deactivate button named after the account
    const toggleBtns = page.getByRole('button', { name: /^(Activate|Deactivate) / });
    await expect(toggleBtns.first()).toBeVisible();
    const firstBtn = toggleBtns.first();
    const label = (await firstBtn.textContent())?.trim();
    expect(label === 'Deactivate' || label === 'Activate').toBeTruthy();
    // The row's status chip agrees with the action on offer
    const accountName = (await firstBtn.getAttribute('aria-label') ?? '').replace(/^(Activate|Deactivate)\s+/, '');
    const row = page.getByRole('listitem').filter({
      has: page.getByRole('button', { name: `Edit ${accountName}`, exact: true }),
    });
    await expect(row.getByText(label === 'Deactivate' ? 'Active' : 'Inactive', { exact: true })).toBeVisible();
  });

  test('can deactivate and reactivate an account', async ({ page }) => {
    await openTab(page, 'Accounts');
    const deactivateBtns = page.getByRole('button', { name: /^Deactivate / });
    await expect(deactivateBtns.first()).toBeVisible();

    const activeBtn = deactivateBtns.first();
    const ariaLabel = await activeBtn.getAttribute('aria-label') ?? '';
    // Extract account name from the aria-label (e.g. "Deactivate Foo" -> "Foo")
    const accountName = ariaLabel.replace(/^Deactivate\s+/, '');
    expect(accountName).not.toBe('');
    const row = page.getByRole('listitem').filter({
      has: page.getByRole('button', { name: `Edit ${accountName}`, exact: true }),
    });
    await expect(row.getByText('Active', { exact: true })).toBeVisible();

    // Deactivate
    await activeBtn.click();
    const inactiveBtn = page.getByRole('button', { name: `Activate ${accountName}`, exact: true });
    await expect(inactiveBtn).toBeVisible();
    await expect(inactiveBtn).toHaveText('Activate');
    await expect(row.getByText('Inactive', { exact: true })).toBeVisible();

    // Reactivate
    await inactiveBtn.click();
    await expect(page.getByRole('button', { name: `Deactivate ${accountName}`, exact: true })).toBeVisible();
    await expect(row.getByText('Active', { exact: true })).toBeVisible();
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
    await openTab(page, 'Accounts');
    await expect(page.getByRole('heading', { name: 'Securities' })).toBeVisible();
    // At least one ETF shows its TER (the securities API test below requires one to exist)
    await expect(page.getByText(/TER \d+[.,]\d+\s*%/).first()).toBeVisible();
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
    await expect(page).toHaveURL(/\/portfolio\?tab=goals/);
    await expect(page.getByRole('heading', { name: 'Financial Goals' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Goal name' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Target amount' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add goal' })).toBeVisible();
    // Goals are no longer on the Settings page
    await page.goto('/settings');
    await expect(page.getByRole('tablist', { name: 'Settings sections' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Financial Goals' })).toHaveCount(0);
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
    await page.goto('/portfolio?tab=goals');
    await expect(page.getByRole('tab', { name: 'Goals & Alerts' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('heading', { name: 'Price Alerts' })).toBeVisible();
    await expect(page.getByRole('combobox', { name: 'Alert when' })).toBeVisible();
    // The threshold field is labelled "Price level" for price alerts
    await expect(page.getByRole('textbox', { name: 'Price level' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Add alert' })).toBeVisible();
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
    await openTab(page, 'Security');
    await expect(page).toHaveURL(/\/settings\?tab=security/);
    await expect(page.getByRole('heading', { name: 'Users', exact: true })).toBeVisible();
    // The add-user form opens from the "Add user" button
    await page.getByRole('button', { name: 'Add user' }).first().click();
    await expect(page.getByRole('heading', { name: 'Add User' })).toBeVisible();
    await expect(page.getByRole('textbox', { name: 'Username' })).toBeVisible();
    await expect(page.getByLabel('Password')).toBeVisible();
    await expect(page.getByRole('combobox', { name: 'Role' })).toBeVisible();
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
    // Wealth Reports live on the Notifications tab
    await openTab(page, 'Notifications');
    await expect(page).toHaveURL(/\/settings\?tab=notifications/);
    await expect(page.getByRole('heading', { name: 'Wealth Reports' })).toBeVisible();
    await expect(page.getByRole('button', { name: /Generate/ })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Notification Channels' })).toBeVisible();
  });

  test('displays two-factor authentication section', async ({ page }) => {
    const status = await (await page.request.get('/api/auth/status')).json();
    await openTab(page, 'Security');
    if (status.required) {
      // Sign-in is on: 2FA is managed per user
      await expect(page.getByRole('heading', { name: 'Two-Factor Authentication' })).toBeVisible();
      const anyUser = page.getByRole('button', { name: /^(Enable|Disable) two-factor authentication for / });
      const noUsers = page.getByText('Two-factor authentication becomes available once a user account exists.');
      await expect(anyUser.first().or(noUsers)).toBeVisible();
    } else {
      // Sign-in is off (the usual E2E setup): the redesign replaces the "2FA Off / Enable 2FA"
      // controls with an explanation, because 2FA cannot apply without a password sign-in.
      await expect(page.getByRole('heading', { name: 'Sign-in', exact: true })).toBeVisible();
      await expect(page.getByText('Sign-in is switched off')).toBeVisible();
      await expect(page.getByText(/passkeys and two-factor authentication don’t apply/)).toBeVisible();
      await expect(page.getByRole('heading', { name: 'Two-Factor Authentication' })).toHaveCount(0);
    }
  });

  test('TOTP setup API returns secret and URL', async ({ request }) => {
    // Use the first existing user (the admin when sign-in is on). An instance without
    // sign-in has no users, so create a throwaway one for the duration of the test.
    const usersResp = await request.get('/api/settings/users');
    expect(usersResp.ok()).toBeTruthy();
    const { users } = await usersResp.json();
    let userId: string | undefined = users?.[0]?.id;
    let tempId: string | undefined;
    if (!userId) {
      const createResp = await request.post('/api/settings/users', {
        data: { username: 'totpuser_e2e', password: 'testpassword123', role: 'member' },
      });
      expect(createResp.ok()).toBeTruthy();
      tempId = userId = (await createResp.json()).id;
    }

    try {
      const resp = await request.post(`/api/settings/users/${userId}/totp/setup`);
      expect(resp.ok()).toBeTruthy();
      const data = await resp.json();
      expect(data).toHaveProperty('secret');
      expect(data).toHaveProperty('url');
      expect(data.url).toContain('otpauth://');
    } finally {
      if (tempId) await request.delete(`/api/settings/users/${tempId}`);
    }
  });
});
