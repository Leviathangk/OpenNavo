import { expect, test } from '@playwright/test';
import { gotoReady } from './helpers';

// Developer map and the anonymous submission status page. The status case creates
// a real receipt through the E2E backend so the token round-trip is covered.

const apiBase = process.env.E2E_API_BASE ?? 'http://127.0.0.1:18082/api/v1';

test('the developer map shows totals, tabs and the city list', async ({ page }) => {
  await gotoReady(page, '/zh/developer-map');

  await expect(page.getByRole('heading', { level: 1 })).toHaveText('开发者地图');
  await expect(page.getByRole('tab', { name: '地图' })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByRole('tab', { name: '提交 App' })).toBeVisible();
  await expect(page.getByRole('tab', { name: '认领与更新' })).toBeVisible();
  await expect(page.getByRole('tab', { name: '文档' })).toBeVisible();
  await expect(page.locator('.dm-world')).toBeVisible({ timeout: 15_000 });

  await page.getByRole('tab', { name: '提交 App' }).click();
  await expect(page.getByRole('heading', { name: '先贴一个链接' })).toBeVisible();
});

test('a receipt token shows the submission status timeline', async ({ page, request }) => {
  const response = await request.post(`${apiBase}/developer/submissions`, {
    data: {
      kind: 'new',
      sourceKind: 'github',
      sourceUrl: 'https://github.com/example/status-fixture',
      contactEmail: 'status-fixture@example.com',
      payload: {
        name: 'Status Fixture',
        summary: 'A fixture app for the submission status page.',
        developer: 'Fixture Studio',
        homepage: 'https://example.com/status-fixture',
        repoUrl: 'https://github.com/example/status-fixture',
        countryCode: 'US'
      }
    }
  });
  expect(response.ok()).toBeTruthy();
  const receipt = (await response.json()) as { data: { token: string; reference: string } };

  await gotoReady(page, `/zh/developer-map?tab=submit&submission=${receipt.data.token}`);

  await expect(page.getByRole('heading', { name: '查询提交状态' })).toBeVisible();
  await expect(page.getByText(receipt.data.reference)).toBeVisible();
  await expect(page.getByText('已提交', { exact: true }).first()).toBeVisible();
  await expect(page.getByRole('heading', { name: '进度' })).toBeVisible();
});

test('an unknown token reports that no submission matches', async ({ page }) => {
  await gotoReady(page, '/zh/developer-map?tab=submit');

  const panel = page.locator('section', { has: page.getByRole('heading', { name: '查询提交状态' }) }).last();
  await panel.getByPlaceholder('查询码（32 位）').fill('a'.repeat(32));
  await panel.getByRole('button', { name: '查询', exact: true }).click();
  await expect(page.getByText('没有找到对应的提交，请检查查询码。')).toBeVisible();
});

test('submitting without an app name explains what is missing', async ({ page }) => {
  await gotoReady(page, '/zh/developer-map?tab=submit');

  const form = page.locator('section', { has: page.getByRole('heading', { name: '先贴一个链接' }) }).first();
  await form.getByPlaceholder('https://github.com/owner/app').fill('https://127.0.0.1/secret');
  await form.getByRole('button', { name: '抓取信息' }).click();
  await form.getByRole('button', { name: '手动填写' }).click();
  await page.getByPlaceholder('you@example.com').fill('dev@example.com');
  await page.getByRole('button', { name: '提交审核' }).click();
  await expect(page.getByText('请填写必填字段。')).toBeVisible();
});
