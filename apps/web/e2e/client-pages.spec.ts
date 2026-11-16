import { expect, test } from '@playwright/test';
import type { Page } from '@playwright/test';
import { gotoReady } from './helpers';

async function stubDeepLink(page: Page) {
  await page.addInitScript(() => {
    window.opennavoLaunchDeepLink = () => {};
  });
}

test('Mac app pages explain the client and offer the download', async ({ page }) => {
  await gotoReady(page, '/monitor');
  await expect(page.getByRole('heading', { name: 'Monitor', exact: true })).toBeVisible();
  await expect(page.getByText('Mac app required', { exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Download for macOS', exact: true })).toHaveAttribute(
    'href',
    '/download'
  );
  await expect(page.getByText('Runs locally. Nothing is uploaded.')).toBeVisible();
});

test('The sidebar lists every Mac app destination', async ({ page }) => {
  await gotoReady(page, '/discover');
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  for (const name of ['Installed', 'Updates', 'Clean Up', 'Monitor', 'Security', 'Firewall']) {
    await expect(nav.getByRole('link', { name, exact: true })).toBeVisible();
  }
});

test('A missing client shows the shared handoff dialog', async ({ page }) => {
  await stubDeepLink(page);
  await gotoReady(page, '/monitor');
  await page.getByRole('button', { name: 'Open in OpenNavo' }).click();
  const dialog = page.getByRole('dialog', { name: 'OpenNavo for Mac is needed' });
  await expect(dialog).toBeVisible({ timeout: 4000 });
  await expect(dialog.getByRole('link', { name: 'Download for macOS' })).toHaveAttribute('href', '/download');
});

test.describe('on a phone', () => {
  test.use({
    userAgent:
      'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1'
  });

  test('Shows how to continue on a Mac instead of a DMG download', async ({ page }) => {
    await gotoReady(page, '/monitor');
    await expect(page.getByText('This page describes the macOS app. Open it on your Mac to continue.')).toBeVisible();
  });
});
