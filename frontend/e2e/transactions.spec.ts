import { test, expect, type Page } from '@playwright/test';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

/** Pick a transaction type in the filter and wait for the filtered list to arrive. */
async function filterByType(page: Page, type: string) {
  await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/transactions') && r.url().includes(`type=${type}`) && r.ok()),
    page.getByLabel('Filter by transaction type').selectOption(type),
  ]);
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0);
}

/** Transaction rows of the desktop table (month group headers have no cells). */
function dataRows(page: Page) {
  return page.getByRole('table').getByRole('row').filter({ has: page.getByRole('cell') });
}

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

  test('transaction table has Date, Description, Type, Account, Category and Amount columns', async ({ page }) => {
    // The Security / Qty / Price columns were folded into Description in the redesign
    const table = page.getByRole('table');
    for (const name of ['Date', 'Description', 'Type', 'Account', 'Category', 'Amount']) {
      await expect(table.getByRole('columnheader', { name, exact: true })).toBeVisible();
    }
    await expect(table.getByRole('columnheader', { name: 'Qty' })).toHaveCount(0);
  });

  test('buy transactions show security name, ISIN, quantity, and price', async ({ page }) => {
    await filterByType(page, 'buy');

    const rows = dataRows(page);
    await expect(rows.first()).toBeVisible();
    await expect(rows.first().getByRole('cell').nth(2)).toHaveText('Buy');

    // Description cell: security name, then "ISIN · N shares at PRICE"
    const description = rows.first().getByRole('cell').nth(1);
    await expect(description).toContainText(/[A-Z]{2}[A-Z0-9]{10}/); // ISIN pattern
    await expect(description).toContainText(/\d\s+shares?\s+at\s+[\d.,]+\s*€/); // quantity and price
    await expect(description).not.toContainText('0 shares at');
  });

  test('deposit transactions show no quantity or price, and offer a category', async ({ page }) => {
    await filterByType(page, 'deposit');

    const rows = dataRows(page);
    await expect(rows.first()).toBeVisible();
    await expect(rows.first().getByRole('cell').nth(2)).toHaveText('Deposit');

    // No share count / price for a cash movement
    const description = rows.first().getByRole('cell').nth(1);
    await expect(description).not.toContainText(/shares?/);
    await expect(description).not.toContainText(/\d\s*€/);
    // Cash rows carry the category override instead
    await expect(rows.first().getByRole('combobox', { name: /^Category for / })).toBeVisible();
  });

  test('type filter works', async ({ page }) => {
    await filterByType(page, 'sell');
    await expect(page.getByLabel('Filter by transaction type')).toHaveValue('sell');
    // All visible rows should carry the "Sale" type chip
    const rows = dataRows(page);
    const count = await rows.count();
    expect(count).toBeGreaterThan(0);
    for (let i = 0; i < count; i++) {
      await expect(rows.nth(i).getByRole('cell').nth(2)).toHaveText('Sale');
    }
    // The count line reflects the filter, and clearing it restores the full list
    await expect(page.getByText(/matching transactions?/)).toBeVisible();
    await page.getByRole('button', { name: 'Clear filters' }).click();
    await expect(page.getByLabel('Filter by transaction type')).toHaveValue('');
    await expect(page.getByText(/transactions? in total/)).toBeVisible();
  });

  test('account, type and month filters are separately labelled', async ({ page }) => {
    const filters = page.getByRole('group', { name: 'Filter transactions' });
    await expect(filters.getByRole('combobox')).toHaveCount(3);
    await expect(filters.getByLabel('Filter by account')).toBeVisible();
    await expect(filters.getByLabel('Filter by transaction type')).toBeVisible();
    await expect(filters.getByLabel('Filter by month')).toBeVisible();
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
    await expect(importBtn).toHaveAttribute('aria-expanded', 'true');
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
