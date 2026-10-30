// @vitest-environment jsdom
import { computed, createSSRApp, h, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { PackagePage } from '../app/utils/packageFacts';
import { siteMessage } from '../server/utils/siteCopy';
import PackageStats from '../app/components/package/PackageStats.vue';
import production from './fixtures/packageFaq.production.json';

const t = (key: string, named?: Record<string, unknown>, plural?: number) =>
  siteMessage('en-US', key, named as Record<string, string | number>, plural);

beforeEach(() => {
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useI18n', () => ({ t, locale: ref('en') }));
  vi.stubGlobal('toAppLocale', () => 'en-US');
  vi.stubGlobal('useUiMessages', () => ({ value: { kind: {} } }));
});
afterEach(() => vi.unstubAllGlobals());

async function render(value: PackagePage): Promise<HTMLElement> {
  const html = await renderToString(createSSRApp({ render: () => h(PackageStats, { pkg: value }) }));
  const page = document.createElement('div');
  page.innerHTML = html;
  return page;
}

it('shows the download file type and size in the stats hint', async () => {
  const value = structuredClone(production.packages.iterm2) as unknown as PackagePage;
  value.downloadSize = 6_100_000;
  value.facts.overview.downloadUrl = 'https://downloads.example.com/iterm2.dmg';
  const page = await render(value);
  expect(page.textContent).toContain('6.1');
  expect(page.textContent).toContain('MB');
  expect(page.textContent).toContain('dmg');
});

it('omits the file-type hint when the download URL has no extension', async () => {
  const value = structuredClone(production.packages.iterm2) as unknown as PackagePage;
  value.downloadSize = 0;
  value.facts.overview.downloadUrl = null;
  const page = await render(value);
  expect(page.textContent).not.toContain('dmg');
});
