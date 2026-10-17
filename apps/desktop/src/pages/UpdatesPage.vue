<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed, onScopeDispose, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnEmpty, OnIcon, OnPageHeader, OnSegmented } from '@opennavo/ui';
import HistoryList from '@/components/updates/HistoryList.vue';
import OutdatedList from '@/components/updates/OutdatedList.vue';
import RunningTask from '@/components/updates/RunningTask.vue';
import { api } from '@/api';
import type { HistoryQuery, Task, TaskState } from '@/ipc/bindings';
import { commands, packageKey, unwrap as unwrapIpc } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useClock } from '@/composables/useClock';
import { useLoader } from '@/composables/useLoader';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useTasksStore, useUpdatesStore } from '@/stores';
import { useMasStore } from '@/stores/mas';
import { isListedTask } from '@/utils/history';

const { appLocale: formattingLocale } = useFormattingLocale();

// Updates (mockup 05): Check / Update All, running tasks, available updates (frontend summary lookup, 06 §6.8), and local history.
const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const updates = useUpdatesStore();
const mas = useMasStore();
const tasks = useTasksStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const download = computed(() => updates.actionable.reduce((total, item) => total + (item.downloadSize ?? 0), 0));
const now = useClock();
const subtitle = computed(() => {
  void now.value;
  const head = updates.actionable.length
    ? t(
        'updates.summary',
        {
          count: updates.actionable.length,
          size: formatBytes(download.value, { locale: formattingLocale.value })
        },
        { plural: updates.actionable.length }
      )
    : t('updates.summaryNone');
  return updates.checkedAt
    ? `${head} · ${t('updates.lastCheck', { time: formatRelativeTime(updates.checkedAt, { locale: appLocale.value }) })}`
    : head;
});

// A check that read most sources but not all says which ones, instead of failing as a whole.
const failedText = computed(() => {
  const sources = updates.failedSources;
  if (!sources.length) return '';
  const label = (source: string) =>
    te(`updates.sources.${source}`) ? t(`updates.sources.${source}`) : t(`updates.source.${source}`);
  const names = new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(sources.map(label));
  return t('updates.failedSources', { sources: names });
});

async function check() {
  try {
    await updates.check();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Source filter (store/security plan §7, mockup D): shown once more than one kind of source has updates. Update All
// still covers every source.
type SourceFilter = 'all' | 'homebrew' | 'mas' | 'other';
const sourceFilter = ref<SourceFilter>('all');
const groupOf = (source: string): Exclude<SourceFilter, 'all'> =>
  source === 'homebrew' ? 'homebrew' : source === 'mas' ? 'mas' : 'other';
const filterCounts = computed(() => {
  const counts = { all: updates.items.length, homebrew: 0, mas: 0, other: 0 };
  for (const item of updates.items) counts[groupOf(item.source)] += 1;
  return counts;
});
const filterOptions = computed(() =>
  (['all', 'homebrew', 'mas', 'other'] as const)
    .filter(key => key === 'all' || filterCounts.value[key] > 0)
    .map(key => ({ value: key, label: t(`updates.filter.${key}`, { count: filterCounts.value[key] }) }))
);
const shownItems = computed(() =>
  sourceFilter.value === 'all'
    ? updates.items
    : updates.items.filter(item => groupOf(item.source) === sourceFilter.value)
);
// A filter whose rows are all gone (updated, skipped) falls back to everything.
watch(filterOptions, options => {
  if (!options.some(option => option.value === sourceFilter.value)) sourceFilter.value = 'all';
});

// App Store updates need an Apple Account signed in to the App Store; its product page is where to sign in.
const masRow = computed(() => updates.items.find(item => item.source === 'mas'));
async function openAppStore() {
  try {
    if (masRow.value) await mas.openStore(Number(masRow.value.token));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function updateAll() {
  try {
    const queued = await updates.upgrade(updates.actionable);
    if (queued.length)
      toasts.push({
        tone: 'success',
        title: t('updates.queued', { count: queued.length }, { plural: queued.length })
      });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Release summaries: batch lookup online; omit summary rows offline or on failure.
const { data: lookup } = useLoader(async () => {
  if (!updates.items.length) return new Map<string, { summary: string | null; publishedAt: string | null }>();
  const result = await unwrap(
    api.POST('/packages/lookup', {
      body: { items: updates.items.map(item => ({ kind: item.kind, token: item.token })) }
    })
  );
  return new Map(
    result.map(item => [
      packageKey(item.kind, item.token),
      {
        summary: item.latestRelease?.summary ?? null,
        publishedAt: item.latestRelease?.publishedAt ?? null
      }
    ])
  );
}, [
  () => updates.items.map(item => `${packageKey(item.kind, item.token)}:${item.currentVersion}`).join(','),
  appLocale
]);
const summaries = computed(() => lookup.value ?? new Map());

// Local history: initially show 20 records, then scroll up to the latest 50.
type Filter = 'all' | 'succeeded' | 'failed';
const filter = ref<Filter>('all');
const PAGE = 20;
const MAX_HISTORY = 50;
const nextOffset = ref(0);
const loadingMore = ref(false);
const moreError = ref(false);
const sentinel = ref<HTMLElement>();
let disposed = false;
const STATES: Record<Filter, TaskState[]> = {
  all: [],
  succeeded: ['succeeded'],
  failed: ['failed']
};
const query = (): HistoryQuery => ({
  q: null,
  ops: ['install', 'upgrade', 'uninstall', 'reinstall', 'clean', 'remove_leftovers', 'rollback'],
  states: STATES[filter.value],
  target: null,
  includeScheduledUpdates: false,
  from: null,
  to: null,
  limit: PAGE,
  offset: 0
});
// IPC offsets count raw rows; collect visible rows before applying UI page limits.
async function readHistoryPage(request: HistoryQuery) {
  const items: Task[] = [];
  let offset = request.offset;
  let total = 0;
  do {
    const page = await unwrapIpc(
      commands.historyList({
        ...request,
        offset,
        limit: request.limit - items.length
      })
    );
    total = page.total;
    offset += page.items.length;
    items.push(...page.items.filter(isListedTask));
    if (!page.items.length || disposed) break;
  } while (items.length < request.limit && offset < total);
  return { items, total, nextOffset: offset };
}
// Finished tasks and filter changes reread the list in the background: the rows on screen stay until the new
// ones land, and a reread for the same filter keeps as many rows as are already shown.
let shownRows = { filter: filter.value, count: 0 };
const {
  data: history,
  loading: historyLoading,
  error: historyError
} = useLoader(
  async () => {
    const keep = shownRows.filter === filter.value ? shownRows.count : 0;
    const request = { ...query(), limit: Math.min(MAX_HISTORY, Math.max(PAGE, keep)) };
    return { filter: filter.value, ...(await readHistoryPage(request)) };
  },
  [filter, () => tasks.finished],
  page => {
    shownRows = { filter: page.filter, count: page.items.length };
    nextOffset.value = page.nextOffset;
    moreError.value = false;
  },
  []
);
// A failed reread keeps rows of the selected filter on screen, never those of another filter.
const shownHistory = computed(() =>
  history.value && (!historyError.value || history.value.filter === filter.value) ? history.value : undefined
);
const historyItems = computed<Task[]>(() => (shownHistory.value?.items ?? []).filter(isListedTask));
const hasMore = computed(
  () => historyItems.value.length < MAX_HISTORY && nextOffset.value < (shownHistory.value?.total ?? 0)
);

async function loadMoreHistory() {
  if (!hasMore.value || historyLoading.value || loadingMore.value) return;
  const current = history.value;
  loadingMore.value = true;
  moreError.value = false;
  try {
    const page = await readHistoryPage({
      ...query(),
      limit: Math.min(PAGE, MAX_HISTORY - historyItems.value.length),
      offset: nextOffset.value
    });
    if (disposed || history.value !== current || !current) return;
    nextOffset.value = page.nextOffset;
    const items = [...new Map([...current.items, ...page.items].map(task => [task.id, task])).values()].slice(
      0,
      MAX_HISTORY
    );
    history.value = {
      filter: current.filter,
      total: page.items.length ? page.total : nextOffset.value,
      nextOffset: page.nextOffset,
      items
    };
    shownRows = { filter: current.filter, count: items.length };
  } catch (error) {
    if (disposed || history.value !== current) return;
    moreError.value = true;
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    loadingMore.value = false;
  }
}
let observer: IntersectionObserver | undefined;
watch(
  [sentinel, historyLoading, loadingMore, hasMore],
  () => {
    observer?.disconnect();
    if (!sentinel.value || historyLoading.value || loadingMore.value || moreError.value || !hasMore.value) return;
    observer = new IntersectionObserver(
      entries => {
        if (entries.some(entry => entry.isIntersecting)) void loadMoreHistory();
      },
      { rootMargin: '200px' }
    );
    observer.observe(sentinel.value);
  },
  { flush: 'post' }
);
onScopeDispose(() => {
  disposed = true;
  observer?.disconnect();
});
useLoader(() => updates.load());
</script>

<template>
  <div class="flex flex-col gap-20px">
    <OnPageHeader :title="t('updates.title')" :subtitle="subtitle">
      <template #actions>
        <OnButton variant="secondary" icon="refresh-cw" :loading="updates.checking" @click="check">
          {{ updates.checking ? t('updates.checking') : t('updates.check') }}
        </OnButton>
        <OnButton variant="primary" :disabled="!updates.actionable.length" @click="updateAll">{{
          t('updates.updateAll')
        }}</OnButton>
      </template>
    </OnPageHeader>

    <RunningTask v-if="tasks.running && tasks.running.op !== 'cleanup'" :task="tasks.running" />

    <div
      v-if="updates.masSignedOut && masRow"
      role="alert"
      class="flex items-center gap-12px rounded-default bg-surface-raised p-12px"
    >
      <span class="flex-1 text-13px text-ink-secondary">{{ t('updates.masSignedOut') }}</span>
      <OnButton variant="secondary" size="sm" @click="openAppStore">{{ t('updates.openAppStore') }}</OnButton>
    </div>

    <p v-if="failedText" role="status" class="m-0 flex items-center gap-6px text-12.5px text-ink-tertiary">
      <OnIcon name="info" :size="14" class="shrink-0" />{{ failedText }}
    </p>

    <OnSegmented
      v-if="filterOptions.length > 2"
      v-model="sourceFilter"
      size="sm"
      class="self-start"
      :aria-label="t('updates.filter.label')"
      :options="filterOptions"
    />
    <OutdatedList v-if="shownItems.length" :items="shownItems" :summaries="summaries" />
    <OnEmpty
      v-else-if="updates.loaded"
      :title="t('updates.empty')"
      :description="t('updates.emptyHint')"
      icon="lucide:check"
    />

    <section class="mt-8px">
      <div class="mb-12px flex flex-wrap items-baseline justify-between gap-12px">
        <h2 class="m-0 flex items-baseline gap-10px text-headline text-ink-primary">
          {{ t('updates.history.title') }}
        </h2>
        <OnSegmented
          v-model="filter"
          size="sm"
          :aria-label="t('updates.history.filterLabel')"
          :options="[
            { value: 'all', label: t('updates.history.all') },
            { value: 'succeeded', label: t('updates.history.succeeded') },
            { value: 'failed', label: t('updates.history.failed') }
          ]"
        />
      </div>
      <p v-if="historyLoading && !shownHistory" role="status">{{ t('common.loading') }}</p>
      <HistoryList v-if="historyItems.length" :tasks="historyItems" />
      <OnEmpty v-else-if="shownHistory" :title="t('updates.history.empty')" icon="lucide:rotate-ccw-clock" />
      <div v-if="hasMore" ref="sentinel" class="mt-16px flex justify-center">
        <OnButton variant="secondary" :loading="loadingMore" :disabled="historyLoading" @click="loadMoreHistory">
          {{ t('common.loadMore') }}
        </OnButton>
      </div>
    </section>
  </div>
</template>
