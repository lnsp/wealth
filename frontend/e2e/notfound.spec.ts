import { test, expect } from '@playwright/test';

test.describe('404 Not Found page', () => {
  test('shows 404 page for nonexistent routes', async ({ page }) => {
    await page.goto('/nonexistent-page');
    await expect(page.getByText('404')).toBeVisible();
    await expect(page.getByText('Page not found')).toBeVisible();
    await expect(page.getByRole('link', { name: 'Go to Net Worth' })).toBeVisible();
  });

  test('404 page has working link back to home', async ({ page }) => {
    await page.goto('/nonexistent-page');
    await page.getByRole('link', { name: 'Go to Net Worth' }).click();
    await page.waitForURL('**/');
    await expect(page.getByText('Total Net Worth')).toBeVisible();
  });

  test('preserves URL for unknown routes', async ({ page }) => {
    await page.goto('/some/deep/invalid/path');
    expect(page.url()).toContain('/some/deep/invalid/path');
    await expect(page.getByText('404')).toBeVisible();
  });

  test('valid routes still work normally', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText('Total Net Worth')).toBeVisible();
    // No 404 on valid routes
    const notFoundText = page.getByText('Page not found');
    await expect(notFoundText).toHaveCount(0);
  });
});
