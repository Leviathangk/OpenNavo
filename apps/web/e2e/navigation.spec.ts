import { expect, test } from '@playwright/test';
import { gotoReady } from './helpers';

// T6: the server render carries only the messages it used; the full language loads before the first
// client navigation, so pages reached in the browser read as completely as server-rendered ones.

test('The first client navigation renders the next page in the same language without missing strings', async ({
  page
}) => {
  const missing: string[] = [];
  page.on('console', message => {
    if (/Not found '.+' key|Cannot translate the value/.test(message.text())) missing.push(message.text());
  });
  await gotoReady(page, '/zh/apps/visual-studio-code');
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '分类' }).click();
  await page.waitForURL(url => url.pathname === '/zh/categories');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('分类');
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-Hans');
  expect(missing).toEqual([]);
});

test('Switching to a site-only language keeps the page and its language', async ({ page }) => {
  await gotoReady(page, '/apps/visual-studio-code');
  await page.getByRole('button', { name: /^Choose language:/ }).click();
  await page.getByRole('menuitem', { name: 'Deutsch' }).click();
  await page.waitForURL(url => url.pathname === '/de/apps/visual-studio-code');
  await expect(page.locator('html')).toHaveAttribute('lang', 'de');
  await expect(page.getByRole('navigation', { name: 'Hauptnavigation' })).toBeVisible();
  // Moving on in the browser stays German.
  await page.getByRole('navigation', { name: 'Hauptnavigation' }).getByRole('link', { name: 'Kategorien' }).click();
  await page.waitForURL(url => url.pathname === '/de/categories');
  await expect(page.locator('html')).toHaveAttribute('lang', 'de');
});
