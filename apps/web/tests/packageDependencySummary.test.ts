import { computed, createSSRApp, h } from 'vue';
import { renderToString } from 'vue/server-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import Summary from '../app/components/package/PackageDependencySummary.vue';
import { pageOf } from './packageFixtures';
import { GEO_SAMPLES } from './geoFixtures';

afterEach(() => vi.unstubAllGlobals());

it('keeps the dependency summary content but follows the API decision for its page link', async () => {
  vi.stubGlobal('computed', computed);
  vi.stubGlobal('useI18n', () => ({ t: (key: string) => key }));
  vi.stubGlobal('useLocalePath', () => (path: string) => `/zh${path}`);
  for (const reason of ['duplicate_of_overview', 'locale_not_ready', '', undefined]) {
    const pkg = pageOf({
      ...GEO_SAMPLES[0]!.detail,
      dependsOn: { macos: null, arch: [], casks: ['helper'], formulae: [] }
    });
    if (reason !== undefined)
      pkg.indexing = {
        indexable: false,
        reason: null,
        locales: [],
        enforced: true,
        pages: {
          dependencies: {
            robots: 'noindex, follow',
            canonicalPath: '',
            hreflang: [],
            inSitemap: false,
            announce: false,
            reason
          }
        }
      } as NonNullable<typeof pkg.indexing>;
    const vue = createSSRApp({ render: () => h(Summary, { pkg }) });
    vue.component('NuxtLink', {
      props: ['to'],
      setup:
        (props, { slots }) =>
        () =>
          h('a', { href: props.to }, slots.default?.())
    });
    const html = await renderToString(vue);
    expect(html).toContain('href="/zh/apps/helper"');
    expect(html.includes(`href="/zh/apps/${pkg.token}/dependencies"`), String(reason)).toBe(
      reason !== 'duplicate_of_overview'
    );
  }
});
