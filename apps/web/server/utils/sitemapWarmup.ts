import { packageSitemaps } from '../../shared/utils/sitemapBuckets';

export const SITEMAP_WARMUP_PATHS = [
  '/sitemap_index.xml',
  ...Object.keys(packageSitemaps).map(name => `/__sitemap__/${name}.xml`)
];

/** Render through Nitro so both the catalog snapshots and the XML response caches are populated. */
export async function warmSitemaps(
  fetch: (path: string) => Promise<{ status: number }>,
  stopped: () => boolean = () => false,
  hasCapacity: () => boolean = () => true,
  includeFonts = true
): Promise<void> {
  // Sequential requests bound memory/CPU and let every child reuse the index's catalog snapshot.
  for (const path of SITEMAP_WARMUP_PATHS) {
    if (!includeFonts && path.startsWith('/__sitemap__/fonts-bucket-')) continue;
    // Yield between XML renders so incoming pages/OG requests can be admitted first. Background work
    // waits while admitted requests or recent event-loop pressure remain; it never reserves a slot.
    await new Promise<void>(resolve => setTimeout(resolve, 0));
    while (!stopped() && !hasCapacity()) await new Promise<void>(resolve => setTimeout(resolve, 100));
    if (stopped()) return;
    try {
      const response = await fetch(path);
      if (response.status !== 200) console.warn(`[opennavo] sitemap warmup failed: ${path} (${response.status})`);
    } catch {
      // Do not print errors which may include API or storage credentials. A failed warmup must not
      // stop startup or replace the normal request's 503 / Retry-After behavior with an empty sitemap.
      console.warn(`[opennavo] sitemap warmup failed: ${path}`);
    }
  }
}
