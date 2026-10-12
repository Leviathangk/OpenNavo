import { describe, expect, it } from 'vitest';
import { LOCALES } from '@opennavo/shared';
import { catalogBaseSnapshot, catalogSnapshotInfo, catalogTextPacks } from '@/ipc/mock-data/snapshot';
import { catalogItems } from '@/ipc/mock-data/fixtures';
import { searchCatalog } from '@/ipc/mock-data/catalog';

describe('Snapshot v2 browser fixtures', () => {
  it('Base data excludes localized text; metadata and six language packs share cursors', () => {
    expect(catalogSnapshotInfo.formatVersion).toBe(2);
    expect(catalogSnapshotInfo.itemCount).toBe(catalogBaseSnapshot.items.length);
    expect(catalogBaseSnapshot.items.length).toBeGreaterThan(0);
    for (const item of catalogBaseSnapshot.items) {
      expect(item.kind).toBe('cask');
      expect(item.sourceLocale).toBe('en-US');
      expect(item).not.toHaveProperty('displayName');
      expect(item).not.toHaveProperty('summary');
    }
    for (const category of catalogBaseSnapshot.categories) {
      expect(category.sourceLocale).toBe('zh-CN');
      expect(category).not.toHaveProperty('name');
    }
    for (const locale of LOCALES) {
      const pack = catalogTextPacks[locale];
      expect(pack).toMatchObject({ formatVersion: 2, locale, cursor: catalogBaseSnapshot.cursor });
      expect(catalogSnapshotInfo.textPacks[locale]).toMatchObject({
        locale,
        version: pack.version,
        cursor: pack.cursor
      });
      for (const item of pack.items) {
        const original = catalogItems.find(source => source.token === item.token)!;
        if (item.displayName) expect(item.displayName).toBe(original.displayName[locale]);
        if (item.summary) expect(item.summary).toBe(original.summary[locale]);
        expect(Object.values(item)).not.toContain(null);
      }
    }
    for (const item of catalogItems) {
      expect(Object.values(item.displayName)).not.toContain(null);
      expect(Object.values(item.summary)).not.toContain(null);
    }
  });
  it('Sparse six-language text is searchable and names follow current locale rather than fixed Chinese/English', () => {
    const original = catalogItems[0]!;
    const fixture = { ...original, token: 'locale-test', displayName: { 'ja-JP': 'テスト専用' } };
    catalogItems.push(fixture);
    try {
      const result = searchCatalog(
        {
          q: 'テスト専用',
          offset: 0,
          limit: 10,
          kind: 'cask',
          category: null,
          includeDisabled: false
        },
        'ja-JP'
      );
      expect(result.items[0]?.item.token).toBe('locale-test');
      expect(result.items[0]?.matchedName).toBeNull();
    } finally {
      catalogItems.pop();
    }
  });
});
