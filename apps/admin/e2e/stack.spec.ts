import { test as base, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { adminApi, apiLogin, login, publicGet, stackReady } from './stack';

// 07 §9: local seeded backend (make e2e-up). Skip the suite when the E2E stack is unavailable.
// Backend login is IP-limited (10/minute, 03 §15): run serially, log superadmin in once, reuse the session.
const test = base.extend<{ superPage: Page }, { superState: string }>({
  superState: [
    async ({ browser }, use, workerInfo) => {
      const context = await browser.newContext();
      await login(await context.newPage());
      const file = `${workerInfo.project.outputDir}/super-state-${workerInfo.workerIndex}.json`;
      await context.storageState({ path: file });
      await context.close();
      await use(file);
    },
    { scope: 'worker' }
  ],
  superPage: async ({ browser, superState }, use) => {
    const context = await browser.newContext({ storageState: superState });
    await use(await context.newPage());
    await context.close();
  }
});

test.describe.configure({ mode: 'serial' });

test.beforeAll(async () => {
  test.skip(!(await stackReady()), 'E2E stack is not running (make e2e-up)');
});

test('Superadmin dashboard shows scale and coverage', async ({ superPage: page }) => {
  await page.goto('/dashboard');
  await expect(page.getByText('App 总数', { exact: true })).toBeVisible();
  await expect(page.getByText('覆盖率')).toBeVisible();
  // The dashboard now exposes SEO automation instead of the removed LLM usage chart.
  await expect(page.getByText('SEO 自动化（Hermes）', { exact: true })).toBeVisible();
});

test('Editing a package Chinese summary updates the public API', async ({ superPage: page }) => {
  test.setTimeout(60_000);
  await page.goto('/catalog/package');
  await page.getByPlaceholder('token 或名称（任意语言）').fill('obsidian');
  await page.getByRole('button', { name: '搜索' }).click();
  await expect(page.locator('.n-data-table-tbody .n-data-table-tr')).toHaveCount(1);
  await page.getByRole('button', { name: '编辑', exact: true }).click();
  await page.waitForURL('**/catalog/package-detail/**');
  await page.locator('.n-tabs-tab', { hasText: '文字与翻译' }).click();
  // Six-language tabs open in the source language; explicitly switch to Simplified Chinese here.
  await page.locator('.n-tabs-tab', { hasText: '简体中文' }).click();

  const column = page.locator('[data-locale="zh-CN"]');
  const summary = column.getByRole('textbox').nth(1);
  const original = await summary.inputValue();
  const next = `E2E 简介 ${Date.now() % 100000}`;
  async function saveSummary(value: string) {
    await summary.fill(value);
    const saved = page.waitForResponse(
      response => response.request().method() === 'PUT' && new URL(response.url()).pathname.endsWith('/i18n/zh-CN')
    );
    await column.getByRole('button', { name: '保存', exact: true }).click();
    const response = await saved;
    expect(response.ok()).toBe(true);
    expect((await response.json()).code).toBe('0000');
    await expect(page.getByText('已保存', { exact: true })).toBeVisible();
    // Consecutive writes share a 10-second cache invalidation window, including seed restoration.
    await expect
      .poll(async () => (await publicGet<{ summary: string }>('/packages/cask/obsidian?locale=zh-CN')).summary, {
        timeout: 15_000,
        message: 'Public summary should converge after cache invalidation'
      })
      .toBe(value);
  }

  try {
    await saveSummary(next);
  } finally {
    // Saving reloads details and returns to the source tab; reopen Chinese before restoring seed data.
    await page.reload();
    await page.locator('.n-tabs-tab', { hasText: '文字与翻译' }).click();
    await page.locator('.n-tabs-tab', { hasText: '简体中文' }).click();
    await saveSummary(original);
  }
});

test('Translation reviewer cannot see System or save basic package details', async ({ page }) => {
  await login(page, 'e2e-reviewer');
  const menu = page.locator('.n-menu').first();
  await expect(menu.getByText('目录')).toBeVisible();
  await expect(menu.getByText('系统')).toHaveCount(0);
  await page.goto('/catalog/package');
  await page.getByRole('button', { name: '编辑', exact: true }).first().click();
  await page.waitForURL('**/catalog/package-detail/**');
  await expect(page.getByText('编辑信息')).toBeVisible();
  await expect(page.getByRole('button', { name: '保存', exact: true })).toHaveCount(0);
  // After removing review (12), this role corrects translations: non-source tabs allow saving; source requires catalog:package:edit and is read-only here.
  await page.locator('.n-tabs-tab', { hasText: '文字与翻译' }).click();
  await page.locator('.n-tabs-tab', { hasText: '日本語' }).click();
  await expect(page.locator('[data-locale="ja-JP"]').getByRole('button', { name: '保存', exact: true })).toBeVisible();
});

test('Password changes prompt old sessions to log in again (7778)', async ({ browser }) => {
  const superToken = await apiLogin();
  const userName = `e2e-pw-${Date.now() % 1_000_000}`;
  const first = 'first-password-0001';
  const second = 'second-password-0002';
  const created = await adminApi<{ id: number }>(superToken, 'POST', '/system/users', {
    userName,
    password: first,
    nickName: userName,
    email: null,
    status: '1',
    roles: ['R_EDITOR']
  });
  const oldSession = await browser.newContext();
  const newSession = await browser.newContext();
  try {
    const oldPage = await oldSession.newPage();
    await login(oldPage, userName, first);
    const newPage = await newSession.newPage();
    await login(newPage, userName, first);

    await newPage.goto('/user-center');
    await newPage.getByPlaceholder('请输入').first().waitFor();
    const inputs = newPage.locator('input[type="password"]');
    await inputs.nth(0).fill(first);
    await inputs.nth(1).fill(second);
    await inputs.nth(2).fill(second);
    await newPage.getByRole('button', { name: '修改密码', exact: true }).click();
    await newPage.waitForURL('**/login**');

    // Reject the next request from an unrefreshed old session; show a nondismissible notice, then return to login on confirmation.
    const menu = oldPage.locator('.n-menu').first();
    await menu.getByText('目录').click();
    await menu.getByText('包管理').click();
    const dialog = oldPage.locator('.n-dialog');
    await expect(dialog).toBeVisible();
    await dialog.getByRole('button', { name: '确认' }).click();
    await oldPage.waitForURL('**/login**');
  } finally {
    await oldSession.close();
    await newSession.close();
    await adminApi(superToken, 'DELETE', `/system/users/${created.id}`);
  }
});

// Requires short-lived E2E access tokens (E2E_JWT_ACCESS_TTL=5s make e2e-up, 03 §5.5).
const accessTtl = Number(process.env.E2E_ACCESS_TTL_SECONDS ?? 0);
test('Expired access tokens refresh automatically without interrupting operations', async ({ page }) => {
  test.skip(!accessTtl, 'Requires E2E_ACCESS_TTL_SECONDS and a short-lived-token E2E stack');
  await login(page, 'e2e-admin');
  await page.waitForTimeout((accessTtl + 1) * 1000);
  // Confirm refresh actually occurred instead of the token simply remaining valid.
  const refreshed = page.waitForRequest(request => request.url().includes('/auth/refreshToken'));
  await page.goto('/catalog/package');
  await refreshed;
  await expect(page.locator('.n-data-table-tbody .n-data-table-tr').first()).toBeVisible();
  await expect(page).toHaveURL(/catalog\/package/);
});

test('Create a collection, add three packages, publish, and read it publicly', async ({ superPage: page }) => {
  const slug = `e2e-col-${Date.now() % 1_000_000}`;
  let createdId: number | null = null;
  try {
    await page.goto('/content/collection');
    await page.getByRole('button', { name: '新建合集' }).click();
    await page.waitForURL('**/collection-detail/new');
    // slug is limited to 64 characters and source title to 40 (CollectionUpsert); new content defaults to English, with other languages translated by AI.
    await page.locator('input[maxlength="64"]').fill(slug);
    await page.locator('[data-locale="en-US"] input[maxlength="40"]').fill(`E2E collection ${slug}`);

    const picker = page.locator('[data-picker="collection-items"]');
    // Cask-only catalog (ADR-018): use seeded apps for collection entries.
    for (const token of ['firefox', 'iterm2', 'raycast']) {
      await picker.click();
      await picker.locator('input').fill(token);
      await page
        .locator('.n-base-select-option', { hasText: `· ${token}` })
        .first()
        .click();
      await page.keyboard.press('Escape');
    }
    await expect(page.locator('[data-token]')).toHaveCount(3);

    await page.getByRole('button', { name: '保存', exact: true }).click();
    await page.waitForURL(url => /collection-detail\/\d+$/.test(url.pathname));
    createdId = Number(new URL(page.url()).pathname.split('/').pop());
    await page.getByRole('button', { name: '立即发布' }).click();
    await page.locator('.n-dialog').getByRole('button', { name: '确认' }).click();
    await expect(page.locator('.n-tag', { hasText: '已发布' })).toBeVisible();

    await expect
      .poll(async () =>
        (await publicGet<{ records: { slug: string }[] }>('/collections?size=100')).records.map(item => item.slug)
      )
      .toContain(slug);
    const detail = await publicGet<{ items: unknown[] }>(`/collections/${slug}`);
    expect(detail.items).toHaveLength(3);
  } finally {
    // Cleanup: unpublish and delete with the page's existing token, without another login.
    if (createdId) {
      const token = JSON.parse((await page.evaluate(() => localStorage.getItem('ONV_ADMIN_token'))) ?? 'null') as
        | string
        | null;
      await adminApi(token, 'POST', `/collections/${createdId}/unpublish`).catch(() => undefined);
      await adminApi(token, 'DELETE', `/collections/${createdId}`);
    }
  }
});

test('Translation management has no batch approval; compatibility marks machine text manual in the public API', async ({
  superPage: page
}) => {
  // 12 removes review (approved change #8): translation management replaces batch approval; /translations/approve remains deprecated for compatibility.
  // Compatibility marks existing Chinese translations manually corrected; queries/rereads explicitly select Chinese to avoid reading English source by default (04 §9.1).
  await page.goto('/changelog/review');
  await expect(page.getByRole('button', { name: '批量通过' })).toHaveCount(0);
  const token = JSON.parse((await page.evaluate(() => localStorage.getItem('ONV_ADMIN_token'))) ?? 'null') as
    | string
    | null;
  const queue = await adminApi<{ records: { id: number; kind: string; token: string; version: string | null }[] }>(
    token,
    'GET',
    '/translations/queue?type=release&status=machine&locale=zh-CN&current=1&size=20'
  );
  const item = queue.records[0];
  // After marking, text is no longer machine-translated; run make e2e-reset when machine-translation seeds are exhausted.
  test.skip(!item?.version, 'No version with machine translation; make e2e-reset restores seeds');
  const target = item!;

  await adminApi(token, 'POST', '/translations/approve', { type: 'release', ids: [target.id] });

  await expect
    .poll(async () => {
      const releases = await publicGet<{ records: { version: string; translation: { status: string } }[] }>(
        `/packages/${target.kind}/${target.token}/releases?size=50&locale=zh-CN`
      );
      return releases.records.find(release => release.version === target.version)?.translation.status;
    })
    .toBe('manual');
});
