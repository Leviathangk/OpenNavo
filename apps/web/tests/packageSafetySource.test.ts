// @vitest-environment jsdom
import { readFileSync } from 'node:fs';
import { computed, createApp, createSSRApp, h, nextTick, ref, resolveComponent } from 'vue';
import type { App } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { SITE_LOCALES } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import PackageSafety from '../app/components/package/PackageSafety.vue';
import type { PackagePage } from '../app/utils/packageFacts';
import { siteMessage } from '../server/utils/siteCopy';
import production from './fixtures/packageFaq.production.json';

let locale: SiteLocale = 'en-US';
let mounted: App | undefined;
const translate = (key: string, named?: Record<string, unknown>) =>
  siteMessage(locale, key, named as Record<string, string | number>);

// The production payload of iTerm2 carries a verified identity, so each case changes only what it is about.
const base = () => structuredClone(production.packages.iterm2) as unknown as PackagePage;

beforeEach(() => {
  locale = 'en-US';
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('resolveComponent', resolveComponent);
  vi.stubGlobal('useI18n', () => ({ t: translate, locale: ref(locale) }));
  vi.stubGlobal('toAppLocale', () => locale);
  vi.stubGlobal('useLocalePath', () => (path: string) => path);
});
afterEach(() => {
  mounted?.unmount();
  mounted = undefined;
  document.body.innerHTML = '';
  vi.unstubAllGlobals();
});

function components(app: App) {
  app.component('NuxtLink', {
    props: ['to'],
    setup:
      (props, { slots }) =>
      () =>
        h('a', { href: props.to }, slots.default?.())
  });
  return app;
}

async function render(pkg: PackagePage) {
  const context: { teleports?: Record<string, string> } = {};
  const html = await renderToString(components(createSSRApp({ render: () => h(PackageSafety, { pkg }) })), context);
  const page = document.createElement('div');
  page.innerHTML = html + (context.teleports?.body ?? '');
  return page;
}

function withIdentity(openSource: 'yes' | 'no' | 'unknown', overrides: Partial<PackagePage> = {}): PackagePage {
  // iTerm2 ships with an OSI record, so the closed-source cases clear it: the row is about a record that has no
  // other source answer.
  const pkg = { ...base(), openSource: null, repository: undefined, ...overrides };
  return {
    ...pkg,
    facts: {
      ...pkg.facts,
      security: {
        ...pkg.facts.security,
        identities: [
          {
            channels: [],
            openSource,
            evidence: {
              status: 'verified',
              checkedAt: '2026-10-14T06:35:05Z',
              sourceUrl: 'https://example.com/identity'
            }
          }
        ]
      }
    }
  };
}

describe('a verified closed-source identity', () => {
  it('states that the source is not public, without a warning tone', async () => {
    const page = await render(withIdentity('no'));
    const row = page.querySelector('[data-testid="package-safety-source"]')!;
    expect(row).not.toBeNull();
    expect(row.querySelector('dt')?.textContent?.trim()).toBe('Source');
    expect(row.querySelector('dd')?.textContent?.trim()).toContain('Not public');
    // A neutral fact, so neither the row nor its value carries a warning or danger tone.
    expect(row.innerHTML).not.toMatch(/warning|danger|text-(amber|red|orange)-/);
    // Nothing infers safety or risk from it.
    expect(row.textContent).not.toMatch(/safe|unsafe|risk|danger|warning/i);
  });

  it('links the record it came from, so the claim can be checked', async () => {
    const page = await render(withIdentity('no'));
    const row = page.querySelector('[data-testid="package-safety-source"]')!;
    // A closed identity has no repository URL, so its own evidence is the only link a reader can follow.
    expect(row.querySelector('a')?.getAttribute('href')).toBe('https://example.com/identity');
    expect(row.querySelector('a')?.getAttribute('rel')).toBe('nofollow noopener noreferrer');
  });

  it('sits after the developer identity and before the distribution', async () => {
    const page = await render(withIdentity('no'));
    const labels = [...page.querySelectorAll('[data-testid="package-safety"] dt')].map(node =>
      node.textContent?.trim()
    );
    expect(labels.indexOf('Source')).toBeGreaterThan(labels.indexOf('Developer identity'));
    expect(labels.indexOf('Source')).toBeLessThan(labels.indexOf('Distribution'));
  });

  it('is not shown for a record that only says unknown', async () => {
    const page = await render(withIdentity('unknown'));
    expect(page.querySelector('[data-testid="package-safety-source"]')).toBeNull();
  });

  it('stays hidden when the catalog already states an open source or a repository', async () => {
    const withRepository = await render(
      withIdentity('no', { repository: { url: 'https://github.com/acme/app' } as never })
    );
    expect(withRepository.querySelector('[data-testid="package-safety-source"]')).toBeNull();
    const withOsi = await render(
      withIdentity('no', { openSource: { url: 'https://github.com/acme/app', license: 'MIT' } as never })
    );
    expect(withOsi.querySelector('[data-testid="package-safety-source"]')).toBeNull();
  });

  it('stays hidden when another verified identity states the source is open', async () => {
    const pkg = withIdentity('no');
    pkg.facts.security = {
      ...pkg.facts.security,
      identities: [
        ...(pkg.facts.security?.identities ?? []),
        {
          channels: [],
          openSource: 'yes',
          repositoryUrl: 'https://github.com/acme/open',
          evidence: { status: 'verified', checkedAt: '2026-10-14T06:35:05Z', sourceUrl: 'https://example.com/open' }
        }
      ]
    };
    const page = await render(pkg);
    expect(page.querySelector('[data-testid="package-safety-source"]')).toBeNull();
  });

  it('stays hidden when another verified identity only gives its repository', async () => {
    const pkg = withIdentity('no');
    pkg.facts.security = {
      ...pkg.facts.security,
      identities: [
        ...(pkg.facts.security?.identities ?? []),
        {
          channels: [],
          openSource: 'unknown',
          repositoryUrl: 'https://github.com/acme/repo',
          evidence: { status: 'verified', checkedAt: '2026-10-14T06:35:05Z', sourceUrl: 'https://example.com/repo' }
        }
      ]
    };
    const page = await render(pkg);
    // The guard is "an open identity or any repository url", not only `openSource: 'yes'`.
    expect(page.querySelector('[data-testid="package-safety-source"]')).toBeNull();
  });

  it('shows the row in the page language', async () => {
    for (const code of SITE_LOCALES) {
      locale = code;
      const page = await render(withIdentity('no'));
      const row = page.querySelector('[data-testid="package-safety-source"]');
      expect(row, code).not.toBeNull();
      expect(row!.textContent, code).toContain(translate('package.safety.sourceNotPublic'));
    }
  });
});

describe('source labels', () => {
  it('is written in each language instead of the English "Not public"', () => {
    for (const code of SITE_LOCALES) {
      const messages = JSON.parse(readFileSync(`i18n/locales/${code}.json`, 'utf8'));
      const label = messages.package.safety.source;
      const value = messages.package.safety.sourceNotPublic;
      expect(label, code).toBeTruthy();
      expect(value, code).toBeTruthy();
      if (code !== 'en-US') {
        expect(label, code).not.toBe('Source');
        expect(value, code).not.toBe('Not public');
      }
      // Neither string takes a placeholder: each is a fixed label or a fixed fact.
      expect(`${label}${value}`, code).not.toMatch(/[{}]/);
    }
  });
});

// The client renderer reads the same state as the server one; this pins that the row is not a server-only path.
describe('client render', () => {
  it('mounts the same row', async () => {
    const container = document.createElement('div');
    mounted = components(createApp({ render: () => h(PackageSafety, { pkg: withIdentity('no') }) }));
    mounted.mount(container);
    await nextTick();
    expect(container.querySelector('[data-testid="package-safety-source"]')?.textContent).toContain('Not public');
  });
});
