import { randomUUID } from 'node:crypto';
import type { PublicComponents } from '@opennavo/api';
import { LOCALES, VERSION_PAGE_MIN_NOTES } from '@opennavo/shared';
import { packageSitemapName, packageSitemaps, type PackageSitemapGroup } from '../../shared/utils/sitemapBuckets';
import { PAGE_CACHE_TTL_SECONDS } from '../utils/boundedCache';
import { localizedEntries } from '../utils/sitemapEntries';
import { createStaleCache } from '../utils/staleCache';

type SitemapEntry = PublicComponents['schemas']['SitemapEntry'];

interface CategoryNode {
  slug: string;
  hiddenByDefault: boolean;
  children: CategoryNode[];
}

// Split completed catalog snapshots below the 512 KiB storage limit.
const PAGE_SIZE = 500;
/**
 * Sitemaps may trail the catalog by a few minutes (IndexNow announces changes as they happen): a
 * catalog snapshot, and the index's list of populated buckets, are used as they are for this long
 * and then served stale while one rebuild per process runs behind the response.
 */
export const SNAPSHOT_FRESH_MS = 5 * 60_000;
/** After a failed background rebuild, the stale snapshot keeps answering this long before a retry. */
export const SNAPSHOT_RETRY_MS = 30_000;
/** Snapshots stay readable as long as the cache storage keeps anything, so stale ones answer at once. */
const SNAPSHOT_TTL_SECONDS = PAGE_CACHE_TTL_SECONDS;

// Dynamic sitemap groups (WP3) are injected through @nuxtjs/sitemap's sitemap:input hook rather than
// HTTP sources: the module turns a failed source into a shorter 200 response, which crawlers read
// as removed URLs. An API error here fails the request instead, so crawlers retry later.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('sitemap:index-resolved', async ctx => {
    // Non-chunked groups are listed even when empty. Filter the rendered index,
    // without mutating its cached entries, so newly published content can reappear.
    // Let API failures propagate when nothing is cached yet: an outage must not look like removed
    // content. The article check is one small request and follows publishing at once; the
    // populated buckets come from the cached catalog (populatedPackageSitemaps).
    const [{ articles, authors }, populated] = await Promise.all([articleSitemap(), populatedPackageSitemaps()]);
    ctx.sitemaps = ctx.sitemaps.filter(({ _sitemapName: sitemapName }) => {
      if (sitemapName && Object.hasOwn(packageSitemaps, sitemapName)) return populated.has(sitemapName);
      if (sitemapName === 'articles') return articles.length > 0;
      if (sitemapName === 'authors') return authors.length > 0;
      return true;
    });
  });
  nitroApp.hooks.hook('sitemap:input', async ctx => {
    if (Object.hasOwn(packageSitemaps, ctx.sitemapName)) {
      const group = ctx.sitemapName.split('-')[0] as PackageSitemapGroup;
      if (group === 'versions' && !(await getSiteSettings()).pages.versionsSitemap) return;
      const records =
        group === 'versions'
          ? (await Promise.all([cachedPackages('apps'), cachedPackages('fonts')])).flat()
          : await cachedPackages(group);
      const urls = records
        .filter(record => packageSitemapName(group, record.token) === ctx.sitemapName)
        .filter(record => group !== 'versions' || (record.notesCount ?? 0) >= VERSION_PAGE_MIN_NOTES)
        .flatMap(record =>
          packageEntries(record, `/apps/${record.token}${group === 'versions' ? '/versions' : ''}`, ctx.sitemapName)
        );
      // Never silently truncate a bucket if the catalog outgrows this partition.
      if (urls.length > 50000) throw new Error(`Sitemap bucket exceeds URL limit: ${ctx.sitemapName}`);
      ctx.urls.push(...urls);
      return;
    }
    const load = GROUPS[ctx.sitemapName];
    if (load) ctx.urls.push(...(await load()));
  });
});

function apiBase() {
  const config = useRuntimeConfig();
  return (config.apiBaseInternal || config.public.apiBase).replace(/\/$/, '');
}

type PackageGroup = 'apps' | 'fonts';
interface PackageSnapshot {
  keys: string[];
  /** Until then the snapshot is fresh; afterwards it still answers while a rebuild runs. */
  expires: number;
}

/**
 * Package buckets that have URLs, for the index. Working this out reads the whole catalog snapshot
 * (megabytes from Redis on every index request), so each process keeps the answer
 * (createStaleCache): after the first request the index answers at once, even when cold.
 */
const populatedPackageSitemaps = createStaleCache(
  async () => {
    const [apps, fonts, settings] = await Promise.all([
      cachedPackages('apps'),
      cachedPackages('fonts'),
      getSiteSettings()
    ]);
    return new Set([
      ...apps.filter(record => record.locales?.length !== 0).map(record => packageSitemapName('apps', record.token)),
      ...fonts.filter(record => record.locales?.length !== 0).map(record => packageSitemapName('fonts', record.token)),
      ...(settings.pages.versionsSitemap
        ? [...apps, ...fonts]
            .filter(record => record.locales?.length !== 0 && (record.notesCount ?? 0) >= VERSION_PAGE_MIN_NOTES)
            .map(record => packageSitemapName('versions', record.token))
        : [])
    ]);
  },
  {
    freshMs: SNAPSHOT_FRESH_MS,
    retryMs: SNAPSHOT_RETRY_MS,
    onError: error => console.error('[opennavo] sitemap index refresh failed', error)
  }
);

// Coalesce reads and rebuilds, including callers that observed missing chunks. Each server has
// its own in-flight maps; shared Redis snapshots remain immutable.
const packageReads = new Map<PackageGroup, Promise<SitemapEntry[]>>();
const packageRebuilds = new Map<PackageGroup, Promise<SitemapEntry[]>>();
const rebuildFailedAt = new Map<PackageGroup, number>();

/** Settles once no snapshot or index refresh is running (tests). */
export async function sitemapSourcesIdle(): Promise<void> {
  await populatedPackageSitemaps.idle();
  while (packageRebuilds.size) await Promise.allSettled(packageRebuilds.values());
}

function manifestKey(group: PackageGroup) {
  return `sitemap-package-manifest-v3-${PAGE_SIZE}:${group}`;
}

function cachedPackages(group: PackageGroup): Promise<SitemapEntry[]> {
  const pending = packageReads.get(group);
  if (pending) return pending;
  const read = readPackages(group).finally(() => packageReads.delete(group));
  packageReads.set(group, read);
  return read;
}

async function readPackages(group: PackageGroup): Promise<SitemapEntry[]> {
  const snapshot = await readSnapshot(group);
  // Without a complete snapshot the request waits for the catalog and fails if the API does.
  if (!snapshot) return rebuildPackages(group);
  if (snapshot.expires <= Date.now()) refreshPackages(group);
  return snapshot.records;
}

async function readSnapshot(group: PackageGroup) {
  const storage = useStorage('cache');
  // A rebuild that publishes between reading the manifest and its chunks reclaims those chunks:
  // read the manifest once more before treating the snapshot as lost (evicted).
  let seen: string | undefined;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const manifest = await storage.getItem<PackageSnapshot>(manifestKey(group));
    if (!manifest || manifest.keys.join() === seen) return undefined;
    const chunks = await Promise.all(manifest.keys.map(key => storage.getItem<SitemapEntry[]>(key)));
    if (chunks.every(chunk => chunk !== null)) return { records: chunks.flat(), expires: manifest.expires };
    seen = manifest.keys.join();
  }
  return undefined;
}

function rebuildPackages(group: PackageGroup): Promise<SitemapEntry[]> {
  const pending = packageRebuilds.get(group);
  if (pending) return pending;
  const rebuild = writeSnapshot(group).finally(() => packageRebuilds.delete(group));
  packageRebuilds.set(group, rebuild);
  return rebuild;
}

/** Rebuilds a stale snapshot behind the response; a failure keeps the stale one for a while. */
function refreshPackages(group: PackageGroup) {
  const failedAt = rebuildFailedAt.get(group);
  if (packageRebuilds.has(group) || (failedAt !== undefined && Date.now() - failedAt < SNAPSHOT_RETRY_MS)) return;
  rebuildPackages(group).then(
    () => rebuildFailedAt.delete(group),
    error => {
      rebuildFailedAt.set(group, Date.now());
      console.error(`[opennavo] sitemap ${group} snapshot refresh failed`, error);
    }
  );
}

async function writeSnapshot(group: PackageGroup): Promise<SitemapEntry[]> {
  const storage = useStorage('cache');
  // Reload every OFFSET page and publish only after all immutable chunks are stored.
  const pages = await loadPackagePages(group);
  const version = randomUUID();
  const keys = pages.map((_, index) => `sitemap-package-snapshots-v2:${group}:${version}:${index}`);
  let previous: PackageSnapshot | null;
  try {
    // Sequential writes ensure no writes remain in flight when failure cleanup starts.
    for (const [index, page] of pages.entries()) {
      await storage.setItem(keys[index]!, page, { ttl: SNAPSHOT_TTL_SECONDS });
      if (!(await storage.hasItem(keys[index]!))) {
        throw new Error('Sitemap snapshot chunk was not stored');
      }
    }
    // Read the manifest being replaced right before replacing it, so a snapshot another server
    // published in the meantime is reclaimed as well.
    previous = await storage.getItem<PackageSnapshot>(manifestKey(group));
    await storage.setItem(
      manifestKey(group),
      { keys, expires: Date.now() + SNAPSHOT_FRESH_MS },
      { ttl: SNAPSHOT_TTL_SECONDS }
    );
  } catch (error) {
    await Promise.all(keys.map(key => storage.removeItem(key)));
    throw error;
  }
  // Explicit reclamation is required: the default memory driver ignores TTL.
  // Readers racing this cleanup move on to the new manifest (readSnapshot).
  if (previous) await Promise.all(previous.keys.map(key => storage.removeItem(key)));
  return pages.flat();
}

async function loadPackagePages(group: 'apps' | 'fonts'): Promise<SitemapEntry[][]> {
  const pages: SitemapEntry[][] = [];
  // Rows can be fewer than the page size (the API filters by index policy); total counts candidates.
  for (let current = 1; ; current += 1) {
    const page = await $fetch<{ data: { total: number; records: SitemapEntry[] } }>(`${apiBase()}/sitemap/packages`, {
      query: { current, size: PAGE_SIZE, group }
    });
    // Link apps share /apps with casks (store/security plan §4); command-line tools have no web page.
    pages.push(page.data.records.filter(record => record.kind === 'cask' || record.kind === 'link'));
    if (current * PAGE_SIZE >= page.data.total) return pages;
  }
}

function packageEntries(record: Pick<SitemapEntry, 'locales'>, path: string, sitemap: string) {
  const locales = record.locales?.map(entry => entry.locale) ?? LOCALES;
  const lastmods = new Map(record.locales?.map(entry => [entry.locale, entry.lastmod]));
  return localizedEntries(path, sitemap, locales, locale => lastmods.get(locale));
}

type ArticleSitemap = PublicComponents['schemas']['ArticleSitemap'];

async function articleSitemap(): Promise<ArticleSitemap> {
  return (await $fetch<{ data: ArticleSitemap }>(`${apiBase()}/sitemap/articles`)).data;
}

function flatten(nodes: CategoryNode[]): CategoryNode[] {
  return nodes.flatMap(node => [node, ...flatten(node.children)]);
}

const GROUPS: Record<string, () => Promise<ReturnType<typeof localizedEntries>>> = {
  categories: async () => {
    const tree = await $fetch<{ data: CategoryNode[] }>(`${apiBase()}/categories`);
    return flatten(tree.data)
      .filter(node => !node.hiddenByDefault)
      .flatMap(node => localizedEntries(`/categories/${node.slug}`, 'categories'));
  },
  // Articles in the languages whose text is ready, and the list once there is something to list.
  articles: async () => {
    const { articles } = await articleSitemap();
    if (!articles.length) return [];
    const newest = articles
      .flatMap(item => item.locales.map(entry => entry.lastmod ?? ''))
      .sort()
      .at(-1);
    return [
      ...localizedEntries('/articles', 'articles', LOCALES, () => newest),
      ...articles.flatMap(item => packageEntries(item, `/articles/${item.slug}`, 'articles'))
    ];
  },
  authors: async () =>
    (await articleSitemap()).authors.flatMap(author =>
      localizedEntries(`/authors/${author.slug}`, 'authors', LOCALES, () => author.lastmod)
    ),
  collections: async () => {
    const urls: ReturnType<typeof localizedEntries> = [];
    for (let current = 1; ; current += 1) {
      const page = await $fetch<{
        data: { total: number; records: { slug: string; updatedAt: string }[] };
      }>(`${apiBase()}/collections`, { query: { current, size: 100 } });
      urls.push(
        ...page.data.records.flatMap(item =>
          localizedEntries(`/collections/${item.slug}`, 'collections', LOCALES, () => item.updatedAt)
        )
      );
      if (page.data.records.length < 100 || current * 100 >= page.data.total) return urls;
    }
  }
};
