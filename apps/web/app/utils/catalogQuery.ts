// List/category/ranking/search query parameters (05 §4: page/sort participate in cache keys); pure functions for tests.

export type CatalogSort = 'popular' | 'updated' | 'name';
export type RankingPeriod = '30d' | '90d' | '365d';

export const CATALOG_SORTS: readonly CatalogSort[] = ['popular', 'updated', 'name'];
export const RANKING_PERIODS: readonly RankingPeriod[] = ['30d', '90d', '365d'];

type QueryValue = string | null | (string | null)[] | undefined;

function first(value: QueryValue): string | undefined {
  const item = Array.isArray(value) ? value[0] : value;
  return item ?? undefined;
}

/** Page number: positive integer, otherwise one. Lenient, for client-side helpers and search. */
export function readPage(value: QueryValue): number {
  const text = first(value);
  if (!text || !/^\d{1,6}$/.test(text)) return 1;
  return Math.max(1, Number(text));
}

const PAGE_NUMBER = /^[1-9]\d{0,5}$/;

/**
 * The page a paginated list URL names (05 §6.1): 1 without ?page=, the number for a page number
 * written the way list links write it, and undefined for anything else (abc, 0, 01, 1.5, an empty
 * or repeated parameter), which list pages answer with 404.
 */
export function parsePage(value: QueryValue): number | undefined {
  if (value === undefined) return 1;
  const text = Array.isArray(value) ? (value.length === 1 ? value[0] : undefined) : value;
  return typeof text === 'string' && PAGE_NUMBER.test(text) ? Number(text) : undefined;
}

import { GRID_PAGE_SIZE } from '#shared/utils/gridPage';
import { canonicalQuery } from '#shared/utils/routeQuery';

export { GRID_PAGE_SIZE };

/** Pages of a list; an empty list still has its first page. */
export function pageCount(total: number, size: number): number {
  return Math.max(1, Math.ceil(total / size));
}

/**
 * Canonical and alternate URLs keep only what the route's query contract lists (shared/utils/routeQuery.ts,
 * S4): page from page 2 on, and sorts, filters and flags that change the list, without their defaults, in
 * contract order. Every other parameter (utm_*, ref, an unknown key) is dropped, and routes that are not
 * lists keep none. The site root is written with its slash when a query follows (https://host/?…).
 */
export function canonicalPageUrl(url: string): string {
  const at = url.indexOf('?');
  if (at < 0) return url;
  const base = url.slice(0, at);
  const path = base.replace(/^[a-z][\w+.-]*:\/\/[^/]+/i, '') || '/';
  const rest = canonicalQuery(path, new URLSearchParams(url.slice(at + 1)));
  if (!rest) return base;
  return /^[a-z][\w+.-]*:\/\/[^/]+$/i.test(base) ? `${base}/?${rest}` : `${base}?${rest}`;
}

export function readEnum<T extends string>(value: QueryValue, allowed: readonly T[], fallback: T): T {
  const text = first(value);
  return text !== undefined && (allowed as readonly string[]).includes(text) ? (text as T) : fallback;
}

export function readFlag(value: QueryValue): boolean {
  return first(value) === '1';
}

/** Trim search terms and cap at 64 characters, matching API limits. */
export function readSearchQuery(value: QueryValue): string {
  return Array.from((first(value) ?? '').trim())
    .slice(0, 64)
    .join('');
}

/**
 * Merge query parameters, removing defaults/undefined/false for concise URLs and stable cache keys.
 * When changing filters, callers also set page undefined to return to page one.
 */
export function mergeQuery(
  current: Record<string, QueryValue>,
  patch: Record<string, string | number | boolean | undefined>,
  defaults: Record<string, string | number | boolean>
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(current)) {
    const text = first(value);
    if (text !== undefined && !(key in patch)) result[key] = text;
  }
  for (const [key, value] of Object.entries(patch)) {
    if (value === undefined || value === false || value === defaults[key]) continue;
    result[key] = value === true ? '1' : String(value);
  }
  for (const [key, value] of Object.entries(defaults)) {
    if (result[key] === (value === true ? '1' : String(value))) delete result[key];
  }
  return result;
}
