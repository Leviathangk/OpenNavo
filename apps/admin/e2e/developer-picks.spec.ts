import { expect, test } from '@playwright/test';
import { loginAs } from './fixtures';

const pick = (overrides: Record<string, unknown> = {}) => ({
  packageId: 42,
  kind: 'link',
  token: 'orbit-notes',
  name: 'Orbit Notes',
  iconUrl: null,
  quote: 'Notes, but lighter.',
  sort: 2,
  active: true,
  publishedAt: '2026-11-11T10:00:00Z',
  submissionId: 11,
  ...overrides
});

test('a picker reorders and hides a developer pick', async ({ page }) => {
  await loginAs(page, 'R_SUPER');

  let saved: Record<string, unknown> | null = null;
  await page.route('**/proxy-default/developer/picks', async route => {
    const request = route.request();
    if (request.method() === 'POST') {
      saved = JSON.parse(request.postData() ?? '{}');
      return route.fulfill({ json: { code: '0000', msg: 'ok', data: { packageId: 42, active: false } } });
    }
    return route.fulfill({ json: { code: '0000', msg: 'ok', data: [pick()] } });
  });

  await page.goto('/developer/picks');
  await expect(page.getByText('Orbit Notes')).toBeVisible();
  await expect(page.getByText('link/orbit-notes')).toBeVisible();

  await page.getByRole('switch').first().click();
  await page.getByRole('button', { name: '保存' }).click();

  await expect.poll(() => saved?.active).toBe(false);
  await expect.poll(() => saved?.packageToken).toBe('orbit-notes');
});
