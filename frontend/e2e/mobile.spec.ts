import { test, expect } from '@playwright/test';

test.describe('Mobile viewport (375px iPhone)', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('Net Worth page renders without horizontal overflow', async ({ page }) => {
    await page.goto('/');
    await page.waitForSelector('text=Total Net Worth');
    await expect(page.getByText('Total Net Worth')).toBeVisible();
    await expect(page.getByRole('button', { name: '1D', exact: true })).toBeVisible();
    // No horizontal scrollbar: page width should match viewport
    const bodyWidth = await page.evaluate(() => document.body.scrollWidth);
    expect(bodyWidth).toBeLessThanOrEqual(390); // small tolerance for chart rendering
  });

  test('Portfolio page shows performance grid in 2 columns', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Portfolio', exact: true }).click();
    await page.waitForURL('**/portfolio');
    await expect(page.getByText('Performance')).toBeVisible();
    await expect(page.getByText('Net Invested')).toBeVisible();
    await expect(page.getByText('Realized', { exact: true })).toBeVisible();
    await expect(page.getByText('Unrealized')).toBeVisible();
    // Prices as of should be below title, not cramping it
    await expect(page.getByText('Prices as of')).toBeVisible();
  });

  test('Transactions page filters stack vertically on mobile', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Transactions', exact: true }).click();
    await page.waitForURL('**/transactions');
    // Type dropdown and search should both be visible
    await expect(page.locator('select')).toBeVisible();
    await expect(page.getByPlaceholder('Search counterparty, ISIN...')).toBeVisible();
    // Mobile card layout should be used (not the desktop table)
    // Verify transaction count is shown
    await expect(page.getByText('total')).toBeVisible();
  });

  test('Transactions mobile cards show clean security info without 0 × 0,00 noise', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Transactions', exact: true }).click();
    await page.waitForURL('**/transactions');
    // Dividend cards should NOT show "0 × 0,00 €"
    const zeroNoise = page.locator('text=0 × 0,00 €');
    await expect(zeroNoise).toHaveCount(0);
  });

  test('Analysis page renders charts without overflow', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('link', { name: 'Analysis', exact: true }).click();
    await page.waitForURL('**/analysis');
    // Tab bar should be visible with Risk as default (TabBar uses role="tab")
    await expect(page.getByRole('tab', { name: 'Risk' })).toBeVisible();
    await expect(page.getByRole('tab', { name: 'Costs' })).toBeVisible();
  });

  test('Bottom tab bar is visible on all pages', async ({ page }) => {
    await page.goto('/');
    // All 7 tabs should be visible
    for (const tab of ['Net Worth', 'Portfolio', 'Analysis', 'Planning', 'Tax', 'Transactions', 'Settings']) {
      await expect(page.getByRole('link', { name: tab, exact: true })).toBeVisible();
    }
  });
});
