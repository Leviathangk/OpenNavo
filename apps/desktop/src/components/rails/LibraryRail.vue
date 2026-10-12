<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnIcon, OnRailPanel, OnRailSection, OnStackBar } from '@opennavo/ui';
import BrewfileRestore from '@/components/library/BrewfileRestore.vue';
import type { BrewfilePreview } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { usePackageState } from '@/composables/usePackageState';
import { useLoader } from '@/composables/useLoader';
import { useToasts } from '@/composables/useToasts';
import { useLibraryStore, useTasksStore } from '@/stores';
import { useInstalledFilterStore } from '@/stores/installedFilter';
import { useCleanupStore } from '@/stores/cleanup';
import { UNUSED_DAYS, isUnused, useLocalAppsStore } from '@/stores/localApps';
import { pickOpenPath, pickSavePath } from '@/utils/files';

const { appLocale: formattingLocale } = useFormattingLocale();

// Installed rail (mockup 06, cleanup proposal §04): storage by source, the Clean Up Mac entry (replacing the leftovers
// and cache cards), unused apps, Brewfile backup/migration.
const { t } = useI18n();
const library = useLibraryStore();
const localApps = useLocalAppsStore();
const filters = useInstalledFilterStore();
const cleanup = useCleanupStore();
const router = useRouter();
const tasks = useTasksStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const restoreOpen = ref(false);
const preview = ref<BrewfilePreview | null>(null);

async function attempt<T>(operation: Promise<T>): Promise<T | undefined> {
  try {
    return await operation;
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
    return undefined;
  }
}

useLoader(() => library.loadStorage(), [() => tasks.finished], undefined, []);

const storage = computed(() => library.storage);
const format = (bytes: number) => formatBytes(bytes, { locale: formattingLocale.value });
// Every app on this Mac by source plus Homebrew's download cache; command-line tools stay excluded (ADR-018).
// Three segments keep the chart within the design system's three category colors. Apps count as the size column
// does (app, data and caches once measured).
const sizeOf = (homebrew: boolean) =>
  localApps.items
    .filter(app => (app.source === 'homebrew') === homebrew)
    .reduce((sum, app) => sum + localApps.spaceOf(app), 0);
const parts = computed(() => {
  if (!storage.value && !localApps.loaded) return [];
  const cache = storage.value?.cacheBytes ?? 0;
  return [
    { key: 'homebrew', label: t('library.storage.homebrew'), value: sizeOf(true) },
    { key: 'others', label: t('library.storage.others'), value: sizeOf(false) },
    { key: 'cache', label: t('library.storage.cache'), value: cache }
  ].map(part => ({ ...part, display: format(part.value) }));
});
const total = computed(() => parts.value.reduce((sum, part) => sum + part.value, 0));

const unused = computed(() => localApps.items.filter(app => isUnused(app)));
const unusedBytes = computed(() => unused.value.reduce((sum, app) => sum + (app.sizeBytes ?? 0), 0));
const lastScan = computed(() => {
  const summary = cleanup.summary;
  if (summary?.scannedAt == null) return null;
  return {
    size: format(summary.reclaimableBytes),
    time: formatRelativeTime(summary.scannedAt, { locale: formattingLocale.value }),
    active: summary.activeBackgroundItems
  };
});

/** Never scanned: start right away; otherwise open the page with the last numbers. */
async function openCleanup() {
  await router.push('/cleanup');
  if (!cleanup.scanned && cleanup.stage === 'idle') void cleanup.scan();
}

async function exportBrewfile() {
  const path = await pickSavePath('Brewfile');
  if (!path) return;
  const count = await attempt(unwrap(commands.brewfileExport(path, null)));
  if (count !== undefined)
    toasts.push({
      tone: 'success',
      title: t('library.backup.exported', { count }, { plural: count })
    });
}

async function restoreBrewfile() {
  const path = await pickOpenPath();
  if (!path) return;
  const result = await attempt(unwrap(commands.brewfilePreview(path)));
  if (!result) return;
  preview.value = result;
  restoreOpen.value = true;
}
</script>

<template>
  <OnRailPanel>
    <OnRailSection
      :title="t('library.storage.title')"
      :subtitle="t('library.storage.total', { size: parts.length ? format(total) : '—' })"
    >
      <OnCard padding="md">
        <p class="m-0 mb-12px text-26px font-600 leading-none text-ink-primary">
          {{ parts.length ? format(total) : '—' }}
        </p>
        <OnStackBar v-if="parts.length" :items="parts" :caption="t('library.storage.caption')" :format="format" />
      </OnCard>
    </OnRailSection>

    <OnCard padding="md" class="flex flex-col gap-10px" data-testid="cleanup-entry">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="eraser" :size="15" />{{ t('cleanup.entry.title') }}
      </h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
        {{ lastScan ? t('cleanup.entry.found', lastScan) : t('cleanup.entry.description') }}
      </p>
      <p v-if="lastScan?.active" class="m-0 text-12.5px leading-[1.55] text-status-danger">
        {{ t('cleanup.entry.active', { count: lastScan.active }, { plural: lastScan.active }) }}
      </p>
      <OnButton
        :variant="lastScan ? 'primary' : 'secondary'"
        :icon="lastScan ? undefined : 'scan-search'"
        @click="openCleanup"
      >
        {{ lastScan ? t('cleanup.entry.open') : t('cleanup.entry.scan') }}
      </OnButton>
    </OnCard>

    <OnCard v-if="localApps.loaded" padding="md" class="flex flex-col gap-10px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="hourglass" :size="15" />{{ t('library.unusedCard.title') }}
      </h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
        {{
          unused.length
            ? t(
                'library.unusedCard.description',
                { count: unused.length, days: UNUSED_DAYS, size: format(unusedBytes) },
                { plural: unused.length }
              )
            : t('library.unusedCard.none', { days: UNUSED_DAYS })
        }}
      </p>
      <OnButton v-if="unused.length" variant="secondary" @click="filters.showUnused(!filters.unusedOnly)">
        {{ filters.unusedOnly ? t('library.unusedCard.all') : t('library.unusedCard.show') }}
      </OnButton>
    </OnCard>

    <OnCard padding="md" class="flex flex-col gap-10px">
      <h2 class="m-0 text-14px font-600 text-ink-primary">{{ t('library.backup.title') }}</h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
        {{ t('library.backup.description') }}
      </p>
      <div class="flex flex-wrap gap-8px">
        <OnButton
          class="max-w-full flex-1 !h-auto min-h-34px py-6px"
          variant="secondary"
          icon="cloud"
          @click="exportBrewfile"
        >
          <span class="min-w-0 whitespace-normal break-words">{{ t('library.backup.export') }}</span>
        </OnButton>
        <OnButton class="max-w-full flex-1 !h-auto min-h-34px py-6px" variant="secondary" @click="restoreBrewfile">
          <span class="min-w-0 whitespace-normal break-words">{{ t('library.backup.restore') }}</span>
        </OnButton>
      </div>
    </OnCard>

    <BrewfileRestore v-model:open="restoreOpen" :preview="preview" />
  </OnRailPanel>
</template>
