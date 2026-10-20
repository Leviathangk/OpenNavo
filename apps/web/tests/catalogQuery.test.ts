import { describe, expect, it } from 'vitest';
import {
  CATALOG_SORTS,
  canonicalPageUrl,
  GRID_PAGE_SIZE,
  mergeQuery,
  pageCount,
  parsePage,
  readEnum,
  readFlag,
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

  it('Canonical and alternate URLs follow the route query contract (S4)', () => {
    expect(canonicalPageUrl('https://opennavo.com/apps?page=1')).toBe('https://opennavo.com/apps');
    expect(canonicalPageUrl('https://opennavo.com/zh/rankings?page=2')).toBe('https://opennavo.com/zh/rankings?page=2');
    expect(canonicalPageUrl('https://opennavo.com/apps?page=abc')).toBe('https://opennavo.com/apps');
    expect(canonicalPageUrl('https://opennavo.com/apps?page=2&page=3')).toBe('https://opennavo.com/apps');
    expect(canonicalPageUrl('https://opennavo.com/articles?type=tutorial&page=1')).toBe(
      'https://opennavo.com/articles?type=tutorial'
    );
    expect(canonicalPageUrl('https://opennavo.com/apps/nextpad++/versions?page=2')).toBe(
      'https://opennavo.com/apps/nextpad++/versions?page=2'
    );
    expect(canonicalPageUrl('https://opennavo.com/apps?utm_source=x&sort=name&page=2&disabled=1')).toBe(
      'https://opennavo.com/apps?page=2&sort=name&disabled=1'
    );
    expect(canonicalPageUrl('https://opennavo.com/apps/nextpad++')).toBe('https://opennavo.com/apps/nextpad++');
    expect(canonicalPageUrl('https://opennavo.com/')).toBe('https://opennavo.com/');
  });

  it('Pages that are not paginated lists keep no parameters', () => {
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
    }
  });

  it('The site root drops its query, as a page without parameters', () => {
    expect(canonicalPageUrl('https://opennavo.com?page=2')).toBe('https://opennavo.com');
    expect(canonicalPageUrl('https://opennavo.com?ref=x&page=1')).toBe('https://opennavo.com');
    expect(canonicalPageUrl('https://opennavo.com')).toBe('https://opennavo.com');
    expect(canonicalPageUrl('https://opennavo.com/zh?page=2')).toBe('https://opennavo.com/zh');
  });

  it('Canonical URLs preserve filters that expand pagination, normalized like API queries', () => {
    for (const path of ['/apps', '/zh/apps', '/categories/utilities', '/ja/categories/utilities']) {
      expect(canonicalPageUrl(`https://example.test${path}?page=4&disabled=1`)).toBe(
        `https://example.test${path}?page=4&disabled=1`
      );
      expect(canonicalPageUrl(`${path}?page=1&disabled=1&disabled=0`)).toBe(path);
      expect(canonicalPageUrl(`${path}?page=1&disabled=1`)).toBe(`${path}?disabled=1`);
      expect(canonicalPageUrl(`${path}?page=2&disabled=0`)).toBe(`${path}?page=2`);
    }
    // Lists without the filter, and pages that are not lists, drop it.
    expect(canonicalPageUrl('/apps/example/versions?page=2&disabled=1')).toBe('/apps/example/versions?page=2');
    expect(canonicalPageUrl('/recent?page=2&disabled=1&size=12')).toBe('/recent?page=2');
    expect(canonicalPageUrl('/apps/example?page=2&disabled=1')).toBe('/apps/example');
  });

  it('Grid lists have one page size on the web (S2)', () => {
    expect(GRID_PAGE_SIZE).toBe(24);
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
