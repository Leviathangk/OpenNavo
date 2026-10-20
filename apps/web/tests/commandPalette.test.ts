import { describe, expect, it } from 'vitest';
import { paletteGroups } from '../app/utils/commandPalette';
import type { SuggestItem } from '../app/utils/commandPalette';

// Web Command-K: what is listed for the typed text, and in what order. The first row of the first group is what Enter
// does, so it must be "all results for the text".
const t = (key: string, params?: Record<string, unknown>) =>
  params ? `${key}(${Object.values(params).join(',')})` : key;

const app = (token: string, kind = 'cask'): SuggestItem => ({
  type: 'package',
  kind: kind as SuggestItem['kind'],
  token,
  name: token.toUpperCase(),
  summary: `${token} summary`,
  iconUrl: null
});
const category: SuggestItem = { type: 'category', slug: 'office', name: 'Office' };

describe('Web command palette groups', () => {
  it('Lists the Go to group first, and all results for the text at its top', () => {
    const groups = paletteGroups('office', [app('libreoffice'), category, app('wps-office')], t);
    expect(groups.map(group => group.key)).toEqual(['jump', 'packages']);
    expect(groups[0]!.items[0]).toMatchObject({ key: 'search:office', label: 'palette.searchAll(office)' });
    // Then the matching categories, then the pages; the apps come after the whole group.
    expect(groups[0]!.items.map(item => item.key)).toEqual(['search:office', 'category:office']);
    expect(groups[1]!.items.map(item => item.key)).toEqual(['package:cask:libreoffice', 'package:cask:wps-office']);
  });

  it('Keeps the all-results row on top while no suggestion has arrived', () => {
    const groups = paletteGroups('  office  ', [], t);
    expect(groups[0]!.items.map(item => item.key)).toEqual(['search:office']);
    expect(groups[1]!.items).toEqual([]);
  });

  it('Lists link apps, and leaves out command-line tools', () => {
    const groups = paletteGroups('app', [app('a-cask'), app('a-link', 'link'), app('a-tool', 'formula')], t);
    expect(groups[1]!.items.map(item => item.key)).toEqual(['package:cask:a-cask', 'package:link:a-link']);
  });

  it('Without text lists only the pages, with no search row', () => {
    const [jump, packages] = paletteGroups('   ', [], t);
    expect(jump!.items.every(item => item.key.startsWith('page:'))).toBe(true);
    expect(jump!.items).toHaveLength(7);
    expect(packages!.items).toEqual([]);
  });

  it('Finds a page by its English key, after the all-results row', () => {
    const [jump] = paletteGroups('rank', [], t);
    expect(jump!.items.map(item => item.key)).toEqual(['search:rank', 'page:/rankings']);
  });
});
