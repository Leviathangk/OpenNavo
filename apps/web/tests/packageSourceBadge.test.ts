// @vitest-environment jsdom
import { computed, createApp, createSSRApp, h, nextTick, ref, resolveComponent } from 'vue';
import type { App } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { SITE_LOCALES } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import Header from '../app/components/package/PackageHeader.vue';
import GetMenu from '../app/components/package/GetMenu.vue';
import { officialSource } from '../app/utils/packageFacts';
import type { PackagePage } from '../app/utils/packageFacts';
import { siteMessage } from '../server/utils/siteCopy';
import lifecycleCases from '../../../packages/shared/tests/fixtures/packageInstallStates.json';
import production from './fixtures/packageFaq.production.json';

let locale: SiteLocale = 'en-US';
let mounted: App | undefined;
const pkg = () => structuredClone(production.packages.iterm2) as unknown as PackagePage;
const translate = (key: string, named?: Record<string, unknown>) =>
  siteMessage(locale, key, named as Record<string, string | number>);

beforeEach(() => {
  locale = 'en-US';
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('resolveComponent', resolveComponent);
  vi.stubGlobal('useI18n', () => ({ t: translate, locale: ref(locale) }));
  vi.stubGlobal('toAppLocale', () => locale);
  vi.stubGlobal('useLocalePath', () => (path: string) => path);
  vi.stubGlobal('useToasts', () => ({ push: vi.fn() }));
  vi.stubGlobal('useOpenInApp', () => ({ open: vi.fn() }));
  vi.stubGlobal('useCopyCommand', () => ({ copy: vi.fn() }));
  vi.stubGlobal('useBrewfileList', () => ({ has: () => false, add: () => 'added' }));
});
afterEach(() => {
  mounted?.unmount();
  mounted = undefined;
  document.body.innerHTML = '';
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function components(app: App) {
  app.component('GetMenu', GetMenu);
  app.component('NuxtLink', {
    props: ['to'],
    setup:
      (props, { slots }) =>
      () =>
        h('a', { href: props.to }, slots.default?.())
  });
  return app;
}
async function render(value = pkg()) {
  const context: { teleports?: Record<string, string> } = {};
  const html = await renderToString(components(createSSRApp({ render: () => h(Header, { pkg: value }) })), context);
  const page = document.createElement('div');
  page.innerHTML = html + (context.teleports?.body ?? '');
  return page;
}

it('keeps Get and Homepage together and places the source badge beside the header tags', async () => {
  const page = await render();
  const actions = page.querySelector('[data-testid="package-header-actions"]')!;
  expect(actions.children).toHaveLength(2);
  expect(actions.querySelector('button')?.getAttribute('aria-label')).toBe('Get iTerm2');
  expect(actions.querySelector('a')?.textContent).toContain('Homepage');
  const badge = page.querySelector('[data-testid="package-source-badge"]')!;
  expect(badge.closest('[data-testid="package-header-tags"]')).not.toBeNull();
  expect(actions.contains(badge)).toBe(false);
  expect(page.querySelector('section')?.textContent).not.toContain('From homebrew/cask');
  expect(actions.textContent).not.toMatch(/Official download|SHA-256|Checked/);
});

it('renders a keyboard-accessible source trigger and its full description in SSR HTML', async () => {
  const value = pkg();
  const page = await render(value);
  const badge = page.querySelector<HTMLButtonElement>('[data-testid="package-source-badge"]')!;
  expect(badge.tagName).toBe('BUTTON');
  expect(badge.type).toBe('button');
  expect(badge.getAttribute('aria-label')).toBe('Official source · Verified');
  const id = badge.getAttribute('aria-describedby');
  expect(id).toBeTruthy();
  const tooltip = page.querySelector(`[id="${id}"][role="tooltip"]`)!;
  expect(tooltip).not.toBeNull();
  expect(tooltip.textContent).toContain('Source: iterm2.com');
  expect(tooltip.textContent).toContain(value.facts.requirements[0]!.sha256);
  expect(tooltip.textContent).toContain('Not disabled by Homebrew');
  expect(tooltip.textContent).toMatch(/Checked:.*2026/);
});

it.each(lifecycleCases)('source badge follows shared lifecycle priority: $name', async ({ fields, expected }) => {
  const value = Object.assign(
    pkg(),
    {
      disabled: false,
      deprecated: false,
      disable: null,
      deprecation: null,
      recommendationPaused: false,
      homebrewStatus: null
    },
    fields
  );
  const page = await render(value);
  const badge = page.querySelector('[data-testid="package-source-badge"]')!;
  expect(badge.getAttribute('data-state')).toBe(expected);
  expect(badge.textContent).toBe(translate(`package.sourceBadge.${expected === 'active' ? 'verified' : expected}`));
  expect(
    badge
      .querySelector('span')
      ?.classList.contains(
        expected === 'disabled'
          ? 'text-status-danger'
          : expected === 'active'
            ? 'text-status-success'
            : 'text-status-warning'
      )
  ).toBe(true);
  if (expected === 'disabled') {
    expect(page.querySelector('[data-testid="package-header-actions"] button')?.hasAttribute('disabled')).toBe(true);
    expect(page.querySelector(`[id="${badge.getAttribute('aria-describedby')}"]`)?.textContent).toContain(
      'Homebrew has disabled this package:'
    );
  }
});

it.each(SITE_LOCALES)('uses real source-badge messages without fallback in %s', async code => {
  locale = code;
  const page = await render();
  const badge = page.querySelector('[data-testid="package-source-badge"]')!;
  expect(badge.textContent).toBe(translate('package.sourceBadge.verified'));
  expect(page.textContent).not.toMatch(/package\.sourceBadge\.|\{domain\}|\{sha256\}|\{date\}/);
  expect(page.querySelector(`[id="${badge.getAttribute('aria-describedby')}"]`)?.textContent).toContain('2026');
});

it.each(['missing checksum', 'missing check date', 'stale evidence', 'invalid check date'])(
  'does not claim verification with %s',
  async reason => {
    const value = pkg();
    for (const row of value.facts.requirements) {
      if (reason === 'missing checksum') row.sha256 = null;
      if (reason === 'missing check date') row.evidence.checkedAt = null;
      if (reason === 'stale evidence') row.evidence.status = 'stale';
      if (reason === 'invalid check date') row.evidence.checkedAt = 'not-a-date';
    }
    const page = await render(value);
    expect(page.querySelector('[data-testid="package-source-badge"]')?.textContent).toBe('Official source');
    expect(officialSource(value)?.verified).toBe(false);
  }
);

it('keeps architecture-specific domains and complete checksums in the tooltip', async () => {
  const value = pkg();
  const first = structuredClone(value.facts.requirements[0]!);
  const second = structuredClone(first);
  first.downloadUrl = 'https://arm64.downloads.example/app.zip';
  first.sha256 = 'a'.repeat(64);
  second.downloadUrl = 'https://intel.downloads.example/app.zip';
  second.sha256 = 'b'.repeat(64);
  value.facts.requirements = [first, second];
  const page = await render(value);
  const badge = page.querySelector('[data-testid="package-source-badge"]')!;
  const description = page.querySelector(`[id="${badge.getAttribute('aria-describedby')}"]`)!.textContent;
  for (const text of ['arm64.downloads.example', 'intel.downloads.example', first.sha256, second.sha256])
    expect(description).toContain(text);
  expect(badge.textContent).toContain('Verified');
});

it('does not verify conflicting checksums for the same download', () => {
  const value = pkg();
  const first = structuredClone(value.facts.requirements[0]!);
  value.facts.requirements = [first, { ...first, sha256: 'b'.repeat(64) }];
  expect(officialSource(value)).toMatchObject({ verified: false, entries: [{ sha256: null }] });
});

it.each(['missing date', 'invalid date', 'stale'])(
  'does not hide %s evidence behind a verified architecture',
  reason => {
    const value = pkg();
    const first = structuredClone(value.facts.requirements[0]!);
    const second = structuredClone(first);
    second.downloadUrl = 'https://intel.downloads.example/app.zip';
    if (reason === 'missing date') second.evidence.checkedAt = null;
    if (reason === 'invalid date') second.evidence.checkedAt = 'not-a-date';
    if (reason === 'stale') second.evidence.status = 'stale';
    value.facts.requirements = [first, second];
    const source = officialSource(value)!;
    expect(source.verified).toBe(false);
    expect(source.entries.map(entry => entry.host)).toContain('intel.downloads.example');
  }
);

it('supports touch clicks, keyboard focus, Escape and hover on the actual badge', async () => {
  vi.useFakeTimers();
  const root = document.createElement('div');
  document.body.append(root);
  mounted = components(createApp({ render: () => h(Header, { pkg: pkg() }) }));
  mounted.mount(root);
  const badge = root.querySelector<HTMLButtonElement>('[data-testid="package-source-badge"]')!;
  const tooltip = document.getElementById(badge.getAttribute('aria-describedby')!)!;
  badge.click();
  await nextTick();
  await nextTick();
  expect(tooltip.classList.contains('visible')).toBe(true);
  badge.click();
  await nextTick();
  expect(tooltip.classList.contains('invisible')).toBe(true);
  badge.focus();
  await vi.advanceTimersByTimeAsync(400);
  await nextTick();
  expect(tooltip.classList.contains('visible')).toBe(true);
  window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
  await nextTick();
  expect(tooltip.classList.contains('invisible')).toBe(true);
  expect(document.activeElement).toBe(badge);
  badge.blur();
  badge.parentElement!.dispatchEvent(new MouseEvent('mouseenter'));
  await vi.advanceTimersByTimeAsync(400);
  await nextTick();
  expect(tooltip.classList.contains('visible')).toBe(true);
  badge.parentElement!.dispatchEvent(new MouseEvent('mouseleave'));
  await nextTick();
  expect(tooltip.classList.contains('invisible')).toBe(true);
});
