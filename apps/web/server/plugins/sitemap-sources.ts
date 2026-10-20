import { randomUUID } from 'node:crypto';
import type { PublicComponents } from '@opennavo/api';
import { LOCALES, VERSION_PAGE_MIN_NOTES } from '@opennavo/shared';
import { packageSitemapName, packageSitemaps, type PackageSitemapGroup } from '../../shared/utils/sitemapBuckets';
import { PAGE_CACHE_TTL_SECONDS } from '../utils/boundedCache';
import { localizedEntries, openLocaleEntries } from '../utils/sitemapEntries';
import { createStaleCache } from '../utils/staleCache';

type SitemapEntry = PublicComponents['schemas']['SitemapEntry'];
type BackgroundItem = PublicComponents['schemas']['BackgroundItem'];
type Locale = PublicComponents['schemas']['Locale'];

interface CategoryNode {
  slug: string;
  hiddenByDefault: boolean;
  /** Apps the category page lists by default; older APIs omit it. */
  packageCount?: number;
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
    // T5: each package bucket and the article groups carry the date their newest page last changed in
    // content; groups without such a date (static pages, categories, collections) carry none.
    const newest = (dates: (string | null | undefined)[]) =>
      dates
        .filter((date): date is string => Boolean(date))
        .sort()
        .at(-1);
    const lastmods: Record<string, string | undefined> = {
      articles: newest(articles.flatMap(item => item.locales.map(entry => entry.lastmod))),
      authors: newest(authors.map(author => author.lastmod))
    };
    ctx.sitemaps = ctx.sitemaps
      .filter(({ _sitemapName: sitemapName }) => {
        if (sitemapName && Object.hasOwn(packageSitemaps, sitemapName)) return populated.has(sitemapName);
        if (sitemapName === 'articles') return articles.length > 0;
        if (sitemapName === 'authors') return authors.length > 0;
        return true;
      })
      .map(entry => {
        const { _sitemapName: sitemapName } = entry;
        const name = sitemapName ?? '';
        const lastmod = Object.hasOwn(packageSitemaps, name) ? populated.get(name) : lastmods[name];
        return lastmod ? { ...entry, lastmod } : entry;
      });
  });
  // L1: the module builds the static group from the app's pages in every site language; only the
  // languages open for indexing (site settings, pages.openLocales) stay in it.
  nitroApp.hooks.hook('sitemap:resolved', async ctx => {
    if (ctx.sitemapName !== 'static') return;
    ctx.urls = openLocaleEntries(ctx.urls, (await getSiteSettings()).pages.openLocales);
  });
  nitroApp.hooks.hook('sitemap:input', async ctx => {
    if (Object.hasOwn(packageSitemaps, ctx.sitemapName)) {
      const group = ctx.sitemapName.split('-')[0] as PackageSitemapGroup;
      if (group === 'versions' && !(await getSiteSettings()).pages.versionsSitemap) return;
      const records =
        group === 'versions'
          ? (await Promise.all([cachedPackages('apps'), cachedPackages('fonts')])).flat()
          : await cachedPackages(group);
      const page = group === 'versions' ? 'versions' : 'overview';
      const urls = records
        .filter(record => packageSitemapName(group, record.token) === ctx.sitemapName)
        .flatMap(record =>
          page === 'versions'
            ? packageEntries(record, `/apps/${record.token}/versions`, ctx.sitemapName, page)
            : [
                ...packageEntries(record, `/apps/${record.token}`, ctx.sitemapName, page),
                // X2 intent pages sit beside their app, only in languages whose decision lists them.
                ...packageEntries(record, `/apps/${record.token}/alternatives`, ctx.sitemapName, 'alternatives'),
                ...packageEntries(record, `/apps/${record.token}/uninstall`, ctx.sitemapName, 'uninstall'),
                ...packageEntries(record, `/apps/${record.token}/troubleshooting`, ctx.sitemapName, 'troubleshooting')
              ]
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
 * Package buckets that have URLs, for the index, with the newest content date of the pages each one
 * lists (undefined when none is known). Working this out reads the whole catalog snapshot (megabytes
 * from Redis on every index request), so each process keeps the answer (createStaleCache): after the
 * first request the index answers at once, even when cold. It uses the same rule as the buckets
 * themselves (listedLocales), so the index never lists an empty one.
 */
const populatedPackageSitemaps = createStaleCache(
  async () => {
    const [apps, fonts, settings] = await Promise.all([
      cachedPackages('apps'),
      cachedPackages('fonts'),
      getSiteSettings()
    ]);
    const buckets = new Map<string, string | undefined>();
    const add = (group: PackageSitemapGroup, record: SitemapEntry, page: SitemapPage) => {
      const entries = listedLocales(record, page);
      if (entries?.length === 0) return;
      const name = packageSitemapName(group, record.token);
      const dates = (entries ?? []).map(entry => entry.pageLastmod?.[page] ?? entry.lastmod);
      buckets.set(name, [buckets.get(name), ...dates].filter(Boolean).sort().at(-1) as string | undefined);
    };
    for (const record of apps) add('apps', record, 'overview');
    for (const record of fonts) add('fonts', record, 'overview');
    if (settings.pages.versionsSitemap) for (const record of [...apps, ...fonts]) add('versions', record, 'versions');
    return buckets;
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

type SitemapPage = 'overview' | 'versions' | 'alternatives' | 'uninstall' | 'troubleshooting';
/**
 * Intent pages (X2) and the troubleshooting page (F7) exist only where a decision opens them; without one
 * there is no page to list.
 */
const INTENT_PAGES: ReadonlySet<SitemapPage> = new Set(['alternatives', 'uninstall', 'troubleshooting']);

/**
 * The languages a sitemap lists one page in: those whose decision says so (S5: `pages.<page>.inSitemap`).
 * Records without decisions keep their published languages, and versions pages then need enough release
 * notes. Undefined means an older record without languages: every language.
 */
function listedLocales(record: Pick<SitemapEntry, 'locales' | 'notesCount'>, page: SitemapPage) {
  if (INTENT_PAGES.has(page)) return record.locales?.filter(entry => entry.pages?.[page]?.inSitemap === true) ?? [];
  const decided = record.locales?.some(entry => entry.pages);
  if (page === 'versions' && !decided && (record.notesCount ?? 0) < VERSION_PAGE_MIN_NOTES) return [];
  return record.locales?.filter(entry => entry.pages?.[page]?.inSitemap ?? true);
}

/** One page in every language listedLocales gives, with the page's own content date (T5: `pageLastmod`). */
function packageEntries(
  record: Pick<SitemapEntry, 'locales' | 'notesCount'>,
  path: string,
  sitemap: string,
  page: SitemapPage = 'overview'
) {
  const entries = listedLocales(record, page);
  const locales = entries?.map(entry => entry.locale) ?? LOCALES;
  const lastmods = new Map(entries?.map(entry => [entry.locale, entry.pageLastmod?.[page] ?? entry.lastmod]));
  return localizedEntries(path, sitemap, locales, locale => lastmods.get(locale));
}

type ArticleSitemap = PublicComponents['schemas']['ArticleSitemap'];

async function articleSitemap(): Promise<ArticleSitemap> {
  return (await $fetch<{ data: ArticleSitemap }>(`${apiBase()}/sitemap/articles`)).data;
}

async function openLocales() {
  return (await getSiteSettings()).pages.openLocales;
}

function flatten(nodes: CategoryNode[]): CategoryNode[] {
  return nodes.flatMap(node => [node, ...flatten(node.children)]);
}

const GROUPS: Record<string, () => Promise<ReturnType<typeof localizedEntries>>> = {
  // Lists and other pages that are not about one app or article use the languages open for indexing (L1).
  categories: async () => {
    const [tree, open] = await Promise.all([
      $fetch<{ data: CategoryNode[] }>(`${apiBase()}/categories`),
      openLocales()
    ]);
    return (
      flatten(tree.data)
        .filter(node => !node.hiddenByDefault)
        // An empty category is a noindex list (S4), so it stays out of the sitemap too.
        .filter(node => (node.packageCount ?? 1) > 0)
        .flatMap(node => localizedEntries(`/categories/${node.slug}`, 'categories', open))
    );
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
      ...localizedEntries('/articles', 'articles', await openLocales(), () => newest),
      ...articles.flatMap(item => packageEntries(item, `/articles/${item.slug}`, 'articles'))
    ];
  },
  // T1: every page of every A–Z leaf in each language open for indexing. Names sort into buckets by the
  // language they are shown in, so a leaf's alternates are the languages that have the same leaf page.
  browse: async () => {
    const languages = new Map<string, Locale[]>();
    for (const locale of await openLocales())
      for (const kind of ['apps', 'fonts'] as const) {
        const root = await $fetch<{ data: { buckets: { key: string; pages: number }[] } }>(`${apiBase()}/browse`, {
          query: { kind, locale }
        });
        for (const bucket of root.data.buckets)
          for (let page = 1; page <= bucket.pages; page += 1) {
            const path = `/browse/${kind === 'fonts' ? 'fonts/' : ''}${bucket.key}${page > 1 ? `?page=${page}` : ''}`;
            languages.set(path, [...(languages.get(path) ?? []), locale]);
          }
      }
    return [...languages].flatMap(([path, locales]) => localizedEntries(path, 'browse', locales));
  },
  // F1: the label pages the API marks indexable in each open language, dated by their checked content.
  'background-items': async () => {
    const labels = new Map<string, { locales: Locale[]; lastmod: Map<Locale, string | undefined> }>();
    for (const locale of await openLocales())
      for (let current = 1; ; current += 1) {
        const page = await $fetch<{ data: { total: number; records: BackgroundItem[] } }>(
          `${apiBase()}/background-items`,
          { query: { locale, current, size: 100 } }
        );
        for (const item of page.data.records.filter(record => record.indexable)) {
          const entry = labels.get(item.label) ?? {
            locales: [] as Locale[],
            lastmod: new Map<Locale, string | undefined>()
          };
          entry.locales.push(locale);
          entry.lastmod.set(locale, item.content?.evidence.checkedAt ?? undefined);
          labels.set(item.label, entry);
        }
        if (page.data.records.length < 100 || current * 100 >= page.data.total) break;
      }
    return [...labels].flatMap(([label, entry]) =>
      localizedEntries(`/background-items/${encodeURIComponent(label)}`, 'background-items', entry.locales, locale =>
        entry.lastmod.get(locale)
      )
    );
  },
  authors: async () => {
    const [{ authors }, open] = await Promise.all([articleSitemap(), openLocales()]);
    return authors.flatMap(author =>
      localizedEntries(`/authors/${author.slug}`, 'authors', open, () => author.lastmod)
    );
  },
  collections: async () => {
    const open = await openLocales();
    const urls: ReturnType<typeof localizedEntries> = [];
    for (let current = 1; ; current += 1) {
      const page = await $fetch<{
        data: { total: number; records: { slug: string; updatedAt: string; itemCount: number }[] };
      }>(`${apiBase()}/collections`, { query: { current, size: 100 } });
      // An empty collection is a noindex page (S4), so it stays out of the sitemap too.
      urls.push(
        ...page.data.records
          .filter(item => item.itemCount > 0)
          .flatMap(item => localizedEntries(`/collections/${item.slug}`, 'collections', open, () => item.updatedAt))
      );
      if (page.data.records.length < 100 || current * 100 >= page.data.total) return urls;
    }
  }
};
