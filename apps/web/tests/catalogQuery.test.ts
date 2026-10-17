import { describe, expect, it } from 'vitest';
import {
  CATALOG_SORTS,
  canonicalPageUrl,
  GRID_PAGE_SIZE,
  type ListQueryParam,
  mergeQuery,
  pageCount,
  parsePage,
  readEnum,
  readFlag,
  readGridPageSize,
  readPage,
  readSearchQuery
} from '../app/utils/catalogQuery';
import { findCategoryPath, flattenCategories } from '../app/utils/categories';
import type { CategoryNode } from '../app/utils/categories';

describe('Query parameters', () => {
  it('Page numbers, enums, switches, search terms', () => {
    expect(readPage('3')).toBe(3);
    expect(readPage(['2', '5'])).toBe(2);
    expect(readPage('0')).toBe(1);
    expect(readPage('-2')).toBe(1);
    expect(readPage('1.5')).toBe(1);
    expect(readPage(undefined)).toBe(1);
    expect(readEnum('name', CATALOG_SORTS, 'popular')).toBe('name');
    expect(readEnum('drop table', CATALOG_SORTS, 'popular')).toBe('popular');
    expect(readFlag('1')).toBe(true);
    expect(readFlag('true')).toBe(false);
    expect(readSearchQuery('  vscode  ')).toBe('vscode');
    expect(readSearchQuery('字'.repeat(80))).toHaveLength(64);
  });

  it('List pages accept only page numbers as list links write them', () => {
    expect(parsePage(undefined)).toBe(1);
    expect(parsePage('1')).toBe(1);
    expect(parsePage('2')).toBe(2);
    expect(parsePage(['7'])).toBe(7);
    expect(parsePage('999999')).toBe(999999);
    for (const value of ['abc', '0', '-1', '1.5', '01', '+2', ' 2', '2 ', '', '1e3', '1000000', null, ['2', '3'], []])
      expect(parsePage(value as Parameters<typeof parsePage>[0]), JSON.stringify(value)).toBeUndefined();
    expect(pageCount(0, 24)).toBe(1);
    expect(pageCount(24, 24)).toBe(1);
    expect(pageCount(25, 24)).toBe(2);
    expect(pageCount(50, 24)).toBe(3);
  });

  // What useListPage records: every list keeps page; grid lists add size, apps and categories disabled.
  const LIST: ListQueryParam[] = ['page'];
  const RECENT: ListQueryParam[] = ['page', 'size'];
  const GRID: ListQueryParam[] = ['page', 'size', 'disabled'];

  it('Canonical and alternate URLs of a list keep page from page 2 on', () => {
    expect(canonicalPageUrl('https://opennavo.com/apps?page=1', GRID)).toBe('https://opennavo.com/apps');
    expect(canonicalPageUrl('https://opennavo.com/zh/rankings?page=2', LIST)).toBe(
      'https://opennavo.com/zh/rankings?page=2'
    );
    expect(canonicalPageUrl('https://opennavo.com/apps?page=abc', GRID)).toBe('https://opennavo.com/apps');
    expect(canonicalPageUrl('https://opennavo.com/apps?page=2&page=3', GRID)).toBe('https://opennavo.com/apps');
    expect(canonicalPageUrl('https://opennavo.com/articles?type=tutorial&page=1', LIST)).toBe(
      'https://opennavo.com/articles?type=tutorial'
    );
    expect(canonicalPageUrl('https://opennavo.com/apps/nextpad++/versions?page=2', LIST)).toBe(
      'https://opennavo.com/apps/nextpad++/versions?page=2'
    );
    expect(canonicalPageUrl('https://opennavo.com/apps/nextpad++')).toBe('https://opennavo.com/apps/nextpad++');
    expect(canonicalPageUrl('https://opennavo.com/')).toBe('https://opennavo.com/');
  });

  it('Pages that are not paginated lists drop page, size and disabled', () => {
    for (const url of [
      'https://opennavo.com?page=2',
      'https://opennavo.com/?page=2&size=12&disabled=1',
      'https://opennavo.com/zh?page=3',
      'https://opennavo.com/apps/wechat?page=3',
      'https://opennavo.com/discover?page=2',
      'https://opennavo.com/categories?page=2&size=12',
      'https://opennavo.com/articles/choose-a-screenshot-app?page=2',
      '/download?disabled=1&size=30&page=9'
    ]) {
      expect(canonicalPageUrl(url), url).toBe(url.slice(0, url.indexOf('?')));
      expect(canonicalPageUrl(url, []), url).toBe(url.slice(0, url.indexOf('?')));
    }
  });

  it('The site root keeps its slash before a query', () => {
    expect(canonicalPageUrl('https://opennavo.com?page=2', LIST)).toBe('https://opennavo.com/?page=2');
    expect(canonicalPageUrl('https://opennavo.com?ref=x&page=1', LIST)).toBe('https://opennavo.com/?ref=x');
    expect(canonicalPageUrl('http://localhost:3000?page=2', LIST)).toBe('http://localhost:3000/?page=2');
    expect(canonicalPageUrl('https://opennavo.com/?page=2', LIST)).toBe('https://opennavo.com/?page=2');
    expect(canonicalPageUrl('https://opennavo.com/zh?page=2', LIST)).toBe('https://opennavo.com/zh?page=2');
    expect(canonicalPageUrl('https://opennavo.com')).toBe('https://opennavo.com');
  });

  it('Canonical URLs preserve filters that expand pagination, normalized like API queries', () => {
    for (const path of ['/apps', '/zh/apps', '/categories/utilities', '/ja/categories/utilities']) {
      expect(canonicalPageUrl(`https://example.test${path}?page=4&disabled=1`, GRID)).toBe(
        `https://example.test${path}?page=4&disabled=1`
      );
      expect(canonicalPageUrl(`${path}?page=1&disabled=1&disabled=0`, GRID)).toBe(`${path}?disabled=1`);
      expect(canonicalPageUrl(`${path}?page=2&disabled=0`, GRID)).toBe(`${path}?page=2`);
    }
    // Lists without the filter, and pages that are not lists, drop it.
    expect(canonicalPageUrl('/apps/example/versions?page=2&disabled=1', LIST)).toBe('/apps/example/versions?page=2');
    expect(canonicalPageUrl('/recent?page=2&disabled=1', RECENT)).toBe('/recent?page=2');
    expect(canonicalPageUrl('/apps/example?page=2&disabled=1')).toBe('/apps/example');
  });

  it('Grid page sizes and canonical URLs use the same bounds and defaults', () => {
    expect(GRID_PAGE_SIZE).toBe(24);
    for (const [value, expected] of [
      ['1', 1],
      ['100', 100],
      ['012', 12],
      ['12.0', 12],
      [['12'], 12],
      [undefined, 24],
      [null, 24],
      ['', 24],
      ['0', 24],
      ['101', 24],
      ['1.5', 24],
      ['abc', 24],
      [['12', '12'], 24]
    ] as const) {
      expect(readGridPageSize(value as Parameters<typeof readGridPageSize>[0])).toBe(expected);
    }
    for (const [path, keep] of [
      ['/apps', GRID],
      ['/zh/apps', GRID],
      ['/recent', RECENT],
      ['/ja/recent', RECENT],
      ['/categories/utilities', GRID],
      ['/ru/categories/utilities', GRID]
    ] as const) {
      for (const size of ['1', '12', '100']) {
        expect(canonicalPageUrl(`${path}?page=4&size=${size}`, keep)).toBe(`${path}?page=4&size=${size}`);
      }
      expect(canonicalPageUrl(`${path}?page=1&size=012`, keep)).toBe(`${path}?size=12`);
      for (const size of ['24', '0', '101', 'abc', '12&size=12']) {
        expect(canonicalPageUrl(`${path}?page=2&size=${size}`, keep)).toBe(`${path}?page=2`);
      }
    }
    for (const path of ['/articles', '/rankings', '/collections', '/authors/lin-wei', '/apps/example/versions']) {
      expect(canonicalPageUrl(`${path}?page=2&size=12`, LIST)).toBe(`${path}?page=2`);
    }
    for (const path of ['/apps/example', '/categories']) {
      expect(canonicalPageUrl(`${path}?page=2&size=12`)).toBe(path);
    }
  });

  it('Merging removes defaults and filter changes can reset page', () => {
    const defaults = { page: 1, sort: 'popular', kind: 'all', disabled: false };
    expect(mergeQuery({ page: '3', sort: 'name' }, { kind: 'cask', page: undefined }, defaults)).toEqual({
      sort: 'name',
      kind: 'cask'
    });
    expect(mergeQuery({ kind: 'cask' }, { sort: 'popular', disabled: true }, defaults)).toEqual({
      kind: 'cask',
      disabled: '1'
    });
    expect(mergeQuery({ q: 'vscode', page: '2' }, { page: 1 }, defaults)).toEqual({ q: 'vscode' });
  });
});

describe('Category tree', () => {
  const node = (slug: string, children: CategoryNode[] = []): CategoryNode => ({
    slug,
    name: slug,
    icon: 'lucide:code-xml',
    appliesTo: 'both',
    hiddenByDefault: false,
    packageCount: 1,
    children
  });
  const tree = [node('developer-tools', [node('editors'), node('terminal')]), node('design')];

  it('Find paths and flatten', () => {
    expect(findCategoryPath(tree, 'terminal').map(item => item.slug)).toEqual(['developer-tools', 'terminal']);
    expect(findCategoryPath(tree, 'missing')).toEqual([]);
    expect(flattenCategories(tree).map(({ node: item, depth }) => `${depth}:${item.slug}`)).toEqual([
      '0:developer-tools',
      '1:editors',
      '1:terminal',
      '0:design'
    ]);
  });
});
