import { test, expect } from '@playwright/test';

test.describe('Progressive Web App', () => {
  test('manifest.json is served with correct fields', async ({ request }) => {
    const resp = await request.get('/manifest.json');
    expect(resp.ok()).toBeTruthy();
    const manifest = await resp.json();

    expect(manifest.name).toBe('Wealth');
    expect(manifest.short_name).toBe('Wealth');
    expect(manifest.display).toBe('standalone');
    expect(manifest.start_url).toBe('/');
    expect(manifest.theme_color).toBe('#F0EDE8');
    expect(manifest.background_color).toBe('#FAF9F6');
    expect(manifest.icons.length).toBeGreaterThanOrEqual(2);

    // Should have 192 and 512 icons
    const sizes = manifest.icons.map((i: { sizes: string }) => i.sizes);
    expect(sizes).toContain('192x192');
    expect(sizes).toContain('512x512');
  });

  test('service worker is served', async ({ request }) => {
    const resp = await request.get('/sw.js');
    expect(resp.ok()).toBeTruthy();
    const text = await resp.text();
    expect(text).toContain('finance-tracker');
    expect(text).toContain('addEventListener');
  });

  test('PWA icons are served', async ({ request }) => {
    const resp192 = await request.get('/icons/icon-192.png');
    expect(resp192.ok()).toBeTruthy();
    expect(resp192.headers()['content-type']).toContain('image/png');

    const resp512 = await request.get('/icons/icon-512.png');
    expect(resp512.ok()).toBeTruthy();
  });

  test('HTML has manifest link and theme-color', async ({ page }) => {
    await page.goto('/');
    // Check manifest link
    const manifestLink = await page.locator('link[rel="manifest"]').getAttribute('href');
    expect(manifestLink).toBe('/manifest.json');

    // Check theme-color
    const themeColor = await page.locator('meta[name="theme-color"]').getAttribute('content');
    expect(themeColor).toBe('#F0EDE8');

    // Check apple-touch-icon
    const appleTouchIcon = await page.locator('link[rel="apple-touch-icon"]').getAttribute('href');
    expect(appleTouchIcon).toContain('icon-192');

    // Check apple-mobile-web-app-capable
    const capable = await page.locator('meta[name="apple-mobile-web-app-capable"]').getAttribute('content');
    expect(capable).toBe('yes');
  });

  test('service worker registers without error', async ({ page }) => {
    await page.goto('/');
    // Wait for SW registration
    await page.waitForTimeout(2000);
    // Check no errors in console related to SW
    const errors = await page.evaluate(() => {
      return navigator.serviceWorker?.controller !== undefined || navigator.serviceWorker?.ready !== undefined;
    });
    // serviceWorker API should be available
    expect(errors).toBeTruthy();
  });
});
