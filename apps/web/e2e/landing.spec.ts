import { expect, test } from '@playwright/test';
import { gotoReady } from './helpers';

// Landing page (05 §4, 08 §10.14): hero and download/browse links; store Discover lives at /discover.
test('Homepage is introduction with download/browse; Discover navigation points to /discover', async ({ page }) => {
  await gotoReady(page, '/zh');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(
    /Mac 上的 Homebrew 应用商店，\s*病毒扫描与应用防火墙。/
  );
  await expect(page.getByRole('link', { name: /免费病毒扫描已上线/ })).toHaveAttribute('href', '/zh/security');
  await expect(page.getByRole('link', { name: '下载 Mac 版' }).first()).toHaveAttribute('href', '/zh/download');
  await expect(page.getByRole('link', { name: '浏览 App' }).first()).toHaveAttribute('href', '/zh/discover');
  const nav = page.getByRole('navigation', { name: '主导航' }).first();
  await expect(nav.getByRole('link', { name: '发现' })).toHaveAttribute('href', '/zh/discover');
  // Playwright text matching skips script elements, so toContainText is always empty; retry reading textContent instead.
  await expect
    .poll(() => page.locator('script[type="application/ld+json"]').first().textContent())
    .toContain('SearchAction');
});

test('Security page explains the antivirus and marks its navigation item', async ({ page }) => {
  await gotoReady(page, '/zh/security');
  await expect(page).toHaveTitle('免费 Mac 杀毒软件：ClamAV 病毒扫描与启动项检查 · OpenNavo');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText(/免费开源的 Mac 杀毒，\s*装软件前先查毒。/);
  const nav = page.getByRole('navigation', { name: '主导航' }).first();
  await expect(nav.getByRole('link', { name: '安全防护' })).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('link', { name: '下载 Mac 版' }).first()).toHaveAttribute('href', '/zh/download');
});

test('Discover lives at /discover with current sidebar state and distinct title', async ({ page }) => {
  await gotoReady(page, '/zh/discover');
  await expect(page).toHaveTitle('发现 Mac App · OpenNavo');
  await expect(page.getByRole('link', { name: '发现', exact: true }).first()).toHaveAttribute('aria-current', 'page');
  await expect(page.getByRole('heading', { name: '热门 App' })).toBeVisible();
});

test('Allowed motion reveals below-viewport sections only on entry', async ({ page }) => {
  await gotoReady(page, '/zh');
  const cta = page.getByRole('heading', { name: '从下一个 App 开始。' });
  await expect(cta).toHaveAttribute('data-reveal', 'pending');
  await cta.scrollIntoViewIfNeeded();
  await expect(cta).toHaveAttribute('data-reveal', 'shown');
});

test('Reduced motion immediately shows sections without pending reveals', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await gotoReady(page, '/zh');
  await expect(page.locator('[data-reveal="pending"]')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: '从下一个 App 开始。' })).toBeVisible();
});
