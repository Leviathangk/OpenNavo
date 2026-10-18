import { describe, expect, it } from 'vitest';
import { webRouteType } from '../src/webRoute';

describe('Web route types', () => {
  it('Maps localized paths onto a fixed set of page types', () => {
    const cases: [string, string][] = [
      ['/', 'home'],
      ['', 'home'],
      ['/zh', 'home'],
      ['/apps/visual-studio-code', 'app'],
      ['/ja/apps/visual-studio-code/versions?page=2', 'app_versions'],
      ['/apps/visual-studio-code/dependencies', 'app_dependencies'],
      ['/apps/visual-studio-code/details/', 'app_details'],
      ['/apps/visual-studio-code/unknown-tab', 'other'],
      ['/apps/a/b/c', 'other'],
      ['/apps', 'list'],
      ['/apps/', 'list'],
      ['/ru/categories/browsers', 'category'],
      ['/categories/browsers/extra', 'other'],
      ['/collections/new-mac', 'collection'],
      ['/articles', 'article_list'],
      ['/zh/articles?page=2', 'article_list'],
      ['/articles/choose-a-screenshot-app', 'article'],
      ['/articles/choose-a-screenshot-app/extra', 'other'],
      ['/ja/articles/feed.xml', 'feed'],
      ['/authors/lin-wei', 'author'],
      ['/authors', 'other'],
      ['/download', 'download'],
      ['/ja/security', 'security'],
      ['/search?q=editor', 'search'],
      ['/robots.txt', 'robots'],
      ['/sitemap_index.xml', 'sitemap'],
      ['/__sitemap__/packages-1.xml', 'sitemap'],
      ['/apps/visual-studio-code/_payload.json', 'payload'],
      ['/_nuxt/entry.js', 'asset'],
      ['/__og-image__/image/apps/x/og.png', 'og_image'],
      ['/_og/s/abc.png', 'og_image'],
      ['/zhx/apps', 'other'],
      ['/0123456789abcdef.txt', 'other'],
      ['relative', 'other']
    ];
    for (const [path, type] of cases) expect(webRouteType(path), path).toBe(type);
  });
});
