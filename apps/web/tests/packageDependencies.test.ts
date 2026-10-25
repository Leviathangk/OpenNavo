import { computed, createSSRApp, h, ref } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import Dependencies from '../app/components/package/PackageDependencies.vue';
import { pageOf } from './packageFixtures';
import { GEO_SAMPLES } from './geoFixtures';

afterEach(() => vi.unstubAllGlobals());

function relations(withContent: boolean) {
  return {
    dependencies: withContent ? [{ kind: 'cask', token: 'helper', name: 'Helper', type: 'cask', children: [] }] : [],
    conflictsWith: { casks: [], formulae: [] },
    dependents: { count: 0, top: [] },
    dependsOn: { macos: null, arch: [], formulae: [], casks: [] }
  };
}

it('keeps the dependencies tab out of the index without an API decision, with or without content', async () => {
  for (const [withContent, decided] of [
    [true, false],
    [false, false],
    [true, true]
  ] as const) {
    const pkg = pageOf({ ...GEO_SAMPLES[0]!.detail });
    pkg.indexing = decided
      ? ({
          indexable: true,
          reason: null,
          locales: ['en-US'],
          enforced: false,
          pages: {
            dependencies: {
              robots: 'noindex, follow',
              canonicalPath: '',
              hreflang: [],
              inSitemap: false,
              announce: false,
              reason: 'supporting_page'
            }
          }
        } as NonNullable<typeof pkg.indexing>)
      : undefined;
    const robots = vi.fn();
    vi.stubGlobal('computed', computed);
    vi.stubGlobal('useI18n', () => ({ t: (key: string) => key, locale: ref('en') }));
    vi.stubGlobal('toAppLocale', () => 'en-US');
    vi.stubGlobal('useApi', () => ({}));
    vi.stubGlobal('useLocalePath', () => (path: string) => path);
    vi.stubGlobal('resolveComponent', () => 'a');
    vi.stubGlobal('createError', (value: unknown) => value);
    vi.stubGlobal('usePackage', async () => ({ data: ref(pkg), token: ref(pkg.token) }));
    vi.stubGlobal('useAsyncData', async () => ({
      data: ref(relations(withContent)),
      error: ref(null),
      refresh: vi.fn()
    }));
    vi.stubGlobal('usePageSeo', vi.fn());
    vi.stubGlobal('useRobotsRule', robots);
    const html = await renderToString(createSSRApp({ render: () => h(Dependencies, { kind: 'cask' }) }));
    expect(html.includes('package.deps.runtime'), `runtime section, content ${withContent}`).toBe(withContent);
    // With a decision the layout applies it (usePageDecision); the tab adds nothing of its own.
    expect(robots.mock.calls, `content ${withContent}, decided ${decided}`).toEqual(
      decided ? [] : [['noindex, follow']]
    );
  }
});
