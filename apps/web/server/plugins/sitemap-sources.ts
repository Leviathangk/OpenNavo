import { createError } from 'h3';
import { labelPath } from '../../shared/utils/backgroundLabels';
import { randomUUID } from 'node:crypto';
import type { PublicComponents } from '@opennavo/api';
import { BRAND, LOCALES, VERSION_PAGE_MIN_NOTES } from '@opennavo/shared';
import { packageSitemapName, packageSitemaps, type PackageSitemapGroup } from '../../shared/utils/sitemapBuckets';
import { MAX_CACHE_ENTRY_BYTES, PAGE_CACHE_TTL_SECONDS } from '../utils/boundedCache';
import { BACKGROUND_PAGE_SIZE, indexPageNoindex } from '../../app/utils/backgroundItems';
import { localizedEntries, openLocaleEntries, pageLocaleEntries, staticSitemapLocale } from '../utils/sitemapEntries';
import { createStaleCache } from '../utils/staleCache';

type SitemapEntry = PublicComponents['schemas']['SitemapEntry'];
type SitemapLocale = PublicComponents['schemas']['SitemapLocale'];
type BackgroundItem = PublicComponents['schemas']['BackgroundItem'];
type Locale = PublicComponents['schemas']['Locale'];

interface CategoryNode {
  slug: string;
  hiddenByDefault: boolean;
  /** Apps the category page lists by default; older APIs omit it. */
  packageCount?: number;
  children: CategoryNode[];
}

const PAGE_SIZE = 500;
/**
 * Stored snapshot chunks stay well below the cache's entry limit, which silently drops anything larger:
 * a page of records whose slimmed form is still bigger is split further (slimRecord, chunkRecords).
 */
const CHUNK_BYTES = Math.floor(MAX_CACHE_ENTRY_BYTES * 0.75);
/**
 * Sitemaps may trail the catalog by up to half an hour (IndexNow announces changes as they happen): a
 * catalog snapshot, and the index's list of populated buckets, are used as they are for this long
 * and then served stale while one rebuild per process runs behind the response. A rebuild reads every
 * page of per-language decisions from the API, so it stays rare.
 */
export const SNAPSHOT_FRESH_MS = 30 * 60_000;
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
    const [{ articles, authors }, populated, open] = await Promise.all([
      articleSitemap(),
      populatedPackageSitemaps(),
      openLocales()
    ]);
    // The label list takes hundreds of API pages to load after a restart, so the index reads it without waiting
    // and leaves the group out until it is known.
    const backgroundItems = backgroundItemEntries.peek();
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
        // An empty urlset in the index reads as a broken sitemap to Search Console.
        if (sitemapName === 'background-items') return Boolean(backgroundItems?.length);
        // A language closed for indexing has no static pages left in its sitemap.
        const locale = staticSitemapLocale(sitemapName);
        if (locale) return open.includes(locale);
        return true;
      })
      .map(entry => {
        const { _sitemapName: sitemapName } = entry;
        const name = sitemapName ?? '';
        const lastmod = Object.hasOwn(packageSitemaps, name) ? populated.get(name) : lastmods[name];
        return lastmod ? { ...entry, lastmod } : entry;
      });
  });
  // L1: the module builds the static group from the app's pages in every site language and splits it into one
  // sitemap per language (en-static, zh-Hans-static, …). Only the languages open for indexing (site settings,
  // pages.openLocales) stay in them, and the background items list only where it is indexable.
  nitroApp.hooks.hook('sitemap:resolved', async ctx => {
    if (ctx.sitemapName !== 'static' && !staticSitemapLocale(ctx.sitemapName)) return;
    const [open, listed] = await Promise.all([openLocales(), backgroundListLocales()]);
    ctx.urls = pageLocaleEntries(openLocaleEntries(ctx.urls, open), '/background-items', listed);
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

/** What a sitemap reads of one language: whether each page is listed, and the dates (slimRecord). */
interface ListedLocale {
  locale: Locale;
  lastmod: string | null;
  pageLastmod?: Record<string, string>;
  pages?: Partial<Record<SitemapPage, { inSitemap?: boolean }>>;
}
/** A catalog record as snapshots store it: the API's record without decision details sitemaps never read. */
interface SitemapRecord {
  kind: SitemapEntry['kind'];
  token: string;
  notesCount?: number;
  locales?: ListedLocale[];
}
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
    const add = (group: PackageSitemapGroup, record: SitemapRecord, page: SitemapPage) => {
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
const packageReads = new Map<PackageGroup, Promise<SitemapRecord[]>>();
const packageRebuilds = new Map<PackageGroup, Promise<SitemapRecord[]>>();
const rebuildFailedAt = new Map<PackageGroup, number>();

/** Settles once no snapshot or index refresh is running (tests). */
export async function sitemapSourcesIdle(): Promise<void> {
  await Promise.all([populatedPackageSitemaps.idle(), backgroundItemEntries.idle(), backgroundListLocales.idle()]);
  while (packageRebuilds.size) await Promise.allSettled(packageRebuilds.values());
}

function manifestKey(group: PackageGroup) {
  return `sitemap-package-manifest-v3-${PAGE_SIZE}:${group}`;
}

function cachedPackages(group: PackageGroup): Promise<SitemapRecord[]> {
  const pending = packageReads.get(group);
  if (pending) return pending;
  const read = readPackages(group).finally(() => packageReads.delete(group));
  packageReads.set(group, read);
  return read;
}

async function readPackages(group: PackageGroup): Promise<SitemapRecord[]> {
  // A live rebuild reads every catalog page. Do not amplify a Redis outage with that work.
  // Missing/evicted snapshots still use the existing live rebuild below.
  const snapshot = await retrySnapshotRead(() => readSnapshot(group), group);
  // Without a complete snapshot the request waits for the catalog and fails if the API does.
  if (!snapshot) return rebuildPackages(group);
  if (snapshot.expires <= Date.now()) refreshPackages(group);
  return snapshot.records;
}

async function retrySnapshotRead<T>(read: () => Promise<T>, group: PackageGroup): Promise<T> {
  try {
    return await read();
  } catch {
    try {
      const value = await read();
      console.warn(`[opennavo] sitemap ${group} snapshot read recovered after retry`);
      return value;
    } catch {
      // Do not log storage errors verbatim: driver errors can contain connection details.
      console.warn(`[opennavo] sitemap ${group} snapshot read failed after retry; returning 503`);
      throw createError({ statusCode: 503, statusMessage: 'Sitemap temporarily unavailable' });
    }
  }
}

async function readSnapshot(group: PackageGroup) {
  const storage = useStorage('cache');
  // A rebuild that publishes between reading the manifest and its chunks reclaims those chunks:
  // read the manifest once more before treating the snapshot as lost (evicted).
  let seen: string | undefined;
  for (let attempt = 0; attempt < 2; attempt += 1) {
    const manifest = await storage.getItem<PackageSnapshot>(manifestKey(group));
    if (!manifest || manifest.keys.join() === seen) return undefined;
    const chunks = await Promise.all(manifest.keys.map(key => storage.getItem<SitemapRecord[]>(key)));
    if (chunks.every(chunk => chunk !== null)) return { records: chunks.flat(), expires: manifest.expires };
    seen = manifest.keys.join();
  }
  return undefined;
}

function rebuildPackages(group: PackageGroup): Promise<SitemapRecord[]> {
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

async function writeSnapshot(group: PackageGroup): Promise<SitemapRecord[]> {
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

/** The chunks of a snapshot: each API page, slimmed, and split again if it still exceeds CHUNK_BYTES. */
async function loadPackagePages(group: 'apps' | 'fonts'): Promise<SitemapRecord[][]> {
  const chunks: SitemapRecord[][] = [];
  // Rows can be fewer than the page size (the API filters by index policy); total counts candidates.
  for (let current = 1; ; current += 1) {
    const page = await $fetch<{ data: { total: number; records: SitemapEntry[] } }>(`${apiBase()}/sitemap/packages`, {
      query: { current, size: PAGE_SIZE, group }
    });
    // Link apps share /apps with casks (store/security plan §4); command-line tools have no web page, and
    // OpenNavo's own entry answers a 301 to /download (middleware/package-canonical.ts).
    const records = page.data.records.filter(
      record => (record.kind === 'cask' || record.kind === 'link') && record.token !== BRAND.caskToken
    );
    chunks.push(...chunkRecords(records.map(slimRecord)));
    if (current * PAGE_SIZE >= page.data.total) return chunks;
  }
}

/**
 * The record without what sitemaps never read. Each language keeps its dates and, per page, only
 * whether it is listed: overview and versions keep an explicit verdict (a missing one means listed),
 * intent pages only a positive one (a missing one means not listed). Language entries and their
 * order are kept, so listedLocales answers exactly as it would for the full record.
 */
export function slimRecord(record: SitemapEntry): SitemapRecord {
  return {
    kind: record.kind,
    token: record.token,
    ...(record.notesCount === undefined ? {} : { notesCount: record.notesCount }),
    ...(record.locales ? { locales: record.locales.map(slimLocale) } : {})
  };
}

function slimLocale(entry: SitemapLocale): ListedLocale {
  const decided = entry.pages;
  // A language without decisions (an older record) keeps every page date, as listedLocales may still list them.
  const listed = decided ? SITEMAP_PAGES.filter(page => isListed(decided, page)) : SITEMAP_PAGES;
  const pageLastmod = Object.entries(entry.pageLastmod ?? {}).filter(([key]) =>
    listed.some(page => key === page || key.startsWith(`${page}:`))
  );
  return {
    locale: entry.locale,
    lastmod: entry.lastmod,
    ...(pageLastmod.length ? { pageLastmod: Object.fromEntries(pageLastmod) } : {}),
    ...(decided
      ? {
          pages: Object.fromEntries(
            SITEMAP_PAGES.flatMap(page =>
              INTENT_PAGES.has(page)
                ? listed.includes(page)
                  ? [[page, { inSitemap: true }]]
                  : []
                : [[page, { inSitemap: listed.includes(page) }]]
            )
          )
        }
      : {})
  };
}

/** Splits records into consecutive chunks that each serialize to at most `limit` bytes. */
export function chunkRecords(records: SitemapRecord[], limit = CHUNK_BYTES): SitemapRecord[][] {
  const chunks: SitemapRecord[][] = [];
  let chunk: SitemapRecord[] = [];
  let bytes = 2;
  for (const record of records) {
    const size = Buffer.byteLength(JSON.stringify(record), 'utf8') + 1;
    if (size + 2 > limit) throw new Error(`Sitemap record ${record.token} alone exceeds the snapshot chunk limit`);
    if (chunk.length && bytes + size > limit) {
      chunks.push(chunk);
      chunk = [];
      bytes = 2;
    }
    chunk.push(record);
    bytes += size;
  }
  // An empty page still takes one chunk, as it did before chunks were split by size.
  chunks.push(chunk);
  return chunks;
}

type SitemapPage = 'overview' | 'versions' | 'alternatives' | 'uninstall' | 'troubleshooting';
const SITEMAP_PAGES: readonly SitemapPage[] = ['overview', 'versions', 'alternatives', 'uninstall', 'troubleshooting'];
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
function listedLocales<T extends ListedLocale>(record: { locales?: T[]; notesCount?: number }, page: SitemapPage) {
  if (INTENT_PAGES.has(page)) return record.locales?.filter(entry => entry.pages && isListed(entry.pages, page)) ?? [];
  const decided = record.locales?.some(entry => entry.pages);
  if (page === 'versions' && !decided && (record.notesCount ?? 0) < VERSION_PAGE_MIN_NOTES) return [];
  return record.locales?.filter(entry => !entry.pages || isListed(entry.pages, page));
}

/** One language's verdict for a page: intent pages need a positive one, other pages are listed unless told not. */
function isListed(pages: NonNullable<ListedLocale['pages']>, page: SitemapPage) {
  return INTENT_PAGES.has(page) ? pages[page]?.inSitemap === true : (pages[page]?.inSitemap ?? true);
}

/** One page in every language listedLocales gives, with the page's own content date (T5: `pageLastmod`). */
function packageEntries(
  record: { locales?: ListedLocale[]; notesCount?: number },
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

/** Settings without the list (an older API) leave the app languages open, as localizedEntries does. */
async function openLocales(): Promise<readonly Locale[]> {
  return (await getSiteSettings()).pages.openLocales ?? LOCALES;
}

function flatten(nodes: CategoryNode[]): CategoryNode[] {
  return nodes.flatMap(node => [node, ...flatten(node.children)]);
}

/**
 * F1: the label pages the API marks indexable in each open language. The index also reads this list to leave the
 * group out while it is empty, so it is kept like the package snapshot: fresh for SNAPSHOT_FRESH_MS, then served
 * stale while one refresh runs.
 */
const backgroundItemEntries = createStaleCache(
  async (): Promise<ReturnType<typeof localizedEntries>> => {
    const labels = new Map<string, Locale[]>();
    for (const locale of await openLocales())
      for (let current = 1; ; current += 1) {
        const page = await $fetch<{ data: { total: number; records: BackgroundItem[] } }>(
          `${apiBase()}/background-items`,
          { query: { locale, current, size: 100 } }
        );
        for (const item of page.data.records.filter(record => record.indexable))
          labels.set(item.label, [...(labels.get(item.label) ?? []), locale]);
        if (page.data.records.length < 100 || current * 100 >= page.data.total) break;
      }
    // No lastmod, like categories and browse: the content's checkedAt dates a verification, not a change,
    // so every re-check would move it.
    return [...labels].flatMap(([label, locales]) => localizedEntries(labelPath(label), 'background-items', locales));
  },
  {
    freshMs: SNAPSHOT_FRESH_MS,
    retryMs: SNAPSHOT_RETRY_MS,
    onError: error => console.error('[opennavo] background-items sitemap refresh failed', error)
  }
);

/**
 * Languages whose background items list is indexable: a label on its first page has a verified description (the
 * page's own rule, indexPageNoindex), so the static sitemaps list the page only there. One API page per language.
 */
const backgroundListLocales = createStaleCache(
  async (): Promise<Locale[]> => {
    const open = await openLocales();
    const pages = await Promise.all(
      open.map(locale =>
        $fetch<{ data: { records: BackgroundItem[] } }>(`${apiBase()}/background-items`, {
          query: { locale, current: 1, size: BACKGROUND_PAGE_SIZE }
        })
      )
    );
    return open.filter((_, index) => !indexPageNoindex(pages[index]!.data.records, ''));
  },
  {
    freshMs: SNAPSHOT_FRESH_MS,
    retryMs: SNAPSHOT_RETRY_MS,
    onError: error => console.error('[opennavo] background items list check failed', error)
  }
);

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
  // F1: the label pages the API marks indexable in each open language (backgroundItemEntries).
  'background-items': () => backgroundItemEntries(),
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
