import { test, expect } from '@playwright/test';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const deltaCSV = path.resolve(__dirname, '../../internal/parser/testdata/delta.csv');

test.describe('Delta App CSV import', () => {
  const cleanup: string[] = [];

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

  test('imports Delta multi-asset CSV: crypto wallet deposits, stock + crypto buys, skips sync legs', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E Delta Portfolio', institution: 'delta', type: 'brokerage', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(deltaCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'delta.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.ok()).toBeTruthy();
    const body = await importResp.json();

    expect(body.institution).toBe('delta');
    expect(body.account_type).toBe('brokerage');
    // 5 rows: BTC DEPOSIT, EUR WITHDRAW (SYNC leg → skipped),
    // ASME.DE BUY, ETH BUY, ASME.DE SELL → 4 imported.
    expect(body.imported).toBe(4);

    // SYNC-BASE-HOLDINGS warning surfaces explicitly so users know the leg was dropped on purpose.
    const syncWarn = body.errors.filter((e: string) => e.toLowerCase().includes('sync'));
    expect(syncWarn.length).toBeGreaterThan(0);

    // Verify both crypto positions and the stock exist in the securities table
    // (might have been created earlier — they're global, not per-account).
    const secResp0 = await request.get('/api/settings/securities');
    expect(secResp0.ok()).toBeTruthy();
    const allSecs = (await secResp0.json()).securities as { isin: string; asset_class: string }[];
    const isins = new Set(allSecs.map(s => s.isin));
    expect(isins.has('CRYPTO:BTC')).toBeTruthy();
    expect(isins.has('CRYPTO:ETH')).toBeTruthy();
    expect(isins.has('ASME.DE')).toBeTruthy();

    const txResp = await request.get('/api/transactions?limit=500');
    expect(txResp.ok()).toBeTruthy();
    const all = await txResp.json();
    const transactions = (all.transactions as { account_id: string; security_isin: string; type: string; quantity: number | string; amount: number | string }[])
      .filter(t => t.account_id === account.id);
    expect(transactions.length).toBe(4);

    // BTC wallet deposit → transfer with quantity, zero cost basis.
    const btc = transactions.find(t => t.security_isin === 'CRYPTO:BTC');
    expect(btc).toBeDefined();
    expect(btc!.type).toBe('transfer');
    expect(Number(btc!.quantity)).toBeCloseTo(0.67420698, 8);
    expect(Number(btc!.amount)).toBe(0);

    // ASME.DE buy: 67 shares at 6769 EUR.
    const stockBuy = transactions.find(t => t.security_isin === 'ASME.DE' && t.type === 'buy');
    expect(stockBuy).toBeDefined();
    expect(Number(stockBuy!.quantity)).toBe(67);
    expect(Number(stockBuy!.amount)).toBe(6769);

    // Crypto is classified into the new asset_class on the securities table.
    const btcSec = allSecs.find(s => s.isin === 'CRYPTO:BTC');
    expect(btcSec).toBeDefined();
    expect(btcSec!.asset_class).toBe('crypto');
  });

  test('Delta appears in the Settings institution dropdown', async ({ page }) => {
    const status = await page.request.get('/api/auth/status').catch(() => null);
    if (status?.ok()) {
      const body = await status.json();
      if (body.required && !body.authenticated) {
        const password = process.env.E2E_ADMIN_PASSWORD ?? 'localdev123456';
        await page.request.post('/api/auth/login', { data: { password } });
      }
    }
    await page.goto('/settings');
    const section = page.getByText('Bank Accounts & Securities');
    await section.scrollIntoViewIfNeeded();
    await section.click();
    await expect(page.getByRole('heading', { name: 'Add Account' })).toBeVisible();

    const institutionSelect = page.locator('select').filter({
      has: page.locator('option[value="delta"]'),
    });
    await expect(institutionSelect).toHaveCount(1);
    await expect(institutionSelect.locator('option[value="delta"]')).toHaveText('Delta');
  });
});
