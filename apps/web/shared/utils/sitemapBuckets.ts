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

/**
 * The approved one-page versions-sitemap pilot (#215, approved 2026-11-04). While the global
 * versions sitemap is off, only this app's source-language release history is listed, with no
 * localized alternates. Keep this list at one entry until the pilot is reviewed.
 */
export const VERSIONS_SITEMAP_PILOT_TOKEN = 'dotnet-sdk';
export const VERSIONS_SITEMAP_PILOT_BUCKET = packageSitemapName('versions', VERSIONS_SITEMAP_PILOT_TOKEN);

export const packageSitemaps = Object.fromEntries(
  PACKAGE_SITEMAP_GROUPS.flatMap(group =>
    Array.from({ length: SITEMAP_BUCKET_COUNT }, (_, bucket) => [
      `${group}-bucket-${bucket}`,
      { includeAppSources: false, chunks: false as const }
    ])
  )
);
