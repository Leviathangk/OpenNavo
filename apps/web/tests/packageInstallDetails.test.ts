// @vitest-environment jsdom
import { computed, createSSRApp, h, ref, resolveComponent } from 'vue';
import type { Component } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import PackageFaq from '../app/components/package/PackageFaq.vue';
import PackageCommand from '../app/components/package/PackageCommand.vue';
import PackageInstall from '../app/components/package/PackageInstall.vue';
import PackageInstallDetailsContent from '../app/components/package/PackageInstallDetailsContent.vue';
import PackageUninstall from '../app/components/package/PackageUninstall.vue';
import type { PackagePage } from '../app/utils/packageFacts';
import { siteMessage } from '../server/utils/siteCopy';
import production from './fixtures/packageFaq.production.json';

const t = (key: string, named?: Record<string, unknown>) =>
  siteMessage('en-US', key, named as Record<string, string | number>);
const pkg = () => {
  const value = structuredClone(production.packages.iterm2) as unknown as PackagePage;
  // The production fixture predates the formulae.brew.sh link the real API always sends.
  value.formulaeUrl = `https://formulae.brew.sh/cask/${value.token}`;
  return value;
};

beforeEach(() => {
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('resolveComponent', resolveComponent);
  vi.stubGlobal('useI18n', () => ({ t, locale: ref('en-US') }));
  vi.stubGlobal('useLocalePath', () => (path: string) => path);
  vi.stubGlobal('useToasts', () => ({ push: vi.fn() }));
  vi.stubGlobal('useClientHandoff', () => ({ openUrl: vi.fn() }));
  vi.stubGlobal('toAppLocale', () => 'en-US');
});
afterEach(() => vi.unstubAllGlobals());

async function render(component: Component, props: Record<string, unknown>) {
  const app = createSSRApp({ render: () => h(component, props) });
  app.component('NuxtLink', {
    props: ['to'],
    setup:
      (linkProps, { slots }) =>
      () =>
        h('a', { href: linkProps.to }, slots.default?.())
  });
  // PackageInstall relies on Nuxt auto-imports for these; register them for unit tests.
  app.component('PackageCommand', PackageCommand);
  app.component('PackageUninstall', PackageUninstall);
  const page = document.createElement('div');
  page.innerHTML = await renderToString(app);
  return page;
}

it('does not link the redirected details tab and keeps the body same-page reachable', async () => {
  const value = pkg();
  value.indexing = { detailsRedirect: true } as PackagePage['indexing'];
  const page = await render(PackageInstall, { pkg: value });
  const details = [...page.querySelectorAll('a')].find(link => link.textContent?.includes('Install Details'));
  expect(details?.getAttribute('href')).toBe('#install-details');
  expect(page.innerHTML).not.toContain('/apps/iterm2/details');
});

it('keeps linking install details when the server does not redirect it', async () => {
  const page = await render(PackageInstall, { pkg: pkg() });
  const details = [...page.querySelectorAll('a')].find(link => link.textContent?.includes('Install Details'));
  expect(details?.getAttribute('href')).toBe('/apps/iterm2/details#requirements');
});

it('renders requirements, downloads, checksums and caveats in the shared details body', async () => {
  const value = pkg();
  value.facts = {
    ...value.facts,
    overview: { ...value.facts.overview, caveats: 'Back up your settings before updating.' }
  };
  const page = await render(PackageInstallDetailsContent, { pkg: value });
  expect(page.querySelector('[data-testid="package-install-details"]')).not.toBeNull();
  const requirements = page.querySelector('[data-testid="package-requirements"]');
  expect(requirements?.textContent).toContain('iterm2.com/downloads');
  expect(requirements?.textContent).toContain(value.facts.overview.sha256!.slice(0, 12));
  expect(page.textContent).toContain('Back up your settings before updating.');
});

it('keeps a summary download and checksum visible when no requirements row has them', async () => {
  const value = pkg();
  value.facts = { ...value.facts, requirements: [] };
  const page = await render(PackageInstallDetailsContent, { pkg: value });
  expect(page.textContent).toContain(value.facts.overview.downloadUrl!);
  expect(page.textContent).toContain(value.facts.overview.sha256!);
});

it.each([
  {
    name: 'disabled',
    fields: { homebrewStatus: { state: 'disabled', reason: 'discontinued' }, disabled: false },
    command: false,
    notice: 'Homebrew has disabled this package: discontinued'
  },
  {
    name: 'deprecated',
    fields: { homebrewStatus: { state: 'deprecated', reason: 'unmaintained' }, disabled: false },
    command: true,
    notice: 'Homebrew has deprecated this package: unmaintained'
  },
  {
    name: 'paused',
    fields: { homebrewStatus: { state: 'active', reason: null }, disabled: true, disable: { reason: 'stale' } },
    command: true,
    notice: 'No new version for more than 90 days'
  }
])('applies the overview lifecycle guard to the details command: $name', async ({ fields, command, notice }) => {
  const value = { ...pkg(), ...fields } as PackagePage;
  const page = await render(PackageInstallDetailsContent, { pkg: value });
  expect(page.textContent).toContain(notice);
  expect(page.textContent?.includes('brew install --cask iterm2')).toBe(command);
  expect(page.querySelector('[data-testid="package-install-notice"]')).not.toBeNull();
});

// On mobile (#69) the install command + lifecycle notice live in PackageHeader, not PackageInstall.
// These cases still cover the #66 product fix: disabled packages must not mount uninstall / Deep clean.
it('hides uninstall and Deep clean on the overview when Homebrew disabled the package', async () => {
  const value = {
    ...pkg(),
    homebrewStatus: { state: 'disabled', reason: 'discontinued', date: null },
    disabled: true
  } as PackagePage;
  const page = await render(PackageInstall, { pkg: value });
  expect(page.querySelector('[data-testid="package-uninstall"]')).toBeNull();
  expect(page.querySelector('[data-testid="package-zap"]')).toBeNull();
  expect(page.textContent?.includes('brew uninstall')).toBe(false);
  expect(page.textContent?.includes('brew install --cask iterm2')).toBe(false);
});

it('keeps uninstall and Deep clean on the overview for an active package', async () => {
  const page = await render(PackageInstall, { pkg: pkg() });
  expect(page.querySelector('[data-testid="package-uninstall"]')).not.toBeNull();
  expect(page.querySelector('[data-testid="package-zap"]')).not.toBeNull();
  expect(page.textContent).toContain('brew uninstall --cask iterm2');
});

it('hides uninstall and Deep clean in the details body when disabled', async () => {
  const value = {
    ...pkg(),
    homebrewStatus: { state: 'disabled', reason: 'discontinued', date: null },
    disabled: true
  } as PackagePage;
  const page = await render(PackageInstallDetailsContent, { pkg: value });
  expect(page.querySelector('[data-testid="package-uninstall"]')).toBeNull();
  expect(page.querySelector('[data-testid="package-zap"]')).toBeNull();
  expect(page.textContent?.includes('brew uninstall')).toBe(false);
});

it('renders no brew uninstall or --zap in the SSR FAQ when disabled', async () => {
  const active = await render(PackageFaq, { pkg: pkg() });
  expect(active.textContent).toContain('brew uninstall --cask iterm2');
  const value = {
    ...pkg(),
    homebrewStatus: { state: 'disabled', reason: 'discontinued', date: null },
    disabled: true
  } as PackagePage;
  const page = await render(PackageFaq, { pkg: value });
  expect(page.textContent).not.toMatch(/\bbrew (uninstall|upgrade|install)\b|--zap/);
});
