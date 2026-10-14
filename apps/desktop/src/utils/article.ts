import type { PublicComponents } from '@opennavo/api';

export type Article = PublicComponents['schemas']['Article'];
export type ArticleCard = PublicComponents['schemas']['ArticleCard'];
export type ArticleType = PublicComponents['schemas']['ArticleType'];
export const ARTICLE_TYPES: readonly ArticleType[] = ['recommendation', 'review', 'tutorial', 'roundup', 'news'];
export const ARTICLE_PAGE_SIZE = 12;
export const ARTICLE_SLUG = /^[a-z0-9]+(-[a-z0-9]+)*$/;

export function articleType(value: unknown): ArticleType | undefined {
  return typeof value === 'string' && ARTICLE_TYPES.includes(value as ArticleType) ? (value as ArticleType) : undefined;
}

export function articlePage(value: unknown): number {
  const page = typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : 1;
  return Number.isSafeInteger(page) && page > 0 ? page : 1;
}
