import { expect, test } from '@playwright/test';
import { gotoReady } from './helpers';

// M4-07: release timeline and SEO metadata (05 §6.1, 08 §10.6).
const timeline = '[aria-label="版本记录"]';

test('Version navigation expands three entries initially with correct title/description/canonical', async ({
  page
}) => {
  await gotoReady(page, '/zh/apps/visual-studio-code');
  await page.getByRole('link', { name: /^版本记录/ }).click();
  await page.waitForURL('**/apps/visual-studio-code/versions');

  const items = page.locator(`${timeline} > li`);
  await expect(items.first()).toContainText('最新');
  expect(await items.count()).toBeGreaterThanOrEqual(4);
  await expect(items.nth(2).locator('dl')).toBeVisible();
  await expect(items.nth(3)).toContainText('展开');

  await expect(page).toHaveTitle('VS Code 编辑器更新日志（最新 1.140.0）· OpenNavo');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', /最新版本 1\.140\.0/);
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', /\/apps\/visual-studio-code\/versions$/);
});

test('Notes/original filters enter URL but not canonical; collapsed entries expand', async ({ page }) => {
  await gotoReady(page, '/zh/apps/visual-studio-code/versions');
  await page.getByRole('radio', { name: '原文' }).click();
  await page.waitForURL(/original=1/);
  await expect(page.locator(`${timeline} > li`).first()).not.toContainText('AI 翻译');
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', /\/apps\/visual-studio-code\/versions$/);

  await page.getByRole('radio', { name: /^有更新说明/ }).click();
  await page.waitForURL(/notes=1/);
  await expect(page.locator(`${timeline} > li`).first()).toBeVisible();

  const collapsed = page.locator(`${timeline} > li`).nth(3);
  await collapsed.getByRole('button', { name: /展开/ }).click();
  await expect(collapsed.locator('dl')).toBeVisible();
});
