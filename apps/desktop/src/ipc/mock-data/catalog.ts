// Simulated catalog commands (simplified 06 §7.1 ranking): exact names first, then text relevance multiplied by popularity.
// Fonts ×0.5, libraries ×0.7. Browser mode/component tests only; production uses Rust FTS5.
import type { CategoryItem, Kind, ListQuery, Locale, Page, SearchQuery } from '../bindings';
import type { LocalItem, LocalSearchHit } from '../client';
import { catalogCategories, catalogItems as caskItems } from './fixtures';
import links from './links.json';
import { pick } from '@opennavo/shared';

// Link apps (store/security plan §4) come from their own feed in Rust; here they simply join the casks.
// Read the cask list on every call: tests add fixtures to it at runtime.
export const linkItems = links.items as unknown as LocalItem[];
const allItems = (): LocalItem[] => [...caskItems, ...linkItems];

const visible = (item: LocalItem, includeDisabled: boolean) => !item.hidden && (includeDisabled || !item.disabled);

function names(item: LocalItem): string[] {
  return [item.token, item.name, ...item.names, ...Object.values(item.displayName), ...item.binaries].filter(Boolean);
}

// Same rule as Rust: kind cask with includeLinks also returns link apps, never command-line tools.
const kindMatches = (item: LocalItem, query: { kind: Kind | null; includeLinks?: boolean }) =>
  !query.kind ||
  item.kind === query.kind ||
  (query.kind === 'cask' && Boolean(query.includeLinks) && item.kind === 'link');

// Ignore case, spaces, and hyphens: vscode matches VS Code.
const normalize = (value: string) => value.toLowerCase().replace(/[\s-]+/g, '');

function matchScore(item: LocalItem, q: string, locale: Locale): { score: number; matchedName: string | null } {
  const query = normalize(q);
  const display = normalize(pick(item.displayName, locale, item.sourceLocale, item.name));
  let best = 0;
  let matchedName: string | null = null;
  for (const name of names(item)) {
    const value = normalize(name);
    const score = value === query ? 1000 : value.startsWith(query) ? 500 : value.includes(query) ? 200 : 0;
    if (score > best) {
      best = score;
      matchedName = value === display ? null : name;
    }
  }
  if (!best) {
    const text = normalize([...Object.values(item.summary), item.pinyin, ...item.tags].join(' '));
    if (text.includes(query)) best = 50;
  }
  if (!best) return { score: 0, matchedName: null };
  const weight = item.isFont ? 0.5 : item.isLibrary ? 0.7 : 1;
  return { score: (best + Math.log10(item.installs30d + 1) * 10) * weight, matchedName };
}

export function searchCatalog(query: SearchQuery, locale: Locale = 'en-US'): Page<LocalSearchHit> {
  const q = query.q.trim();
  if (!q) return { total: 0, items: [] };
  const hits = allItems()
    .filter(item => visible(item, query.includeDisabled))
    .filter(item => kindMatches(item, query))
    .filter(item => !query.category || inCategory(item, query.category))
    .map(item => ({ item, ...matchScore(item, q, locale) }))
    .filter(hit => hit.score > 0)
    .sort((a, b) => b.score - a.score);
  return {
    total: hits.length,
    items: hits
      .slice(query.offset, query.offset + query.limit)
      .map(hit => ({ item: hit.item, score: hit.score, matchedName: hit.matchedName }))
  };
}

/** Categories include descendants: any item category equals slug or has slug as an ancestor. */
function inCategory(item: LocalItem, slug: string): boolean {
  return item.categories.some(category => category === slug || ancestors(category).includes(slug));
}

function ancestors(slug: string): string[] {
  const out: string[] = [];
  let current = catalogCategories.find(category => category.slug === slug)?.parent ?? null;
  while (current) {
    out.push(current);
    current = catalogCategories.find(category => category.slug === current)?.parent ?? null;
  }
  return out;
}

const SORTS: Record<Exclude<ListQuery['sort'], 'name'>, (a: LocalItem, b: LocalItem) => number> = {
  popular: (a, b) => (b.popularity ?? 0) - (a.popularity ?? 0),
  updated: (a, b) => b.versionChangedAt.localeCompare(a.versionChangedAt),
  installs30d: (a, b) => b.installs30d - a.installs30d,
  // Sort missing 90/365-day data from older snapshots (NULL) last (06 §8).
  installs90d: (a, b) => (b.installs90d ?? -1) - (a.installs90d ?? -1),
  installs365d: (a, b) => (b.installs365d ?? -1) - (a.installs365d ?? -1)
};

export function listCatalog(query: ListQuery, locale: Locale = 'en-US'): Page<LocalItem> {
  const collator = new Intl.Collator(locale);
  const items = allItems()
    .filter(item => visible(item, query.includeDisabled))
    .filter(item => kindMatches(item, query))
    .filter(item => !query.category || inCategory(item, query.category))
    .filter(item => query.includeFonts || !item.isFont)
    .filter(item => query.includeLibraries || !item.isLibrary || query.category === 'libraries')
    .sort(
      query.sort === 'name'
        ? (a, b) =>
            collator.compare(
              pick(a.displayName, locale, a.sourceLocale, a.name),
              pick(b.displayName, locale, b.sourceLocale, b.name)
            )
        : SORTS[query.sort]
    );
  return { total: items.length, items: items.slice(query.offset, query.offset + query.limit) };
}

export function getCatalogItem(kind: Kind, token: string): LocalItem | null {
  return allItems().find(item => item.kind === kind && item.token === token && !item.hidden) ?? null;
}

export function listCategories(locale: Locale): CategoryItem[] {
  return catalogCategories
    .map(category => ({
      slug: category.slug,
      parent: category.parent,
      name: pick(category.name, locale, category.sourceLocale, category.slug),
      icon: category.icon,
      appliesTo: category.appliesTo,
      hiddenByDefault: category.hiddenByDefault,
      sort: category.sort,
      packageCount: allItems().filter(item => !item.hidden && inCategory(item, category.slug)).length
    }))
    .sort((a, b) => a.sort - b.sort);
}
