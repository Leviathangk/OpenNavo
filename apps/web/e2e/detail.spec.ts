import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { gotoReady } from './helpers';

// 10 §4 M2-04: missing-client deep-link guidance through simulated blur; replace real navigation with window.opennavoLaunchDeepLink.
// No blur means no client; emitting blur means the client handled the deep link.
async function stubDeepLink(page: Page, { appInstalled }: { appInstalled: boolean }) {
  await page.addInitScript(installed => {
    window.opennavoLaunchDeepLink = url => {
      (window as unknown as { opennavoLastDeepLink: string }).opennavoLastDeepLink = url;
      if (installed) window.dispatchEvent(new Event('blur'));
    };
  }, appInstalled);
}

async function openInApp(page: Page) {
  await page.getByRole('button', { name: '获取 VS Code 编辑器', exact: true }).click();
  await page.getByRole('menuitem', { name: '在 OpenNavo 中打开' }).click();
}

const lastDeepLink = (page: Page) =>
  page.evaluate(() => (window as unknown as { opennavoLastDeepLink?: string }).opennavoLastDeepLink);

test('Missing client: no blur after link prompts and offers copying commands', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await stubDeepLink(page, { appInstalled: false });
  await gotoReady(page, '/zh/apps/visual-studio-code');
  await openInApp(page);
  expect(await lastDeepLink(page)).toBe('opennavo://package/cask/visual-studio-code?action=install');

  const dialog = page.getByRole('dialog', { name: '还没有安装 OpenNavo？' });
  await expect(dialog).toBeVisible({ timeout: 4000 });
  await expect(dialog.getByRole('link', { name: '下载客户端' })).toHaveAttribute('href', '/zh/download');
  await dialog.getByRole('button', { name: '改为复制安装命令' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.getByRole('region', { name: '通知' })).toContainText('已复制安装命令');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('brew install --cask visual-studio-code');
});

test('Installed client: blur after link suppresses prompt', async ({ page }) => {
  await stubDeepLink(page, { appInstalled: true });
  await gotoReady(page, '/zh/apps/visual-studio-code');
  await openInApp(page);
  await page.waitForTimeout(2000);
  await expect(page.getByRole('dialog')).toHaveCount(0);
});

test('Do not ask again makes future opens copy commands directly', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await stubDeepLink(page, { appInstalled: false });
  await gotoReady(page, '/zh/apps/visual-studio-code');
  await openInApp(page);
  await page.getByRole('dialog').getByRole('button', { name: '不再提示' }).click();
  await expect(page.getByRole('dialog')).toBeHidden();

  await page.evaluate(() => navigator.clipboard.writeText(''));
  await openInApp(page);
  await page.waitForTimeout(2000);
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('brew install --cask visual-studio-code');
});

test('Detail tabs have independent URLs; unknown packages return 404', async ({ page }) => {
  await gotoReady(page, '/zh/apps/visual-studio-code');
  await page.getByRole('link', { name: '依赖与冲突' }).click();
  await expect(page).toHaveURL(/\/apps\/visual-studio-code\/dependencies$/);
  await page.getByRole('link', { name: '安装细节' }).click();
  await expect(page).toHaveURL(/\/details$/);
  await expect(page.getByText('formulae.brew.sh').first()).toBeVisible();

  const response = await page.goto('/apps/no-such-app-zz');
  expect(response?.status()).toBe(404);
});
