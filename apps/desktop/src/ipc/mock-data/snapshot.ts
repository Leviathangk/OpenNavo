import type { PublicComponents } from '@opennavo/api';
import { LOCALES } from '@opennavo/shared';
import type { Locale } from '@opennavo/shared';
import { catalogCategories, catalogCursor, catalogItems } from './fixtures';

type Schema = PublicComponents['schemas'];
const generatedAt = '2026-09-30T08:00:00Z';
const version = Date.parse(generatedAt);

// Browsers do not download these URLs; they validate the v2 fixture contract structure.
export const catalogBaseSnapshot: Schema['CatalogBaseSnapshot'] = {
  formatVersion: 2,
  cursor: catalogCursor,
  generatedAt,
  items: catalogItems.flatMap(({ displayName: _displayName, summary: _summary, ...item }) =>
    item.kind === 'cask'
      ? [
          {
            ...item,
            kind: item.kind,
            popularity: item.popularity ?? 0,
            installs90d: item.installs90d ?? undefined,
            installs365d: item.installs365d ?? undefined,
            appTargets: item.appTargets ?? undefined,
            tap: item.tap ?? undefined
          }
        ]
      : []
  ),
  categories: catalogCategories.flatMap(({ name: _name, ...category }) =>
    category.appliesTo === 'formula' ? [] : [{ ...category, appliesTo: category.appliesTo }]
  )
};

function textPack(locale: Locale): Schema['CatalogTextPack'] {
  return {
    formatVersion: 2,
    cursor: catalogCursor,
    version,
    locale,
    generatedAt,
    items: catalogItems.flatMap(item => {
      const displayName = item.displayName[locale];
      const summary = item.summary[locale];
      return item.kind === 'cask' && (displayName || summary)
        ? [
            {
              kind: item.kind,
              token: item.token,
              ...(displayName ? { displayName } : {}),
              ...(summary ? { summary } : {})
            }
          ]
        : [];
    }),
    categories: catalogCategories.flatMap(category =>
      category.appliesTo !== 'formula' && category.name[locale]
        ? [{ slug: category.slug, name: category.name[locale]! }]
        : []
    )
  };
}

export const catalogTextPacks = Object.fromEntries(LOCALES.map(locale => [locale, textPack(locale)])) as Record<
  Locale,
  Schema['CatalogTextPack']
>;

export const catalogSnapshotInfo: Schema['CatalogSnapshotInfo'] = {
  formatVersion: 2,
  cursor: catalogCursor,
  itemCount: catalogBaseSnapshot.items.length,
  createdAt: generatedAt,
  url: 'https://cdn.opennavo.example/mock/catalog-v2.json.gz',
  sha256: '0'.repeat(64),
  bytes: 0,
  textPacks: Object.fromEntries(
    LOCALES.map(locale => [
      locale,
      {
        locale,
        version,
        cursor: catalogCursor,
        url: `https://cdn.opennavo.example/mock/text-${locale}.json.gz`,
        sha256: '0'.repeat(64),
        bytes: 0
      }
    ])
  ) as Schema['CatalogTextPacks']
};
