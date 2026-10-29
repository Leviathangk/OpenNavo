<script setup lang="ts">
import { LOCALE_METADATA } from '@opennavo/shared';
import { computed, ref, watch } from 'vue';
import { I18nT, useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import type { ReleaseEntry, ReleasePage } from '@opennavo/api';
import { formatDate, formatDuration, formatTime, formatVersion } from '@opennavo/shared';
import { OnCard, OnEmpty, OnSegmented, OnStatTile, OnToggle, OnVersionTimeline } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import RequestError from '@/components/common/RequestError.vue';
import { api } from '@/api';
import type { Task } from '@/ipc/bindings';
import { commands, unwrap as unwrapIpc } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLoader } from '@/composables/useLoader';
import { usePackage } from '@/composables/usePackageDetail';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useLibraryStore, useSettingsStore, useTasksStore, useUpdatesStore } from '@/stores';
import { openExternal } from '@/utils/external';

// Version history (08 §10.6, mockup 04): toolbar (all / with notes / previously installed; Chinese / original) and timeline.
// Local annotations use history_list (successful installs/updates); Current installation uses the installed version; offer Update to this version when behind latest.
// Right rail: 30-day release count, Homebrew adoption, sources, notification and pin toggles. Offline shows only an offline notice.
const PAGE_SIZE = 20;
type Filter = 'all' | 'notes' | 'mine';
type Source = ReleaseEntry['source'] | 'local';

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { kind, token, detail } = usePackage();
const library = useLibraryStore();
const updates = useUpdatesStore();
const tasks = useTasksStore();
const settings = useSettingsStore();
const toasts = useToasts();
const { displayName } = usePackageIdentity();
const { act, errorText } = usePackageState();

const filter = ref<Filter>('all');
const original = ref(false);
const pages = ref(1);

watch([kind, token], () => {
  filter.value = 'all';
  original.value = false;
  pages.value = 1;
});

watch([filter, original, appLocale], () => {
  pages.value = 1;
});

function releaseFetcher() {
  const path = { kind: kind.value, token: token.value };
  const query = { original: original.value, locale: appLocale.value };
  return (current: number, onlyWithNotes: boolean, size = PAGE_SIZE) =>
    unwrap(
      api.GET('/packages/{kind}/{token}/releases', {
        params: {
          path,
          query: { ...query, current, size, onlyWithNotes }
        }
      })
    );
}

// Append older versions page by page; restart at page one when filters or locale change. Switching filters or
// the original text keeps the toolbar and timeline on screen until the new pages land.
const { data: loaded, error } = useLoader(
  async () => {
    const shown = { filter: filter.value, original: original.value };
    const onlyWithNotes = shown.filter === 'notes';
    const mine = shown.filter === 'mine';
    const pageCount = pages.value;
    const fetchReleases = releaseFetcher();
    const all: ReleasePage[] = [];
    // Locally installed versions may predate the first page by far; this filter must traverse the server timeline.
    for (let current = 1; current <= (mine ? Number.POSITIVE_INFINITY : pageCount); current += 1) {
      const page = await fetchReleases(current, onlyWithNotes);
      all.push(page);
      if (!page.records.length || current * page.size >= page.total) break;
    }
    const last = all.at(-1);
    return last ? { ...last, records: all.flatMap(page => page.records), ...shown } : undefined;
  },
  [kind, token, filter, original, pages, appLocale],
  undefined,
  [kind, token, appLocale]
);
// A failed switch never leaves the previous filter's timeline on screen.
const data = computed(() =>
  loaded.value && (!error.value || (loaded.value.filter === filter.value && loaded.value.original === original.value))
    ? loaded.value
    : undefined
);
// Label totals follow the app and language mode; switching filters never changes them.
const { data: totals } = useLoader(async () => {
  const fetchReleases = releaseFetcher();
  const [all, notes] = await Promise.all([fetchReleases(1, false, 1), fetchReleases(1, true, 1)]);
  return { all: all.total, notes: notes.total };
}, [kind, token, appLocale, original]);

// Local records: latest successful install/update per version.
const { data: local } = useLoader(
  async () => {
    const target = { kind: kind.value, token: token.value };
    const byVersion = new Map<string, Task>();
    for (let offset = 0; ; offset += 200) {
      const history = await unwrapIpc(
        commands.historyList({
          q: null,
          ops: ['install', 'upgrade', 'reinstall'],
          states: ['succeeded'],
          target,
          includeScheduledUpdates: true,
          from: null,
          to: null,
          limit: 200,
          offset
        })
      );
      for (const task of history.items)
        if (task.toVersion && !byVersion.has(task.toVersion)) byVersion.set(task.toVersion, task);
      if (!history.items.length || offset + history.items.length >= history.total) break;
    }
    return byVersion;
  },
  [kind, token, () => tasks.finished],
  undefined,
  [kind, token]
);

const installed = computed(() => library.find(kind.value, token.value));
const installedVersion = computed(() => installed.value?.actualVersion ?? installed.value?.installedVersion ?? null);
const outdated = computed(() => updates.find(kind.value, token.value));

const counts = computed(() => ({
  all: totals.value?.all ?? 0,
  notes: totals.value?.notes ?? 0,
  mine: local.value?.size ?? 0
}));
const filterOptions = computed(() => [
  {
    value: 'all' as const,
    label: t('package.release.filterAll', { count: counts.value.all }, { plural: counts.value.all })
  },
  {
    value: 'notes' as const,
    label: t('package.release.filterWithNotes', { count: counts.value.notes }, { plural: counts.value.notes })
  },
  {
    value: 'mine' as const,
    label: t('package.release.filterMine', { count: counts.value.mine }, { plural: counts.value.mine })
  }
]);
const languageOptions = computed(() => [
  { value: 'translated' as const, label: LOCALE_METADATA[appLocale.value].name },
  { value: 'original' as const, label: t('package.release.languageOriginal') }
]);

const entries = computed(() => {
  const records = data.value?.records ?? [];
  return filter.value === 'mine' ? records.filter(entry => local.value?.has(entry.version)) : records;
});
const moreCount = computed(() =>
  filter.value === 'mine' ? 0 : Math.max(0, (data.value?.total ?? 0) - (data.value?.records.length ?? 0))
);

function localText(entry: ReleaseEntry) {
  const task = local.value?.get(entry.version);
  if (!task?.finishedAt) return null;
  const time = `${formatDate(task.finishedAt, { locale: appLocale.value })} ${formatTime(task.finishedAt)}`;
  const seconds = task.startedAt ? Math.max(1, Math.round((task.finishedAt - task.startedAt) / 1000)) : 0;
  const duration = formatDuration(seconds, { locale: appLocale.value });
  return task.op === 'upgrade' && task.fromVersion
    ? { key: 'package.release.localUpgrade', time, from: formatVersion(task.fromVersion), duration }
    : { key: 'package.release.localInstall', time, from: '', duration };
}

const canUpdate = (entry: ReleaseEntry) => entry.isLatest && Boolean(outdated.value) && !outdated.value?.pinned;

async function update() {
  try {
    await act(kind.value, token.value, 'update');
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  }
}

function openSource(_entry: ReleaseEntry, event: MouseEvent) {
  const link = event.currentTarget as HTMLAnchorElement | null;
  if (!link?.href) return;
  event.preventDefault();
  void openExternal(link.href);
}

// Median Homebrew lag behind upstream; within one hour counts as synchronized.
function lag(minutes: number) {
  const value = Math.abs(minutes);
  if (value < 60) return t('package.release.lagMinutes', { n: value });
  if (value < 1440) return t('package.release.lagHours', { n: Math.round(value / 60) });
  return t('package.release.lagDays', { n: Math.round(value / 1440) }, { plural: Math.round(value / 1440) });
}
const brew = computed(() => {
  const stats = data.value?.stats;
  if (!stats) return null;
  const median = stats.brewLag.medianMinutes;
  const value =
    median === null || median === undefined
      ? '—'
      : Math.abs(median) <= 60
        ? t('package.release.brewSync')
        : median > 0
          ? t('package.release.brewLater', { time: lag(median) })
          : t('package.release.brewEarlier', { time: lag(median) });
  return {
    value,
    hint: stats.brewLag.compared
      ? t('package.release.brewHint', {
          earlier: stats.brewLag.earlierCount,
          compared: stats.brewLag.compared
        })
      : undefined
  };
});

const sources = computed<Source[]>(() => {
  const used = new Set<Source>();
  for (const entry of data.value?.records ?? []) {
    used.add(entry.source);
    if (entry.brewCommittedAt) used.add('homebrew');
  }
  if (local.value?.size) used.add('local');
  const order: Source[] = ['editorial', 'webpage', 'github_release', 'sparkle', 'manual', 'homebrew', 'local'];
  return order.filter(source => used.has(source));
});
const sourceTone = (source: Source) =>
  source === 'homebrew' ? 'bg-chart-series2' : source === 'local' ? 'bg-status-success' : 'bg-chart-series1';

// Pin/unpin tasks (Homebrew 7 supports casks, 06 §6.3); wait for active tasks to finish.
const pinBusy = computed(() => Boolean(tasks.forPackage(kind.value, token.value)));
async function togglePin(value: boolean) {
  try {
    await tasks.enqueue(value ? 'pin' : 'unpin', { kind: kind.value, token: token.value });
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  }
}
const pinHint = computed(() =>
  detail.value?.autoUpdates
    ? t('package.release.pinHintSelfUpdate', { name: displayName(kind.value, token.value) })
    : t('package.release.pinHint')
);
</script>

<template>
  <p v-if="!data && !error" role="status">{{ t('common.loading') }}</p>
  <RequestError v-if="error && !data" :error="error" />
  <div v-else-if="data" class="grid grid-cols-[minmax(0,1fr)_280px] gap-20px pt-4px">
    <section class="flex min-w-0 flex-col gap-16px">
      <div class="flex flex-wrap items-center justify-between gap-12px">
        <OnSegmented
          v-model="filter"
          :options="filterOptions"
          :aria-label="t('package.release.filterLabel')"
          @update:model-value="pages = 1"
        />
        <OnSegmented
          :model-value="original ? 'original' : 'translated'"
          :options="languageOptions"
          :aria-label="t('package.release.languageLabel')"
          @update:model-value="value => ((original = value === 'original'), (pages = 1))"
        />
      </div>
      <OnEmpty
        v-if="!entries.length"
        icon="lucide:rotate-ccw-clock"
        :title="filter === 'mine' ? t('package.release.mineEmpty') : t('package.release.emptyTitle')"
      />
      <OnVersionTimeline
        v-else
        :entries="entries"
        :installed-version="installedVersion"
        :can-update="canUpdate"
        :more-count="moreCount"
        :has-local="entry => Boolean(localText(entry))"
        :heading-level="2"
        @update="update"
        @open-source="openSource"
        @more="pages += 1"
      >
        <template #markdown="{ source }">
          <OnMarkdown :source="source" :heading-level="3" />
        </template>
        <template #local="{ entry }">
          <I18nT v-if="localText(entry)" :keypath="localText(entry)!.key" tag="span" scope="global">
            <template #time>
              <strong class="font-600 text-ink-primary">{{ localText(entry)!.time }}</strong>
            </template>
            <template #from>{{ localText(entry)!.from }}</template>
            <template #duration>{{ localText(entry)!.duration }}</template>
          </I18nT>
        </template>
      </OnVersionTimeline>
    </section>
    <aside class="flex flex-col gap-16px">
      <div class="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-8px">
        <OnStatTile
          :label="t('package.release.count30d')"
          :value="String(data.stats.count30d)"
          :unit="t('package.release.count30dUnit', data.stats.count30d)"
          :hint="t(`package.release.cadenceHint.${data.stats.cadence}`)"
        />
        <OnStatTile v-if="brew" :label="t('package.release.brewLabel')" :value="brew.value" :hint="brew.hint" />
      </div>
      <OnCard v-if="sources.length" padding="md">
        <h2 class="m-0 mb-10px text-13.5px font-600 text-ink-primary">
          {{ t('package.release.sourcesTitle') }}
        </h2>
        <ol class="m-0 flex list-none flex-col gap-12px p-0">
          <li v-for="(source, index) in sources" :key="source" class="flex gap-10px">
            <span
              class="mt-1px h-20px w-20px flex shrink-0 items-center justify-center rounded-tiny text-11px font-600 text-ink-primary"
              :class="sourceTone(source)"
            >
              {{ index + 1 }}
            </span>
            <span class="min-w-0">
              <span class="block text-13px font-600 text-ink-primary">{{
                t(`package.release.sourceInfo.${source}.title`)
              }}</span>
              <span class="block text-12px leading-[1.5] text-ink-tertiary">{{
                t(`package.release.sourceInfo.${source}.body`)
              }}</span>
            </span>
          </li>
        </ol>
      </OnCard>
      <OnCard padding="none">
        <ul class="m-0 list-none p-0">
          <li class="flex items-center gap-12px px-16px py-12px">
            <span class="min-w-0 flex-1">
              <span id="release-notify" class="block text-13.5px text-ink-primary">{{
                t('package.release.notify')
              }}</span>
              <span class="block text-12px text-ink-tertiary">{{ t('package.release.notifyHint') }}</span>
            </span>
            <OnToggle
              :model-value="settings.value?.notifyUpdates ?? false"
              :disabled="!settings.value"
              aria-labelledby="release-notify"
              @update:model-value="value => settings.update('notifyUpdates', value)"
            />
          </li>
          <li
            v-if="installed"
            class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-16px py-12px"
          >
            <span class="min-w-0 flex-1">
              <span id="release-pin" class="block text-13.5px text-ink-primary">{{ t('package.release.pin') }}</span>
              <span class="block text-12px text-ink-tertiary">{{ pinHint }}</span>
            </span>
            <OnToggle
              :model-value="installed.pinned"
              :disabled="pinBusy"
              aria-labelledby="release-pin"
              @update:model-value="togglePin"
            />
          </li>
        </ul>
      </OnCard>
    </aside>
  </div>
</template>
