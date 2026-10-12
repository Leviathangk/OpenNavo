<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { formatBytes, formatCount, formatCountCompact, formatRelativeTime } from '@opennavo/shared';
import { collectionIcons, OnCollectionCard, OnRailPanel, OnRailSection, OnRankRow, OnStatTile } from '@opennavo/ui';
import { api } from '@/api';
import { commands, unwrap as unwrapIpc } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { fetchHome, peekHome } from '@/composables/useHome';
import { useClock } from '@/composables/useClock';
import { useLoader } from '@/composables/useLoader';
import { usePackageSummary } from '@/composables/usePackageSummary';
import { useLibraryStore, useSettingsStore, useUpdatesStore, useCatalogStore, useTasksStore } from '@/stores';
import { splitValue } from '@/utils/stat';

const { appLocale: formattingLocale } = useFormattingLocale();

// Discover rail (mockup 02): 2×2 local overview, weekly popular apps, collection card.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const { toSummary } = usePackageSummary();
const library = useLibraryStore();
const updates = useUpdatesStore();
const settings = useSettingsStore();

const catalog = useCatalogStore();
const tasks = useTasksStore();
const now = useClock();
useLoader(() => library.loadStorage(), [() => tasks.finished], undefined, []);

interface Tile {
  key: string;
  label: string;
  value: string;
  unit?: string;
  hint?: string;
}

const tiles = computed<Tile[]>(() => {
  void now.value;
  const download = updates.actionable.reduce((total, item) => total + (item.downloadSize ?? 0), 0);
  const storage = library.storage;
  // Cask-only catalog (ADR-018): storage includes apps/download cache, excluding command-line tools.
  const total = storage ? storage.appsBytes + storage.cacheBytes : null;
  const checked = updates.checkedAt;
  const auto = settings.value?.autoCheck;
  return [
    {
      key: 'installed',
      label: t('rail.installed'),
      value: library.loaded ? formatCount(library.items.length, { locale: formattingLocale.value }) : '—'
    },
    {
      key: 'updates',
      label: t('rail.updates'),
      value: updates.loaded ? formatCount(updates.actionable.length, { locale: formattingLocale.value }) : '—',
      hint: updates.actionable.length
        ? t('rail.updatesHint', { size: formatBytes(download, { locale: formattingLocale.value }) })
        : t('rail.updatesNone')
    },
    {
      key: 'disk',
      label: t('rail.disk'),
      ...(total === null ? { value: '—' } : splitValue(formatBytes(total, { locale: formattingLocale.value }))),
      hint: storage
        ? t('rail.diskHint', {
            size: formatBytes(storage.cacheBytes, { locale: formattingLocale.value })
          })
        : undefined
    },
    {
      key: 'lastCheck',
      label: t('rail.lastCheck'),
      ...(checked ? splitValue(formatRelativeTime(checked, { locale: appLocale.value })) : { value: '—' }),
      hint: auto ? t('rail.lastCheckAuto', { time: settings.value?.checkTime ?? '' }) : t('rail.lastCheckManual')
    }
  ];
});

// Weekly popular apps: API 30-day rankings online; local 30-day install sorting offline.
const { data: trending } = useLoader(
  async () => {
    try {
      const page = await unwrap(api.GET('/rankings', { params: { query: { kind: 'cask', period: '30d', size: 4 } } }));
      return page.records.map(record => ({
        rank: record.rank,
        pkg: record.package,
        installs: record.installs
      }));
    } catch {
      const page = await unwrapIpc(
        commands.catalogList({
          kind: 'cask',
          category: null,
          sort: 'installs30d',
          includeFonts: true,
          includeLibraries: false,
          includeDisabled: false,
          limit: 4,
          offset: 0
        })
      );
      return page.items.map((item, index) => ({
        rank: index + 1,
        pkg: toSummary(item),
        installs: item.installs30d
      }));
    }
  },
  [appLocale, () => catalog.revision],
  undefined,
  [appLocale]
);

const { data: home } = useLoader(
  () => fetchHome(appLocale.value),
  [appLocale],
  undefined,
  [appLocale],
  () => peekHome(appLocale.value)
);
const collection = computed(() => home.value?.collections[0]);
</script>

<template>
  <OnRailPanel>
    <OnRailSection :title="t('rail.title')" :subtitle="t('rail.subtitle')">
      <div class="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-8px">
        <OnStatTile
          v-for="tile in tiles"
          :key="tile.key"
          :label="tile.label"
          :value="tile.value"
          :unit="tile.unit"
          :hint="tile.hint"
        />
      </div>
    </OnRailSection>

    <OnRailSection v-if="trending?.length" :title="t('rail.weeklyTop')" :subtitle="t('rail.weeklyTopHint')">
      <ol class="m-0 flex list-none flex-col gap-8px p-0">
        <li v-for="entry in trending" :key="entry.pkg.token">
          <OnRankRow
            :rank="entry.rank"
            :pkg="entry.pkg"
            :value="formatCountCompact(entry.installs, { locale: appLocale })"
            :href="`/package/cask/${entry.pkg.token}`"
            :link-as="RouterLink"
          />
        </li>
      </ol>
    </OnRailSection>

    <div v-if="collection" class="flex flex-col gap-10px">
      <OnCollectionCard
        :title="collection.title"
        :subtitle="collection.subtitle"
        :count="collection.itemCount"
        :icons="collectionIcons(collection)"
        :href="`/collections/${collection.slug}`"
        :link-as="RouterLink"
      />
    </div>
  </OnRailPanel>
</template>
