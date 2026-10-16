import { readdirSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { canonicalRequestUrl, PAGE_SECTIONS } from '../server/utils/canonicalUrl';

describe('One-hop canonical page URLs', () => {
  it('redirects trailing slashes, uppercase and the /en prefix, keeping the query', () => {
    const cases: [string, string][] = [
      ['/apps/perforce/', '/apps/perforce'],
      ['/Apps/perforce', '/apps/perforce'],
      ['/APPS/Visual-Studio-Code/Versions/?page=2', '/apps/visual-studio-code/versions?page=2'],
      ['/zh/', '/zh'],
      ['/ZH', '/zh'],
      ['/ja/categories/', '/ja/categories'],
      ['/en', '/'],
      ['/en/', '/'],
      ['/en?ref=old', '/?ref=old'],
      ['/en/download', '/download'],
      ['/EN/Download/?utm_source=x&A=B', '/download?utm_source=x&A=B'],
      ['/en/apps/perforce/versions', '/apps/perforce/versions'],
      ['/search/?q=Visual%20Studio', '/search?q=Visual%20Studio'],
      ['/collections//', '/collections']
    ];
    for (const [from, to] of cases) expect(canonicalRequestUrl(from), from).toBe(to);
  });

  it('spells out escaped tokens in the same hop', () => {
    expect(canonicalRequestUrl('/zh/apps/plus-app%2B%2B/versions?source=a%2Bb')).toBe(
      '/zh/apps/plus-app++/versions?source=a%2Bb'
    );
    expect(canonicalRequestUrl('/Apps/plus-app%2b%2b/')).toBe('/apps/plus-app++');
    expect(canonicalRequestUrl('/en/apps/versioned-app%4011')).toBe('/apps/versioned-app@11');
    // Payloads keep their path apart from the token (Nuxt requests them with the spelling it renders).
    expect(canonicalRequestUrl('/ja/apps/plus-app%2B%2B/versions/_payload.json')).toBe(
      '/ja/apps/plus-app++/versions/_payload.json'
    );
    // Paths that keep an escape are not pages: they answer 404 without a hop.
    for (const token of ['plus-app%252B%252B', 'bad%20token', 'bad%2Ftoken', 'bad%ZZ', 'Bad%20Token/'])
      expect(canonicalRequestUrl(`/apps/${token}`), token).toBeUndefined();
  });

  it('leaves canonical pages alone', () => {
    for (const url of [
      '/',
      '/?page=2',
      '/zh',
      '/apps',
      '/apps?page=2&sort=name',
      '/apps/nextpad++',
      '/apps/logi-options+',
      '/apps/font-m+-nerd-font',
      '/apps/versioned-app@11/details',
      '/apps/some.app_name',
      '/ru/articles/choose-a-screenshot-app',
      '/apps/perforce?Ref=Upper'
    ])
      expect(canonicalRequestUrl(url), url).toBeUndefined();
  });

  it('never touches assets, payloads, the API, sitemaps, robots or other files', () => {
    for (const url of [
      '/_nuxt/Entry.CQMRp0wV.js',
      '/_nuxt/builds/meta/abc.json',
      '/_payload.json?_b=1',
      '/Apps/_payload.json',
      '/apps/perforce/_payload.json?_b=1',
      '/zh/_payload.json',
      '/api/consent-region',
      '/API/Consent-Region/',
      '/sitemap_index.xml',
      '/Sitemap.xml',
      '/__sitemap__/apps-bucket-0.xml',
      '/robots.txt',
      '/Robots.txt',
      '/FixtureKey-0001.txt',
      '/favicon.ico',
      '/fonts/OFL.txt',
      '/_og/s/c_OgListSatori,ch_Abc.png',
      '/__og-image__/image/x.png',
      '/_i18n/4faf2c50/en/messages.json',
      '/articles/feed.xml',
      '/zh/articles/feed.xml',
      '/en/articles/feed.xml',
      '/__nuxt_error',
      '/wp-login.php',
      '//apps/perforce'
    ])
      expect(canonicalRequestUrl(url), url).toBeUndefined();
  });

  it('knows every page route', () => {
    const sections = readdirSync(new URL('../app/pages', import.meta.url))
      .map(name => name.replace(/\.vue$/, ''))
      .filter(name => name !== 'index');
    expect([...PAGE_SECTIONS].sort()).toEqual([...new Set(sections)].sort());
  });
});
