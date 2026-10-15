// Keep this count stable: changing it requires expiring all sitemap caches together.
// Hashing tokens (rather than slicing a sorted URL list) keeps every language of an app
// in the same file even when other apps or translations are added or removed.
export const SITEMAP_BUCKET_COUNT = 8;
export const PACKAGE_SITEMAP_GROUPS = ['apps', 'fonts', 'versions'] as const;
export type PackageSitemapGroup = (typeof PACKAGE_SITEMAP_GROUPS)[number];

export function packageSitemapName(group: PackageSitemapGroup, token: string) {
  let hash = 2166136261;
  for (const char of token) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619);
  return `${group}-bucket-${(hash >>> 0) % SITEMAP_BUCKET_COUNT}`;
}

export const packageSitemaps = Object.fromEntries(
  PACKAGE_SITEMAP_GROUPS.flatMap(group =>
    Array.from({ length: SITEMAP_BUCKET_COUNT }, (_, bucket) => [
      `${group}-bucket-${bucket}`,
      { includeAppSources: false, chunks: false as const }
    ])
  )
);
