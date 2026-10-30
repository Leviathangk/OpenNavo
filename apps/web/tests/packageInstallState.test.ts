// @vitest-environment jsdom
import { computed, createSSRApp, h, ref, resolveComponent } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { OnPackageSpecCard } from '@opennavo/ui';
import lifecycleCases from '../../../packages/shared/tests/fixtures/packageInstallStates.json';
import Header from '../app/components/package/PackageHeader.vue';
import type { PackagePage } from '../app/utils/packageFacts';
import { packageSpec } from '../app/utils/packageSpec';
import { siteMessage } from '../server/utils/siteCopy';
import production from './fixtures/packageFaq.production.json';

const t = (key: string, named?: Record<string, unknown>) =>
  siteMessage('en-US', key, named as Record<string, string | number>);
const statusLabels = { disabled: 'Disabled', deprecated: 'Deprecated', paused: 'Paused by OpenNavo' };
const homebrewLabels = {
  active: 'Available; the app updates itself',
  disabled: 'Disabled by Homebrew',
  deprecated: 'Deprecated',
  paused: 'Still offered by Homebrew; paused by OpenNavo'
};

beforeEach(() => {
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('resolveComponent', resolveComponent);
  vi.stubGlobal('useI18n', () => ({ t, locale: ref('en') }));
  vi.stubGlobal('toAppLocale', () => 'en-US');
  vi.stubGlobal('useLocalePath', () => (path: string) => path);
});
afterEach(() => vi.unstubAllGlobals());

it.each(lifecycleCases)(
  'web header chips and Homebrew spec row share lifecycle priority: $name',
  async ({ fields, expected }) => {
    const pkg = {
      ...production.packages.iterm2,
      autoUpdates: true,
      disabled: false,
      deprecated: false,
      disable: null,
      deprecation: null,
      recommendationPaused: false,
      homebrewStatus: null,
      ...fields
    } as unknown as PackagePage;
    const spec = packageSpec(pkg, t, 'en-US', Date.parse('2026-10-30T00:00:00Z'));
    const app = createSSRApp({
      render: () => h('div', [h(Header, { pkg }), h(OnPackageSpecCard, { rows: spec.rows })])
    });
    app.component('NuxtLink', {
      props: ['to'],
      setup:
        (props, { slots }) =>
        () =>
          h('a', { href: props.to }, slots.default?.())
    });
    app.component('GetMenu', { render: () => h('button', 'Get') });
    const page = document.createElement('div');
    page.innerHTML = await renderToString(app);
    const header = page.querySelector('h1')!.parentElement!;
    for (const [status, label] of Object.entries(statusLabels))
      expect(header.textContent?.includes(label), status).toBe(expected === status);
    const card = page.querySelector('[data-testid="package-spec-card"]')!;
    const homebrew = [...card.querySelectorAll('dt')].find(row => row.textContent === 'Homebrew');
    expect(homebrew?.nextElementSibling?.textContent?.trim()).toBe(
      homebrewLabels[expected as keyof typeof homebrewLabels]
    );
  }
);
