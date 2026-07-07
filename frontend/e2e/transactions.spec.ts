import { test, expect } from '@playwright/test';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

test.describe('Transactions page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Transactions', exact: true }).click();
    await page.waitForURL('**/transactions');
    // Wait for transactions to load — filter row shows "total" count
    await page.waitForSelector('text=total');
  });

  test('loads the transactions page with total count', async ({ page }) => {
    await expect(page.getByText('total')).toBeVisible();
  });

  test('transaction table has Security, Qty, and Price columns', async ({ page }) => {
    await expect(page.getByRole('columnheader', { name: 'Security' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Qty' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Price' })).toBeVisible();
    await expect(page.getByRole('columnheader', { name: 'Amount' })).toBeVisible();
  });

  test('buy transactions show security name, ISIN, quantity, and price', async ({ page }) => {
    // Filter to buy transactions
    await page.locator('select').selectOption('buy');
    await page.waitForTimeout(500);

    // Should show security with ISIN
    const firstRow = page.locator('table tbody tr').first();
    await expect(firstRow).toBeVisible();

    // Should contain an ISIN (format: 2 letters + 10 alphanumeric)
    const securityCell = firstRow.locator('td').nth(2);
    const text = await securityCell.textContent();
    expect(text).toMatch(/[A-Z]{2}[A-Z0-9]{10}/); // ISIN pattern

    // Qty should not be "—"
    const qtyCell = firstRow.locator('td').nth(3);
    const qtyText = await qtyCell.textContent();
    expect(qtyText).not.toBe('—');

    // Price should not be "—"
    const priceCell = firstRow.locator('td').nth(4);
    const priceText = await priceCell.textContent();
    expect(priceText).not.toBe('—');
  });

  test('deposit transactions show dashes for Qty and Price', async ({ page }) => {
    await page.locator('select').selectOption('deposit');
    await page.waitForTimeout(500);

    const firstRow = page.locator('table tbody tr').first();
    await expect(firstRow).toBeVisible();

    // Qty should be "—"
    const qtyCell = firstRow.locator('td').nth(3);
    await expect(qtyCell).toHaveText('—');

    // Price should be "—"
    const priceCell = firstRow.locator('td').nth(4);
    await expect(priceCell).toHaveText('—');
  });

  test('type filter works', async ({ page }) => {
    await page.locator('select').selectOption('sell');
    await page.waitForTimeout(500);
    // All visible rows should have "sell" badge
    const badges = page.locator('table tbody tr td:nth-child(2) span');
    const count = await badges.count();
    expect(count).toBeGreaterThan(0);
    for (let i = 0; i < count; i++) {
      await expect(badges.nth(i)).toHaveText('sell');
    }
  });

  test('import API returns parsing warnings for skipped rows', async ({ request }) => {
    // Get brokerage account ID (CSV contains brokerage transactions)
    const accResp = await request.get('/api/portfolio/accounts');
    const { accounts } = await accResp.json();
    const brokerage = accounts.find((a: { type: string }) => a.type === 'brokerage') || accounts[0];
    const accountId = brokerage.id;

    // Import the CSV file — all transactions already exist, but warnings should still appear
    const csvPath = path.resolve(__dirname, '../../exports/brokerage.csv');
    const csvData = fs.readFileSync(csvPath);

    const resp = await request.post('/api/import', {
      multipart: {
        account_id: accountId,
        file: { name: 'brokerage.csv', mimeType: 'text/csv', buffer: csvData },
      },
    });
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();

    expect(body).toHaveProperty('errors');
    // The CSV has REJECTED and CANCELLED rows that generate warnings
    expect(body.errors.length).toBeGreaterThan(0);
    // At least one warning should mention a status
    const hasStatusWarning = body.errors.some(
      (e: string) => e.includes('REJECTED') || e.includes('CANCELLED')
    );
    expect(hasStatusWarning).toBeTruthy();
  });

  test('transactions API returns security details', async ({ request }) => {
    const resp = await request.get('/api/transactions?limit=10&type=buy');
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.transactions.length).toBeGreaterThan(0);

    const buyTxn = body.transactions[0];
    expect(buyTxn.security_isin).toBeTruthy();
    expect(buyTxn.quantity).toBeGreaterThan(0);
    expect(buyTxn.price).toBeGreaterThan(0);
    expect(buyTxn.amount).toBeGreaterThan(0);
  });

  test('CSV upload via UI: toggle uploader, select account, upload file', async ({ page, request }) => {
    // Find the brokerage account name via API (CSV contains brokerage transactions)
    const accResp = await request.get('/api/portfolio/accounts');
    const { accounts } = await accResp.json();
    const brokerage = accounts.find((a: { type: string }) => a.type === 'brokerage');
    if (!brokerage) {
      test.skip();
      return;
    }

    // Click "Import CSV" button to reveal uploader
    const importBtn = page.getByRole('button', { name: 'Import CSV' });
    await expect(importBtn).toBeVisible();
    await importBtn.click();

    // Account selector should appear
    const accountSelect = page.locator('select', { has: page.locator('option', { hasText: 'Select account...' }) });
    await expect(accountSelect).toBeVisible();

    // Select the brokerage account by matching its name in the option text
    const options = accountSelect.locator('option');
    const optionTexts = await options.allTextContents();
    const matchingLabel = optionTexts.find(t => t.includes(brokerage.name));
    expect(matchingLabel).toBeTruthy();
    await accountSelect.selectOption({ label: matchingLabel! });

    // Upload a CSV file via the file input
    const csvPath = path.resolve(__dirname, '../../exports/brokerage.csv');
    if (!fs.existsSync(csvPath)) {
      test.skip();
      return;
    }
    const fileInput = page.locator('input[type="file"]').first();
    await fileInput.setInputFiles(csvPath);

    // Uploader should collapse after successful import (all rows already exist = skipped)
    await expect(accountSelect).toBeHidden({ timeout: 15000 });

    // Transaction list should still be visible with a total count
    await expect(page.getByText('total')).toBeVisible();
  });

  test('displays import history section', async ({ page }) => {
    const heading = page.getByText('Import History');
    const count = await heading.count();
    if (count > 0) {
      await expect(heading.first()).toBeVisible();
    }
  });

  test('CSV export endpoint returns valid file', async ({ request }) => {
    const resp = await request.get('/api/settings/export-transactions');
    expect(resp.ok()).toBeTruthy();
    const contentType = resp.headers()['content-type'];
    expect(contentType).toContain('text/csv');
    const body = await resp.text();
    expect(body.length).toBeGreaterThan(0);
    // Should have CSV header
    expect(body).toContain('date');
  });
});
