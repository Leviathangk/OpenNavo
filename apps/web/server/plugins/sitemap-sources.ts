import type { PublicComponents } from '@opennavo/api';
import { LOCALES, VERSION_PAGE_MIN_NOTES } from '@opennavo/shared';
import { localizedEntries } from '../utils/sitemapEntries';

type SitemapEntry = PublicComponents['schemas']['SitemapEntry'];

interface CategoryNode {
  slug: string;
  hiddenByDefault: boolean;
  children: CategoryNode[];
}

const PAGE_SIZE = 5000;

// Dynamic sitemap groups (WP3) are injected through @nuxtjs/sitemap's sitemap:input hook rather than
// HTTP sources: the module turns a failed source into a shorter 200 response, which crawlers read
// as removed URLs. An API error here fails the request instead, so crawlers retry later.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('sitemap:input', async ctx => {
    const load = GROUPS[ctx.sitemapName];
    if (load) ctx.urls.push(...(await load()));
  });
});

function apiBase() {
  const config = useRuntimeConfig();
  return (config.apiBaseInternal || config.public.apiBase).replace(/\/$/, '');
}

async function packages(group?: 'apps' | 'fonts'): Promise<SitemapEntry[]> {
  const records: SitemapEntry[] = [];
  // Rows can be fewer than the page size (the API filters by index policy); total counts candidates.
  for (let current = 1; ; current += 1) {
    const page = await $fetch<{ data: { total: number; records: SitemapEntry[] } }>(`${apiBase()}/sitemap/packages`, {
      query: { current, size: PAGE_SIZE, ...(group ? { group } : {}) }
    });
    records.push(...page.data.records.filter(record => record.kind === 'cask'));
    if (current * PAGE_SIZE >= page.data.total) return records;
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
  apps: async () => (await packages('apps')).flatMap(record => packageEntries(record, `/apps/${record.token}`, 'apps')),
  fonts: async () =>
    (await packages('fonts')).flatMap(record => packageEntries(record, `/apps/${record.token}`, 'fonts')),
  // Version pages only with enough release notes, and only while enabled in admin.
  versions: async () => {
    if (!(await getSiteSettings()).pages.versionsSitemap) return [];
    return (await packages())
      .filter(record => (record.notesCount ?? 0) >= VERSION_PAGE_MIN_NOTES)
      .flatMap(record => packageEntries(record, `/apps/${record.token}/versions`, 'versions'));
  },
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
      const page = await $fetch<{ data: { total: number; records: { slug: string; updatedAt: string }[] } }>(
        `${apiBase()}/collections`,
        { query: { current, size: 100 } }
      );
      urls.push(
        ...page.data.records.flatMap(item =>
          localizedEntries(`/collections/${item.slug}`, 'collections', LOCALES, () => item.updatedAt)
        )
      );
      if (page.data.records.length < 100 || current * 100 >= page.data.total) return urls;
    }
  }
};
