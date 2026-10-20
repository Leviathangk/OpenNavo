import { expect, test } from '@playwright/test';
import { gotoReady } from './helpers';

// 10 §4 M2-03: vscode search ranks Visual Studio Code first; assert token because seed-e2e has a Chinese display name.
test('vscode search ranks Visual Studio Code first', async ({ page }) => {
  await gotoReady(page, '/zh/search?q=vscode');
  const first = page.getByRole('listitem').first().getByRole('link').first();
  await expect(first).toHaveAttribute('href', '/zh/apps/visual-studio-code');
  await expect(page).toHaveTitle(/vscode/);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', 'noindex, follow');
});

// The Go to group comes first and starts with all the results for the text, so Enter goes to them.
test('Command-K vscode then Enter goes to all the results', async ({ page }) => {
  await gotoReady(page, '/zh');
  await page.keyboard.press('ControlOrMeta+K');
  const input = page.getByRole('combobox');
  await expect(input).toBeFocused();
  await input.fill('vscode');
  await expect(page.getByRole('option').first()).toContainText('查看「vscode」的全部结果');
  await expect(
    page
      .getByRole('option')
      .filter({ hasText: /VS Code|Visual Studio Code/ })
      .first()
  ).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/search\?q=vscode$/);
});

test('Command-K vscode then Down and Enter opens Visual Studio Code details', async ({ page }) => {
  await gotoReady(page, '/zh');
  await page.keyboard.press('ControlOrMeta+K');
  const input = page.getByRole('combobox');
  await expect(input).toBeFocused();
  await input.fill('vscode');
  const app = page
    .getByRole('option')
    .filter({ hasText: /VS Code|Visual Studio Code/ })
    .first();
  await expect(app).toBeVisible();
  for (let step = 0; step < 6 && (await app.getAttribute('aria-selected')) !== 'true'; step++)
    await page.keyboard.press('ArrowDown');
  await expect(app).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/\/apps\/visual-studio-code$/);
});

test('Zero results show empty state and listing suggestion', async ({ page }) => {
  await gotoReady(page, '/zh/search?q=zzqxjv-not-a-package');
  await expect(page.getByRole('heading', { name: /没有找到/ })).toBeVisible();
  await expect(page.getByRole('link', { name: '提交建议收录' })).toHaveAttribute(
    'href',
    /\/feedback\?type=suggest_package/
  );
});
