import { test, expect } from '@playwright/test';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const checkingCSV = path.resolve(__dirname, '../../internal/parser/testdata/revolut_checking.csv');
const savingsCSV = path.resolve(__dirname, '../../internal/parser/testdata/revolut_savings.csv');

test.describe('Revolut CSV import', () => {
  // Track created accounts so we always clean up, even on test failure.
  const cleanup: string[] = [];

  // The repo's e2e workflow normally runs with auth disabled. When auth IS
  // enabled, fall back to the legacy single-password login per test so the
  // suite stays runnable in either configuration. Playwright's request
  // fixture is per-test, so each test needs its own session cookie.
  test.beforeEach(async ({ request }) => {
    const status = await request.get('/api/auth/status').catch(() => null);
    if (!status?.ok()) return;
    const body = await status.json();
    if (!body.required || body.authenticated) return;
    const password = process.env.E2E_ADMIN_PASSWORD ?? 'localdev123456';
    await request.post('/api/auth/login', { data: { password } });
  });

  test.afterEach(async ({ request }) => {
    while (cleanup.length > 0) {
      const id = cleanup.pop()!;
      await request.delete(`/api/settings/accounts/${id}`).catch(() => {});
    }
  });

  test('imports a Revolut current-account CSV, skips PENDING rows, classifies types', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E Revolut Checking', institution: 'revolut', type: 'checking', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(checkingCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'revolut_checking.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.ok()).toBeTruthy();
    const body = await importResp.json();

    expect(body.institution).toBe('revolut');
    expect(body.account_type).toBe('checking');
    // 11 rows in fixture, 2 PENDING skipped → 9 imported
    expect(body.imported).toBe(9);
    expect(body.skipped).toBe(0);
    // PENDING skips produce per-row warnings
    expect(Array.isArray(body.errors)).toBeTruthy();
    const pendingWarnings = body.errors.filter((e: string) => e.includes('PENDING'));
    expect(pendingWarnings.length).toBe(2);

    // Verify a sample of transactions made it into the DB. The list endpoint
    // doesn't filter by account, so pull a large window and narrow client-side.
    const txResp = await request.get(`/api/transactions?limit=500`);
    expect(txResp.ok()).toBeTruthy();
    const all = await txResp.json();
    const transactions = (all.transactions as { account_id: string }[])
      .filter(t => t.account_id === account.id);
    expect(transactions.length).toBe(9);

    const types = transactions.map((t: { type: string }) => t.type);
    expect(types).toContain('deposit');     // TOPUP, CARD_REFUND
    expect(types).toContain('withdrawal');  // CARD_PAYMENT, ATM, TRANSFER, EXCHANGE
    expect(types).toContain('fee');         // FEE row

    // The ATM row carries a non-zero fee — make sure it round-tripped.
    const atm = transactions.find((t: { counterparty: string }) =>
      (t.counterparty ?? '').includes('Sparkasse ATM'));
    expect(atm).toBeDefined();
    expect(Number(atm.amount)).toBeCloseTo(100, 2);
    expect(Number(atm.fee)).toBeCloseTo(1.99, 2);

    // Re-importing the same file is a no-op (dedup via import_hash).
    const dupResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'revolut_checking.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(dupResp.ok()).toBeTruthy();
    const dup = await dupResp.json();
    expect(dup.imported).toBe(0);
  });

  test('imports a Revolut savings-vault CSV with US-format dates and € amounts', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E Revolut Savings', institution: 'revolut', type: 'savings', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(savingsCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'revolut_savings.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.ok()).toBeTruthy();
    const body = await importResp.json();

    expect(body.institution).toBe('revolut');
    expect(body.account_type).toBe('savings');
    // Fixture has 8 data rows (deposits, withdrawals, interest payments).
    expect(body.imported).toBe(8);

    const txResp = await request.get(`/api/transactions?limit=500`);
    expect(txResp.ok()).toBeTruthy();
    const all = await txResp.json();
    const transactions = (all.transactions as { account_id: string }[])
      .filter(t => t.account_id === account.id);
    expect(transactions.length).toBe(8);

    const types = transactions.map((t: { type: string }) => t.type);
    expect(types.filter((t: string) => t === 'deposit').length).toBe(4);
    expect(types.filter((t: string) => t === 'withdrawal').length).toBe(2);
    expect(types.filter((t: string) => t === 'interest').length).toBe(2);

    // Interest row: tiny amount, EUR currency, classification from description.
    const interestTxns = transactions.filter((t: { type: string }) => t.type === 'interest');
    for (const it of interestTxns) {
      expect(it.currency).toBe('EUR');
      expect(Number(it.amount)).toBeGreaterThan(0);
      expect(Number(it.amount)).toBeLessThan(1);
    }

    // The €2,000.00 deposit must round-trip without losing the thousands separator.
    const bigDeposit = transactions.find((t: { amount: number | string }) =>
      Math.abs(Number(t.amount) - 2000) < 0.001);
    expect(bigDeposit).toBeDefined();
  });

  test('rejects a checking CSV imported into a savings account', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E Revolut Wrong Type', institution: 'revolut', type: 'savings', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(checkingCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'revolut_checking.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.status()).toBe(400);
    const body = await importResp.json();
    expect(JSON.stringify(body).toLowerCase()).toContain('checking');
  });

  test('Revolut appears in the Settings institution dropdown', async ({ page }) => {
    // The page fixture has its own cookie jar that isn't shared with the
    // beforeEach `request` login — log in via the page context's request so
    // navigation carries the session cookie.
    const status = await page.request.get('/api/auth/status').catch(() => null);
    if (status?.ok()) {
      const body = await status.json();
      if (body.required && !body.authenticated) {
        const password = process.env.E2E_ADMIN_PASSWORD ?? 'localdev123456';
        await page.request.post('/api/auth/login', { data: { password } });
      }
    }

    await page.goto('/settings');
    // The "Add Account" form lives inside a collapsed details section — expand it first.
    const section = page.getByText('Bank Accounts & Securities');
    await section.scrollIntoViewIfNeeded();
    await section.click();
    await expect(page.getByRole('heading', { name: 'Add Account' })).toBeVisible();

    const institutionSelect = page.locator('select').filter({
      has: page.locator('option[value="revolut"]'),
    });
    await expect(institutionSelect).toHaveCount(1);
    await expect(institutionSelect.locator('option[value="revolut"]')).toHaveText('Revolut');
  });
});
