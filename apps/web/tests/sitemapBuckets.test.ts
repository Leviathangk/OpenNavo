import { describe, expect, it } from 'vitest';
import { packageSitemapName, packageSitemaps } from '../shared/utils/sitemapBuckets';
import { localizedEntries } from '../server/utils/sitemapEntries';

describe('Stable sitemap buckets', () => {
  it('Keeps retained URLs exactly once when different buckets use different catalog generations', () => {
    const old = Array.from({ length: 6000 }, (_, i) => `app-${i}`);
    const updated = ['new-app', ...old.slice(100).reverse()];
    const names = Object.keys(packageSitemaps).filter(name => name.startsWith('apps-'));
    const mixed = names.flatMap((name, index) =>
      (index % 2 ? old : updated)
        .filter(token => packageSitemapName('apps', token) === name)
        .flatMap(token => localizedEntries(`/apps/${token}`, name))
    );
    const urls = mixed.map(entry => entry.loc);
    expect(new Set(urls).size).toBe(urls.length);
    const retained = new Set(urls);
    for (const token of old.slice(100)) {
      for (const entry of localizedEntries(`/apps/${token}`, 'unused')) expect(retained.has(entry.loc)).toBe(true);
    }
  });
});
