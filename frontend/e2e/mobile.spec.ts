import { test, expect, type Page } from '@playwright/test';

/** The page must not scroll sideways: content width equals the viewport width. */
async function expectNoHorizontalOverflow(page: Page) {
  const { scrollWidth, bodyWidth, viewport } = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    bodyWidth: document.body.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(scrollWidth).toBeLessThanOrEqual(viewport);
  expect(bodyWidth).toBeLessThanOrEqual(viewport);
}

/** The bottom tab bar (the only "Main navigation" landmark visible on phones). */
const bottomBar = (page: Page) => page.getByRole('navigation', { name: 'Main navigation' });

test.describe('Mobile viewport (375px iPhone)', () => {
  test.use({ viewport: { width: 375, height: 812 } });

  test('Net Worth page renders without horizontal overflow', async ({ page }) => {
    await page.goto('/');
    await page.waitForSelector('text=Total Net Worth');
    await expect(page.getByText('Total Net Worth')).toBeVisible();
    // Period selector is usable on a phone; 1Y is the default
    const periods = page.getByRole('group', { name: 'Time period' });
    await expect(periods.getByRole('button', { name: '1Y', exact: true })).toHaveAttribute('aria-pressed', 'true');
    await expect(periods.getByRole('button', { name: 'All', exact: true })).toBeVisible();
    await expect(page.getByText('Liquid wealth', { exact: true })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });

  test('app header carries the page title on phones', async ({ page }) => {
    await page.goto('/');
    // Exactly one visible h1, and it sits in the app header rather than the page body
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
    await expect(page.getByRole('banner').getByRole('heading', { name: 'Net Worth', level: 1 })).toBeVisible();
    await bottomBar(page).getByRole('link', { name: 'Portfolio', exact: true }).click();
    await expect(page.getByRole('banner').getByRole('heading', { name: 'Portfolio', level: 1 })).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
  });

  test('Portfolio page shows performance grid in 2 columns', async ({ page }) => {
    await page.goto('/');
    await bottomBar(page).getByRole('link', { name: 'Portfolio', exact: true }).click();
    await page.waitForURL('**/portfolio');
    await expect(page.getByRole('heading', { name: 'Performance', exact: true })).toBeVisible();
    const invested = page.getByText('Net capital invested', { exact: true });
    const totalGain = page.getByText('Total gain · since inception', { exact: true });
    await expect(invested).toBeVisible();
    await expect(totalGain).toBeVisible();
    await expect(page.getByText('Realised gain · since inception', { exact: true })).toBeVisible();
    await expect(page.getByText('Remaining gain · since inception', { exact: true })).toBeVisible();
    // Prices as of should be below title, not cramping it
    await expect(page.getByText('Prices as of')).toBeVisible();
    // Two columns: the first two tiles share a row, side by side
    const a = (await invested.boundingBox())!;
    const b = (await totalGain.boundingBox())!;
    expect(Math.abs(a.y - b.y)).toBeLessThan(4);
    expect(b.x).toBeGreaterThan(a.x + 100);
    await expectNoHorizontalOverflow(page);
  });

  test('Transactions page filters fit on mobile and rows are cards', async ({ page }) => {
    await page.goto('/');
    await bottomBar(page).getByRole('link', { name: 'Transactions', exact: true }).click();
    await page.waitForURL('**/transactions');
    // All three filters and the search field should be visible
    await expect(page.getByLabel('Filter by account')).toBeVisible();
    await expect(page.getByLabel('Filter by transaction type')).toBeVisible();
    await expect(page.getByLabel('Filter by month')).toBeVisible();
    await expect(page.getByPlaceholder('Search counterparty, ISIN...')).toBeVisible();
    // Verify transaction count is shown
    await expect(page.getByText('total')).toBeVisible();
    // Mobile card layout should be used (not the desktop table)
    await expect(page.getByRole('main').getByRole('listitem').first()).toBeVisible();
    await expect(page.getByRole('table')).toHaveCount(0);
    await expectNoHorizontalOverflow(page);
  });

  test('Transactions mobile cards show clean security info without 0 × 0,00 noise', async ({ page }) => {
    await page.goto('/');
    await bottomBar(page).getByRole('link', { name: 'Transactions', exact: true }).click();
    await page.waitForURL('**/transactions');
    await expect(page.getByRole('main').getByRole('listitem').first()).toBeVisible();
    // Dividend cards should NOT show "0 × 0,00 €" (nor its redesigned form "0 shares at 0,00 €")
    await expect(page.locator('text=0 × 0,00 €')).toHaveCount(0);
    await expect(page.getByText(/(^|\s)0 shares/)).toHaveCount(0);
  });

  test('Cashflow page renders without horizontal overflow', async ({ page }) => {
    await page.goto('/');
    await bottomBar(page).getByRole('link', { name: 'Cashflow', exact: true }).click();
    await page.waitForURL('**/cashflow');
    await expect(page.getByRole('heading', { name: 'Monthly Cashflow' })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });

  test('Analysis page renders charts without overflow', async ({ page }) => {
    // Analysis is reached through the More tab on phones
    await page.goto('/');
    await bottomBar(page).getByRole('link', { name: 'More', exact: true }).click();
    await page.getByRole('main').getByRole('link', { name: /^Analysis/ }).click();
    await page.waitForURL('**/analysis');
    // Tab bar should be visible with Risk as default (TabBar uses role="tab")
    await expect(page.getByRole('tab', { name: 'Risk' })).toBeVisible();
    await expect(page.getByRole('tab', { name: 'Risk' })).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByRole('tab', { name: 'Costs' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Risk Analytics' })).toBeVisible();
    await expectNoHorizontalOverflow(page);
  });

  test('Bottom tab bar has exactly five tabs on all pages', async ({ page }) => {
    const tabs = ['Net Worth', 'Portfolio', 'Cashflow', 'Transactions', 'More'];
    for (const path of ['/', '/portfolio', '/cashflow', '/transactions', '/more', '/analysis', '/settings']) {
      await page.goto(path);
      const links = bottomBar(page).getByRole('link');
      await expect(links).toHaveCount(5);
      for (const tab of tabs) {
        await expect(bottomBar(page).getByRole('link', { name: tab, exact: true })).toBeVisible();
      }
    }
  });

  test('More lists Analysis, Planning, Tax and Settings and each is reachable', async ({ page }) => {
    const pages: [string, string][] = [
      ['Analysis', '/analysis'], ['Planning', '/planning'], ['Tax', '/tax'], ['Settings', '/settings'],
    ];
    for (const [name, path] of pages) {
      await page.goto('/');
      await bottomBar(page).getByRole('link', { name: 'More', exact: true }).click();
      await page.waitForURL('**/more');
      await expect(page.getByRole('banner').getByRole('heading', { name: 'More', level: 1 })).toBeVisible();
      const main = page.getByRole('main');
      await expect(main.getByRole('link')).toHaveCount(pages.length);
      // Link names are "<Page> <hint>", so match on the leading page name
      await main.getByRole('link', { name: new RegExp(`^${name}\\b`) }).click();
      await page.waitForURL(`**${path}`);
      await expect(page.getByRole('banner').getByRole('heading', { name, level: 1 })).toBeVisible();
      await expect(page.getByRole('tablist')).toBeVisible();
    }
  });
});
