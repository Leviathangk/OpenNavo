import { LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import { createMemoryHistory, createRouter } from 'vue-router';
import { describe, expect, it } from 'vitest';
import { localizeArticlePath } from '../app/utils/articleLinks';

// Use Vue Router's real segment, case and trailing-slash behavior, with i18n's configured prefixes.
const paths = [
  '/',
  '/apps/:token()',
  '/apps/:token()/versions',
  '/articles/:slug()',
  '/background-items/:label()',
  '/future'
];
const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    ...SITE_LOCALES.flatMap(locale => {
      const prefix = LOCALE_METADATA[locale].prefix;
      return paths.map(path => ({ path: `${prefix}${path}`, component: {} }));
    }),
    { path: '/untranslated', component: {} }
  ]
});

describe('article paths use the web routing and locale config', () => {
  it.each(SITE_LOCALES)('targets the configured prefix for %s', locale => {
    expect(localizeArticlePath('/apps/tool', locale, router)).toBe(`${LOCALE_METADATA[locale].prefix}/apps/tool`);
  });

  it.each([
    ['/zh/apps/tool', 'ja', '/ja/apps/tool'],
    ['/ja/apps/tool', 'es', '/es/apps/tool'],
    ['/es/apps/tool', 'ru', '/ru/apps/tool'],
    ['/zh-hant/apps/tool', 'zh', '/zh/apps/tool'],
    ['/zh/apps/tool', 'zh-hant', '/zh-hant/apps/tool'],
    ['/ZH-HANT/apps/tool', 'ja', '/ja/apps/tool'],
    ['/zh-hant/apps/tool', 'en', '/apps/tool'],
    ['/apps/tool', 'en', '/apps/tool'],
    ['/zh', 'en', '/'],
    ['/zh-hant/', 'ja', '/ja'],
    ['/', 'zh-hant', '/zh-hant'],
    ['/apps/tool/', 'ja', '/ja/apps/tool/'],
    ['/apps/tool@2/versions', 'ja', '/ja/apps/tool@2/versions'],
    ['/apps/tool.app', 'ja', '/ja/apps/tool.app'],
    ['/background-items/com.Example.Agent', 'ja', '/ja/background-items/com.Example.Agent'],
    ['/background-items/com.Example.js', 'ja', '/ja/background-items/com.Example.js'],
    ['/future', 'ja', '/ja/future'],
    ['/articles/feed.xml', 'ja', '/ja/articles/feed.xml'],
    ['/zh/apps/tool/feed.xml', 'en', '/apps/tool/feed.xml'],
    ['/collections/picks/feed.xml', 'zh-hant', '/zh-hant/collections/picks/feed.xml']
  ])('resolves %s for %s as %s', (path, locale, expected) => {
    expect(localizeArticlePath(path, locale, router)).toBe(expected);
  });

  it.each([
    '/_og/image.png',
    '/_nuxt/entry.js',
    '/api/v1/articles',
    '/favicon.svg',
    '/fonts/site.woff2',
    '/assets/capture.png',
    '/sitemap.xml',
    '/sitemap_index.xml',
    '/sitemap-apps.xml',
    '/robots.txt',
    '/feed.xml',
    '/feed.json',
    '/articles/feed.json',
    '/articles/capture.png',
    '/articles/capture.avif',
    '/apps/tool/feed.json',
    '/zh/_og/image.png',
    '/zh-hant-extra/apps/tool',
    '/zh-Hans/apps/tool',
    '/untranslated'
  ])('keeps the non-localized path %s', path => {
    expect(localizeArticlePath(path, 'ja', router)).toBe(path);
  });
});
