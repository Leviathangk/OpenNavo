import { expect, test } from '@playwright/test';
import { loginAs } from './fixtures';

// Developer submission review: the drawer exposes the receipt token and publishing
// sends the status the reviewer picked.
const TOKEN = 'example-receipt-token-0000000002';

function submission(overrides: Record<string, unknown> = {}) {
  return {
    id: 7,
    publicToken: TOKEN,
    kind: 'new',
    status: 'submitted',
    sourceKind: 'github',
    sourceUrl: 'https://github.com/example/hyperfocus',
    packageId: null,
    payload: { name: 'Hyperfocus Timer', summary: 'A tiny focus timer for macOS.' },
    contactEmail: 'dev@example.com',
    proof: '',
    reviewerId: null,
    decisionNote: null,
    createdAt: '2026-11-11T10:00:00Z',
    updatedAt: '2026-11-11T10:00:00Z',
    decidedAt: null,
    ...overrides
  };
}

function pick(overrides: Record<string, unknown> = {}) {
  return {
    packageId: 42,
    kind: 'link',
    token: 'hyperfocus-timer',
    name: 'Hyperfocus Timer',
    iconUrl: null,
    quote: 'A calm timer.',
    sort: 3,
    active: true,
    publishedAt: '2026-11-11T10:00:00Z',
    submissionId: 7,
    ...overrides
  };
}

async function mockPicks(page: Parameters<typeof loginAs>[0], picks: Record<string, unknown>[]) {
  await page.route('**/proxy-default/developer/picks', async route =>
    route.fulfill({ json: { code: '0000', msg: 'ok', data: picks } })
  );
}

test('a reviewer publishes a submission and sees its receipt token', async ({ page }) => {
  await loginAs(page, 'R_SUPER');
  await mockPicks(page, []);

  let patched: Record<string, unknown> | null = null;
  await page.route('**/proxy-default/developer/submissions**', async route => {
    const request = route.request();
    if (request.method() === 'PATCH') {
      patched = JSON.parse(request.postData() ?? '{}');
      return route.fulfill({ json: { code: '0000', msg: 'ok', data: submission({ status: patched?.status, packageId: 42 }) } });
    }
    return route.fulfill({ json: { code: '0000', msg: 'ok', data: [submission()] } });
  });

  await page.goto('/developer/review');
  await expect(page.getByText('dev@example.com')).toBeVisible();

  await page.getByRole('button', { name: '审核' }).click();
  await expect(page.getByText('查询码:')).toBeVisible();
  await expect(page.getByText(TOKEN)).toBeVisible();
  await expect(page.getByText('类型:')).toBeVisible();

  await page.getByRole('button', { name: '发布' }).click();
  await expect.poll(() => patched?.status).toBe('published');
});

// Re-saving a published submission must keep the featured quote and order instead of
// clearing them.
test('re-saving a published submission keeps its pick', async ({ page }) => {
  await loginAs(page, 'R_SUPER');
  await mockPicks(page, [pick()]);

  let patched: Record<string, unknown> | null = null;
  await page.route('**/proxy-default/developer/submissions**', async route => {
    const request = route.request();
    const published = submission({ status: 'published', packageId: 42 });
    if (request.method() === 'PATCH') {
      patched = JSON.parse(request.postData() ?? '{}');
      return route.fulfill({ json: { code: '0000', msg: 'ok', data: published } });
    }
    return route.fulfill({ json: { code: '0000', msg: 'ok', data: [published] } });
  });

  await page.goto('/developer/review');
  await page.getByRole('button', { name: '审核' }).click();

  await page.getByRole('button', { name: '发布' }).click();
  await expect.poll(() => patched?.quote).toBe('A calm timer.');
  await expect.poll(() => patched?.sort).toBe(3);
  await expect.poll(() => patched?.feature).toBe(true);
});

// A rejected review keeps the drawer open with the reason, so the editor can fix it.
test('a rejected save shows the reason in the drawer', async ({ page }) => {
  await loginAs(page, 'R_SUPER');
  await mockPicks(page, []);

  await page.route('**/proxy-default/developer/submissions**', async route => {
    const request = route.request();
    if (request.method() === 'PATCH') {
      return route.fulfill({
        json: { code: '1001', msg: 'Validation failed (city_not_found:US)', data: null },
        status: 200
      });
    }
    return route.fulfill({ json: { code: '0000', msg: 'ok', data: [submission()] } });
  });

  await page.goto('/developer/review');
  await page.getByRole('button', { name: '审核' }).click();
  await page.getByRole('button', { name: '发布' }).click();

  await expect(page.getByRole('alert').getByText('Validation failed (city_not_found:US)')).toBeVisible();
  await expect(page.getByText(TOKEN)).toBeVisible();
});

// A pick curated on the picks page belongs to no submission, so the drawer must not
// prefill it and publishing must not hand it to this submission.
test('an unowned pick is not handed to the submission', async ({ page }) => {
  await loginAs(page, 'R_SUPER');
  await mockPicks(page, [pick({ submissionId: null })]);

  let patched: Record<string, unknown> | null = null;
  await page.route('**/proxy-default/developer/submissions**', async route => {
    const request = route.request();
    const published = submission({ status: 'published', packageId: 42 });
    if (request.method() === 'PATCH') {
      patched = JSON.parse(request.postData() ?? '{}');
      return route.fulfill({ json: { code: '0000', msg: 'ok', data: published } });
    }
    return route.fulfill({ json: { code: '0000', msg: 'ok', data: [published] } });
  });

  await page.goto('/developer/review');
  await page.getByRole('button', { name: '审核' }).click();
  await page.getByRole('button', { name: '发布' }).click();

  await expect.poll(() => patched?.feature).toBe(false);
});
