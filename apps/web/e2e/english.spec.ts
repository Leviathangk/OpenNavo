import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { gotoReady } from './helpers';

// 10 §4 M2-09: English root-path detail page has English UI, package body, SEO, and six-language hreflang links.

const CJK = /[一-鿿]/;

function headInfo(page: Page) {
  return page.evaluate(() => ({
    description: document.querySelector('meta[name="description"]')?.getAttribute('content') ?? '',
    ogLocale: document.querySelector('meta[property="og:locale"]')?.getAttribute('content') ?? '',
    canonical: document.querySelector('link[rel="canonical"]')?.getAttribute('href') ?? '',
    alternates: Object.fromEntries(
      Array.from(document.querySelectorAll('link[rel="alternate"][hreflang]'), link => [
        link.getAttribute('hreflang'),
        new URL(link.getAttribute('href') ?? '', location.href).pathname
      ])
    )
  }));
}

test('English details have English UI/body and complete SEO/hreflang', async ({ page }) => {
  await gotoReady(page, '/apps/visual-studio-code');

  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page).toHaveTitle('Visual Studio Code for Mac – Download & Install · OpenNavo');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Visual Studio Code');

  // Left navigation (05 §11.2); Cask-only (ADR-018), without a command-line tools entry.
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  for (const label of ['Discover', 'Categories', 'Rankings', 'Collections']) {
    await expect(nav.getByRole('link', { name: label })).toBeVisible();
  }
  await expect(nav.getByRole('link', { name: 'Command-line Tools' })).toHaveCount(0);
  const tabs = page.getByRole('navigation', { name: 'Package tabs' });
  for (const label of ['Overview', 'Release Notes', 'Dependencies', 'Install Details']) {
    await expect(tabs.getByRole('link', { name: new RegExp(`^${label}`) })).toBeVisible();
  }
  await expect(page.getByRole('button', { name: 'Get Visual Studio Code', exact: true })).toBeVisible();
  // Long install commands use Shell continuation across two lines.
  const install = page.locator('main pre', { hasText: 'brew install' });
  await expect(install).toContainText('brew install --cask');
  await expect(install).toContainText('visual-studio-code');

  // The spec card filters alternate names along with the rest of the English body.
  const spec = page.getByTestId('package-spec-card');
  await expect(spec).toContainText('Also known as');
  await expect(spec).toContainText('vscode');
  await expect(spec).not.toContainText('VS Code 编辑器');
  expect(await page.locator('main').textContent()).not.toMatch(CJK);

  const head = await headInfo(page);
  expect(head.description).toContain('Visual Studio Code');
  expect(head.description).toBe(
    'Deterministic E2E fixture for Visual Studio Code. Explore installation, uninstallation, and release history for Visual Studio Code on Mac.'
  );
  expect(head.description).not.toMatch(CJK);
  expect(head.ogLocale).toBe('en_US');
  expect(new URL(head.canonical).pathname).toBe('/apps/visual-studio-code');
  expect(head.alternates).toMatchObject({
    'x-default': '/apps/visual-studio-code',
    'zh-Hans': '/zh/apps/visual-studio-code',
    en: '/apps/visual-studio-code',
    ja: '/ja/apps/visual-studio-code',
    es: '/es/apps/visual-studio-code',
    'pt-BR': '/pt/apps/visual-studio-code',
    ru: '/ru/apps/visual-studio-code'
  });
});

test('Every English detail tab has an English title', async ({ page }) => {
  const cases: [string, RegExp][] = [
    ['/apps/visual-studio-code/versions', /^Visual Studio Code Release Notes \(Latest 1\.140\.0\) · OpenNavo$/],
    ['/apps/visual-studio-code/dependencies', /^Visual Studio Code Dependencies · OpenNavo$/],
    ['/apps/visual-studio-code/details', /^Visual Studio Code Install Details · OpenNavo$/]
  ];
  for (const [path, title] of cases) {
    await gotoReady(page, path);
    await expect(page).toHaveTitle(title);
    // The server's PageDetails decision consolidates install details into the overview.
    expect(new URL((await headInfo(page)).canonical).pathname).toBe(
      path.endsWith('/details') ? '/apps/visual-studio-code' : path
    );
  }
});

test('Removed command-line pages return 404 at /en/cli', async ({ page }) => {
  // Cask-only (ADR-018, 05 §11.1): former command-line detail cases now assert not found.
  const response = await page.goto('/cli/ripgrep');
  expect(response?.status()).toBe(404);
});

test('English-to-Chinese switching retains the same package', async ({ page }) => {
  await gotoReady(page, '/apps/visual-studio-code');
  await page.getByRole('button', { name: /^Choose language:/ }).click();
  const switcher = page.getByRole('menuitem', { name: '简体中文' });
  await expect(switcher).toBeVisible();
  await switcher.click();
  await page.waitForURL(url => url.pathname === '/zh/apps/visual-studio-code');
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-Hans');
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeVisible();
});

test('Mobile locale menu stays onscreen and preserves search route/query', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await gotoReady(page, '/search?q=vscode');
  await page.getByRole('button', { name: 'Open menu' }).click();
  await page.getByRole('button', { name: /^Choose language:/ }).click();
  const menu = page.getByRole('menu', { name: 'Choose language' });
  await expect(menu).toBeVisible();
  // Every site language (L1): the six app languages plus German, French, Italian and Traditional Chinese.
  await expect(menu.getByRole('menuitem')).toHaveCount(10);
  const bounds = await menu.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.x).toBeGreaterThanOrEqual(0);
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(375);
  await menu.getByRole('menuitem', { name: 'Русский' }).click();
  await page.waitForURL(url => url.pathname === '/ru/search' && url.searchParams.get('q') === 'vscode');
  await expect(page.locator('html')).toHaveAttribute('lang', 'ru');
});
