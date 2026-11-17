import { test, expect } from '@playwright/test';

test.describe('404 Not Found page', () => {
  test('shows 404 page for nonexistent routes', async ({ page }) => {
    await page.goto('/nonexistent-page');
    await expect(page.getByText('Error 404')).toBeVisible();
    // Exactly one visible h1 carries the title (page h1 from 768px up, app header on phones)
    await expect(page.getByRole('heading', { name: 'Page not found', level: 1 })).toBeVisible();
    await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1);
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
    await expect(page.getByRole('heading', { name: 'Net Worth', level: 1 })).toBeVisible();
    const notFoundText = page.getByText('Page not found');
    await expect(notFoundText).toHaveCount(0);
  });
});
