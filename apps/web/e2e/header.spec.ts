import { expect, test } from '@playwright/test';
import { gotoReady } from './helpers';

for (const width of [1280, 390]) {
  test(`Detail header pairs Get and Homepage at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await gotoReady(page, '/apps/visual-studio-code');
    const actions = page.getByTestId('package-header-actions');
    const get = actions.getByRole('button', { name: 'Get Visual Studio Code', exact: true });
    const homepage = actions.getByRole('link', { name: 'Homepage', exact: true });
    await expect(get).toBeVisible();
    await expect(homepage).toBeVisible();
    const [primary, secondary] = await Promise.all([get.boundingBox(), homepage.boundingBox()]);
    expect(primary).not.toBeNull();
    expect(secondary).not.toBeNull();
    expect(Math.abs(primary!.y - secondary!.y)).toBeLessThanOrEqual(1);
    expect(secondary!.x - (primary!.x + primary!.width)).toBeGreaterThanOrEqual(7);
    expect(secondary!.x - (primary!.x + primary!.width)).toBeLessThanOrEqual(9);
    if (width === 390) expect(Math.abs(primary!.width - secondary!.width)).toBeLessThanOrEqual(1);
    await expect(actions).not.toContainText('Official download');
    await expect(page.getByRole('heading', { level: 1 }).locator('..').locator('..')).not.toContainText(
      'From homebrew/cask'
    );
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  });
}
