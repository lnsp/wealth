import { test, expect } from '@playwright/test';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const ordersCSV = path.resolve(__dirname, '../../internal/parser/testdata/ing_ordermanager.csv');
const holdingsCSV = path.resolve(__dirname, '../../internal/parser/testdata/ing_depotuebersicht.csv');

test.describe('ING CSV import', () => {
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

  test('imports an ING Ordermanager CSV: parses title rows, German decimals, skips Storniert', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E ING Orders', institution: 'ing', type: 'brokerage', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(ordersCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'ing_ordermanager.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.ok()).toBeTruthy();
    const body = await importResp.json();

    expect(body.institution).toBe('ing');
    expect(body.account_type).toBe('brokerage');
    // Fixture: 4 order rows — 3 Ausgeführt + 1 Storniert. Only the executed ones import.
    expect(body.imported).toBe(3);
    expect(Array.isArray(body.errors)).toBeTruthy();
    const storniertWarnings = body.errors.filter((e: string) => e.toLowerCase().includes('storniert'));
    expect(storniertWarnings.length).toBe(1);

    // Verify the parsed transactions made it in.
    const txResp = await request.get('/api/transactions?limit=500');
    expect(txResp.ok()).toBeTruthy();
    const all = await txResp.json();
    const transactions = (all.transactions as { account_id: string; type: string; security_isin: string; quantity: number | string; price: number | string }[])
      .filter(t => t.account_id === account.id);
    expect(transactions.length).toBe(3);

    const types = transactions.map(t => t.type);
    expect(types.filter(t => t === 'buy').length).toBe(2);   // 2 Kauf rows
    expect(types.filter(t => t === 'sell').length).toBe(1);  // 1 Verkauf

    // German-decimal quantity 6,76942 round-trips as 6.76942.
    const em = transactions.find(t => t.security_isin === 'IE00B4L5YC18');
    expect(em).toBeDefined();
    expect(Number(em!.quantity)).toBeCloseTo(6.76942, 5);
    expect(Number(em!.price)).toBeCloseTo(42.692, 3);

    // Re-importing the same file is a no-op.
    const dup = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'ing_ordermanager.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(dup.ok()).toBeTruthy();
    expect((await dup.json()).imported).toBe(0);
  });

  test('imports an ING Depotübersicht: synthesizes buys at cost basis, skips n.a. positions and footer', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E ING Holdings', institution: 'ing', type: 'brokerage', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(holdingsCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'ing_depotuebersicht.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.ok()).toBeTruthy();
    const body = await importResp.json();

    expect(body.institution).toBe('ing');
    expect(body.account_type).toBe('brokerage');
    // 3 positions in fixture; E.ON has n.a. cost basis → skipped; footer ignored.
    expect(body.imported).toBe(2);
    const naWarnings = body.errors.filter((e: string) => e.toLowerCase().includes('cost basis'));
    expect(naWarnings.length).toBe(1);

    const txResp = await request.get('/api/transactions?limit=500');
    expect(txResp.ok()).toBeTruthy();
    const all = await txResp.json();
    const transactions = (all.transactions as { account_id: string; security_isin: string; type: string; amount: number | string; quantity: number | string }[])
      .filter(t => t.account_id === account.id);
    expect(transactions.length).toBe(2);

    // ARM HLDGS: 60 × 99.63 cost basis → Einstandswert 5,977.80 with German thousands separator.
    const arm = transactions.find(t => t.security_isin === 'US0420682058');
    expect(arm).toBeDefined();
    expect(arm!.type).toBe('buy');
    expect(Number(arm!.quantity)).toBe(60);
    expect(Number(arm!.amount)).toBeCloseTo(5977.80, 2);
  });

  test('rejects a brokerage CSV imported into a checking account', async ({ request }) => {
    const create = await request.post('/api/settings/accounts', {
      data: { name: 'E2E ING Wrong Type', institution: 'ing', type: 'checking', currency: 'EUR' },
    });
    expect(create.ok()).toBeTruthy();
    const account = await create.json();
    cleanup.push(account.id);

    const csvData = fs.readFileSync(ordersCSV);
    const importResp = await request.post('/api/import', {
      multipart: {
        account_id: account.id,
        file: { name: 'ing_ordermanager.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(importResp.status()).toBe(400);
    expect(JSON.stringify(await importResp.json()).toLowerCase()).toContain('brokerage');
  });
});
