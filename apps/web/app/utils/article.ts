// Article pages (WP17), pure for testing: index decisions, titles, dates and structured data. The
// API decides which languages are ready; pages in other languages show the source text, stay out
// of the index and are left out of hreflang. So do languages the site has closed for indexing (L1).
import type { PublicComponents } from '@opennavo/api';
import { LOCALE_METADATA } from '@opennavo/shared';
import type { SiteLocale as Locale } from '@opennavo/shared';
import { TITLE_SUFFIX } from './seoTitles';
import { appEntityId, listItems } from './structuredData';

type Schemas = PublicComponents['schemas'];
export type Article = Schemas['Article'];
export type ArticleCard = Schemas['ArticleCard'];
export type ArticleType = Schemas['ArticleType'];
export type ArticleAuthor = Schemas['ArticleAuthor'];

export const ARTICLE_TYPES: readonly ArticleType[] = ['recommendation', 'review', 'tutorial', 'roundup', 'news'];
/** Cards per list page (articles and author pages). */
export const ARTICLE_PAGE_SIZE = 12;
/** Same rule as the API's slug parameter; anything else is a 404 without asking the API. */
export const ARTICLE_SLUG = /^[a-z0-9]+(-[a-z0-9]+)*$/;

export function isArticleType(value: unknown): value is ArticleType {
  return typeof value === 'string' && (ARTICLE_TYPES as readonly string[]).includes(value);
}

/**
 * Languages an article is indexed in, and the ones its hreflang lists: those whose text is ready and that
 * the site has open for indexing (L1, site settings pages.openLocales).
 */
export function articleIndexedLocales(
  article: Pick<Article, 'availableLocales'>,
  openLocales: readonly Locale[]
): Locale[] {
  return article.availableLocales.filter(locale => openLocales.includes(locale));
}

/**
 * robots for an article page: languages whose text is not ready show the source text, unindexed, and a
 * language closed for indexing is unindexed even with its text ready.
 */
export function articleRobots(
  article: Pick<Article, 'availableLocales'>,
  locale: Locale,
  openLocales: readonly Locale[]
): string | undefined {
  return articleIndexedLocales(article, openLocales).includes(locale) ? undefined : 'noindex, follow';
}

/** Title tag: the search title when the editors wrote one, else the headline; always branded once. */
export function articleTitle(article: Pick<Article, 'title' | 'seoTitle'>): string {
  const title = (article.seoTitle || article.title).trim();
  return title.endsWith(TITLE_SUFFIX) ? title : `${title}${TITLE_SUFFIX}`;
}

export function articleDescription(article: Pick<Article, 'summary' | 'seoDescription'>): string | undefined {
  return article.seoDescription?.trim() || article.summary?.trim() || undefined;
}

/** Show an update date only when it falls on another day than the publication. */
export function showUpdated(article: Pick<Article, 'publishedAt' | 'updatedAt'>): boolean {
  return article.updatedAt.slice(0, 10) !== article.publishedAt.slice(0, 10);
}

/** Navo is the site's disclosed editorial byline, not an individual person. */
export function isEditorialAuthor(author: Pick<ArticleAuthor, 'slug'>): boolean {
  return author.slug === 'navo';
}

export function authorEntityId(siteUrl: string, slug: string): string {
  return `${siteUrl}/authors/${slug}#person`;
}

export interface ArticleSchemaContext {
  /** Site origin without a trailing slash. */
  siteUrl: string;
  /** Absolute image: the cover, else the share image. */
  image?: string;
  /** Language of the page, including source-text fallback pages. */
  pageLocale?: Locale;
  /** Path of the author page in the page language. */
  authorPath?: string;
  /** Path of an app page in the page language. */
  appPath: (token: string) => string;
}

/**
 * BlogPosting input. The byline is the named author (a Person with their author page) or the
 * editorial team, which is the site Organization. Apps are named by their stable entity IDs: about
 * for a single-app article, mentions for every app the page shows.
 */
export function blogPostingNode(article: Article, context: ArticleSchemaContext): Record<string, unknown> {
  const { siteUrl } = context;
  const node: Record<string, unknown> = {
    '@type': 'BlogPosting',
    headline: article.title,
    datePublished: article.publishedAt,
    dateModified: article.updatedAt,
    inLanguage: LOCALE_METADATA[context.pageLocale ?? article.locale].hreflang,
    publisher: { '@id': `${siteUrl}/#identity` },
    author:
      article.author && !isEditorialAuthor(article.author)
        ? {
            '@type': 'Person',
            '@id': authorEntityId(siteUrl, article.author.slug),
            name: article.author.name,
            ...(context.authorPath ? { url: `${siteUrl}${context.authorPath}` } : {})
          }
        : { '@id': `${siteUrl}/#identity` }
  };
  const description = articleDescription(article);
  if (description) node.description = description;
  if (context.image) node.image = context.image;
  const apps = article.packages.map(pkg => ({
    '@type': 'SoftwareApplication',
    '@id': appEntityId(siteUrl, pkg.token),
    name: pkg.displayName,
    url: `${siteUrl}${context.appPath(pkg.token)}`
  }));
  if (apps.length === 1) node.about = apps[0];
  if (apps.length) node.mentions = apps;
  return node;
}

/** Roundups list their apps as an ItemList in the order the body shows the cards. */
export function roundupItems(
  article: Article,
  tokens: readonly string[],
  context: Pick<ArticleSchemaContext, 'siteUrl' | 'appPath'>
): Record<string, unknown>[] | null {
  if (article.type !== 'roundup') return null;
  const byToken = new Map(article.packages.map(pkg => [pkg.token, pkg]));
  const shown = [...new Set(tokens)].flatMap(token => {
    const pkg = byToken.get(token);
    return pkg ? [{ name: pkg.displayName, url: `${context.siteUrl}${context.appPath(pkg.token)}` }] : [];
  });
  return shown.length >= 2 ? listItems(shown) : null;
}
