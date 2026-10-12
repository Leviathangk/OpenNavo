<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnAppIcon, OnCheckbox, OnIcon } from '@opennavo/ui';
import type { CategoryReport, CleanupCategory, CleanupGroup, SkippedGroup } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { groupKey, inUseBytes, useCleanupStore } from '@/stores/cleanup';
import type { CleanupEntry } from '@/stores/cleanup';
import { useLocalAppsStore } from '@/stores/localApps';
import CleanupItemRow from './CleanupItemRow.vue';
import { CATEGORY_ICONS } from './meta';

// One category (cleanup proposal §03 ③④): tick it as a whole or unfold to adjust items; skipped groups are listed read-only.
// Unfolding lists one row per app with its own tick; its items stay folded until that row is unfolded too.
const props = defineProps<{ category: CategoryReport }>();
const emit = defineEmits<{ reveal: [entry: CleanupEntry]; protect: [entry: CleanupEntry] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();
const localApps = useLocalAppsStore();
const { localApp } = usePackageIdentity();

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const items = computed(() => props.category.groups.flatMap(group => group.items));
const state = computed(() => store.categoryState(props.category));
const open = computed(() => store.expanded.includes(props.category.key));
const groupOpen = (group: CleanupGroup) => store.expanded.includes(groupKey(props.category.key, group));
const chosenBytes = computed(() =>
  items.value.filter(item => store.isChecked(item)).reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0)
);
const total = computed(() =>
  props.category.complete
    ? format(props.category.bytes)
    : t('cleanup.category.atLeast', { size: format(props.category.bytes) })
);
/** Caches of running apps are measured but not selectable; quitting the app frees them. */
const held = computed(() => {
  const bytes = inUseBytes(props.category);
  return bytes ? t('cleanup.category.inUse', { size: format(bytes) }) : '';
});
const status = computed(() =>
  chosenBytes.value
    ? t('cleanup.category.selected', { size: format(chosenBytes.value) })
    : items.value.length
      ? t('cleanup.category.none')
      : held.value || t('cleanup.category.allSkipped')
);
/** One row per app and reason (Rust may report several folders of one app), largest first. */
const skipped = computed(() => {
  const rows = new Map<string, SkippedGroup>();
  for (const group of props.category.skipped) {
    const key = `${group.reason}:${group.appId ?? group.title}`;
    const row = rows.get(key);
    rows.set(
      key,
      row
        ? {
            ...row,
            bytes: row.bytes === null && group.bytes === null ? null : (row.bytes ?? 0) + (group.bytes ?? 0),
            processes: [...new Set([...row.processes, ...group.processes])]
          }
        : group
    );
  }
  return [...rows]
    .map(([key, group]) => ({ key, group }))
    .sort((a, b) => (b.group.bytes ?? -1) - (a.group.bytes ?? -1));
});

function icon(group: Pick<CleanupGroup, 'appId' | 'bundleId' | 'title'> & { id?: string }) {
  const app = group.appId ? localApps.find(group.appId) : undefined;
  if (app) return localApp(app).icon;
  return {
    kind: 'cask' as const,
    token: group.bundleId ?? group.id ?? group.title,
    name: group.title,
    src: null,
    accent: null
  };
}
/** Name what holds a skipped group ("go 正在运行") when Rust reports the processes. */
const names = (processes: string[]) =>
  new Intl.ListFormat(appLocale.value, { type: 'conjunction', style: 'narrow' }).format(processes);
function skipReason(group: SkippedGroup) {
  const withNames = group.reason === 'running' || group.reason === 'in_use' || group.reason === 'busy';
  return withNames && group.processes.length
    ? t(`cleanup.skipReasonsBy.${group.reason}`, { names: names(group.processes) })
    : t(`cleanup.skipReasons.${group.reason}`);
}
/** These categories hold one folder-level group (the temp folder, the Trash, Downloads and Desktop) that Rust names
 *  by key; the title comes from here. */
const FOLDER_GROUPS: readonly CleanupCategory[] = ['temp', 'trash', 'installers'];
const title = (group: CleanupGroup) =>
  FOLDER_GROUPS.includes(props.category.key) ? t(`cleanup.groups.${props.category.key}`) : group.title;
const groupSize = (group: CleanupGroup) => format(group.items.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0));
/** Leftovers show how long ago the app's files last changed and whether its background items still load. */
function subtitle(group: CleanupGroup) {
  // Rust names device support groups by model and platform; the description comes from here.
  if (group.items.some(item => item.reason === 'device_support' || item.reason === 'device_support_latest'))
    return { text: t('cleanup.groups.deviceSupport', { platform: group.subtitle ?? '' }), warn: false };
  // Backups are named by device (Rust adds the model), archives by app.
  if (group.items.some(item => item.reason === 'device_backup'))
    return {
      text: group.subtitle
        ? t('cleanup.groups.deviceBackupModel', { model: group.subtitle })
        : t('cleanup.groups.deviceBackup'),
      warn: false
    };
  if (group.items.some(item => item.reason === 'xcode_archive'))
    return { text: t('cleanup.groups.xcodeArchive'), warn: false };
  if (props.category.key !== 'leftovers') return { text: group.subtitle ?? '', warn: false };
  const services = group.items.filter(item => item.reason === 'orphan_service').length;
  if (services)
    return {
      text: t('cleanup.category.servicesLoading', { count: services }, { plural: services }),
      warn: true
    };
  const newest = Math.max(...group.items.map(item => item.modifiedAt ?? 0));
  return {
    text: newest
      ? t('cleanup.category.lastModified', {
          time: formatRelativeTime(newest, { locale: appLocale.value })
        })
      : '',
    warn: false
  };
}
const entry = (group: CleanupGroup, item: CleanupGroup['items'][number]): CleanupEntry => ({
  category: props.category,
  group,
  item
});
</script>

<template>
  <section
    class="overflow-hidden rounded-panel border border-solid border-line-subtle bg-surface-card"
    :data-category="category.key"
  >
    <div class="flex items-center gap-12px px-14px py-11px">
      <OnCheckbox
        :model-value="state === 'on'"
        :indeterminate="state === 'ind'"
        :disabled="!items.length"
        :aria-label="t(`cleanup.categories.${category.key}`)"
        @update:model-value="value => store.setCategory(category, value)"
      />
      <span class="grid h-30px w-30px shrink-0 place-items-center rounded-[8px] bg-surface-raised text-ink-secondary">
        <OnIcon :name="CATEGORY_ICONS[category.key]" :size="15" />
      </span>
      <button
        type="button"
        class="m-0 flex min-w-0 flex-1 cursor-pointer items-center gap-12px border-none bg-transparent p-0 text-left outline-none focus-visible:shadow-focus-ring"
        :aria-expanded="open"
        @click="store.toggleExpanded(category.key)"
      >
        <span class="min-w-0 flex-1">
          <span class="block text-13.5px font-600 text-ink-primary">{{ t(`cleanup.categories.${category.key}`) }}</span>
          <!-- Long hints are cut to one line; the whole sentence stays reachable on hover. -->
          <span
            class="mt-1px block truncate text-12px text-ink-tertiary"
            :title="t(`cleanup.categoryHints.${category.key}`)"
            >{{ t(`cleanup.categoryHints.${category.key}`) }}</span
          >
        </span>
        <span class="min-w-84px shrink-0 text-right">
          <span class="block text-13px tabular-nums text-ink-primary">{{ items.length ? total : '—' }}</span>
          <span class="mt-1px block text-11.5px tabular-nums text-ink-tertiary">{{ status }}</span>
          <span v-if="items.length && held" class="block text-11.5px tabular-nums text-ink-tertiary">{{ held }}</span>
        </span>
        <OnIcon
          name="chevron-down"
          :size="15"
          class="shrink-0 text-ink-tertiary transition-transform duration-fast"
          :class="open ? '' : '-rotate-90'"
        />
      </button>
    </div>

    <div v-if="open" class="border-t border-t-solid border-line-subtle bg-surface-inset">
      <div
        v-for="group in category.groups"
        :key="group.id"
        class="[&+&]:border-t [&+&]:border-t-solid [&+&]:border-line-subtle"
      >
        <div class="flex min-h-42px items-center gap-12px py-6px pl-44px pr-14px">
          <OnCheckbox
            :model-value="store.groupState(group) === 'on'"
            :indeterminate="store.groupState(group) === 'ind'"
            :aria-label="title(group)"
            @update:model-value="value => store.setGroup(group, value)"
          />
          <button
            type="button"
            class="m-0 flex min-w-0 flex-1 cursor-pointer items-center gap-12px border-none bg-transparent p-0 text-left outline-none focus-visible:shadow-focus-ring"
            :aria-expanded="groupOpen(group)"
            @click="store.toggleExpanded(groupKey(category.key, group))"
          >
            <OnAppIcon v-bind="icon({ ...group, title: title(group) })" :size="24" />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-12.5px font-600 text-ink-primary">{{ title(group) }}</span>
              <span
                v-if="subtitle(group).text"
                class="block truncate text-11.5px"
                :class="subtitle(group).warn ? 'text-status-danger' : 'text-ink-tertiary'"
                >{{ subtitle(group).text }}</span
              >
            </span>
            <span class="shrink-0 text-12px tabular-nums text-ink-secondary">{{ groupSize(group) }}</span>
            <OnIcon
              name="chevron-down"
              :size="15"
              class="shrink-0 text-ink-tertiary transition-transform duration-fast"
              :class="groupOpen(group) ? '' : '-rotate-90'"
            />
          </button>
        </div>
        <template v-if="groupOpen(group)">
          <CleanupItemRow
            v-for="item in group.items"
            :key="item.id"
            :item="item"
            :checked="store.isChecked(item)"
            @toggle="value => store.setItem(item.id, value)"
            @reveal="emit('reveal', entry(group, item))"
            @protect="emit('protect', entry(group, item))"
          />
        </template>
      </div>
      <div v-if="skipped.length" class="border-t border-t-solid border-line-subtle py-8px pl-44px pr-14px">
        <p class="m-0 mb-4px flex items-center gap-6px text-11.5px text-ink-tertiary">
          <OnIcon name="shield" :size="12" />{{
            t('cleanup.category.skipped', { count: skipped.length }, { plural: skipped.length })
          }}
        </p>
        <!-- Indented past the tick column so the icons line up with the groups' icons. -->
        <div
          v-for="{ key, group } in skipped"
          :key="key"
          class="flex items-center gap-10px py-5px pl-28px text-12px text-ink-tertiary"
        >
          <OnAppIcon v-bind="icon({ appId: group.appId, bundleId: null, title: group.title })" :size="20" />
          <span class="text-ink-secondary">{{ group.title }}</span>
          <span class="min-w-0 flex-1 truncate">{{ skipReason(group) }}</span>
          <span v-if="group.bytes" class="shrink-0 tabular-nums">{{ format(group.bytes) }}</span>
        </div>
      </div>
    </div>
  </section>
</template>
