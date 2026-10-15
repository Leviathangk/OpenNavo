import { randomUUID } from 'node:crypto';
import type { PublicComponents } from '@opennavo/api';
import { LOCALES, VERSION_PAGE_MIN_NOTES } from '@opennavo/shared';
import { packageSitemapName, packageSitemaps, type PackageSitemapGroup } from '../../shared/utils/sitemapBuckets';
import { localizedEntries } from '../utils/sitemapEntries';

type SitemapEntry = PublicComponents['schemas']['SitemapEntry'];

interface CategoryNode {
  slug: string;
  hiddenByDefault: boolean;
  children: CategoryNode[];
}

// Split completed catalog snapshots below the 512 KiB storage limit.
const PAGE_SIZE = 500;

// Dynamic sitemap groups (WP3) are injected through @nuxtjs/sitemap's sitemap:input hook rather than
// HTTP sources: the module turns a failed source into a shorter 200 response, which crawlers read
// as removed URLs. An API error here fails the request instead, so crawlers retry later.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('sitemap:index-resolved', async ctx => {
    // Non-chunked groups are listed even when empty. Filter the rendered index,
    // without mutating its cached entries, so newly published content can reappear.
    // Let API failures propagate: an outage must not look like removed content.
    const { articles, authors } = await articleSitemap();
    const [apps, fonts, settings] = await Promise.all([
      cachedPackages('apps'),
      cachedPackages('fonts'),
      getSiteSettings()
    ]);
    const populated = new Set([
      ...apps.filter(record => record.locales?.length !== 0).map(record => packageSitemapName('apps', record.token)),
      ...fonts.filter(record => record.locales?.length !== 0).map(record => packageSitemapName('fonts', record.token)),
      ...(settings.pages.versionsSitemap
        ? [...apps, ...fonts]
            .filter(record => record.locales?.length !== 0 && (record.notesCount ?? 0) >= VERSION_PAGE_MIN_NOTES)
            .map(record => packageSitemapName('versions', record.token))
        : [])
    ]);
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
  expires: number;
}

// Coalesce the entire read/repair cycle, including callers that observed missing chunks.
// Each server has its own in-flight map; shared Redis snapshots remain immutable.
const packageLoads = new Map<PackageGroup, Promise<SitemapEntry[]>>();

function cachedPackages(group: PackageGroup): Promise<SitemapEntry[]> {
  const pending = packageLoads.get(group);
  if (pending) return pending;
  const load = readOrRebuildPackages(group).finally(() => packageLoads.delete(group));
  packageLoads.set(group, load);
  return load;
}

async function readOrRebuildPackages(group: PackageGroup): Promise<SitemapEntry[]> {
  const storage = useStorage('cache');
  const manifestKey = `sitemap-package-manifest-v3-${PAGE_SIZE}:${group}`;
  const previous = await storage.getItem<PackageSnapshot>(manifestKey);
  if (previous && previous.expires > Date.now()) {
    const chunks = await Promise.all(previous.keys.map(key => storage.getItem<SitemapEntry[]>(key)));
    if (chunks.every(chunk => chunk !== null)) return chunks.flat();
  }

  // Reload every OFFSET page and publish only after all immutable chunks are stored.
  const pages = await loadPackagePages(group);
  const version = randomUUID();
  const keys = pages.map((_, index) => `sitemap-package-snapshots-v2:${group}:${version}:${index}`);
  try {
    // Sequential writes ensure no writes remain in flight when failure cleanup starts.
    for (const [index, page] of pages.entries()) {
      await storage.setItem(keys[index]!, page, { ttl: 120 });
      if (!(await storage.hasItem(keys[index]!))) {
        throw new Error('Sitemap snapshot chunk was not stored');
      }
    }
    // Keep the manifest until replaced so even a TTL-less driver can reclaim its chunks.
    await storage.setItem(manifestKey, { keys, expires: Date.now() + 60000 });
  } catch (error) {
    await Promise.all(keys.map(key => storage.removeItem(key)));
    throw error;
  }
  // Explicit reclamation is required: the default memory driver ignores TTL.
  // Readers racing this cleanup rebuild instead of returning a partial catalog.
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
    pages.push(page.data.records.filter(record => record.kind === 'cask'));
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
