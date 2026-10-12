<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed, onScopeDispose, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnEmpty, OnPageHeader, OnSegmented } from '@opennavo/ui';
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
import { useSettingsStore, useTasksStore, useUpdatesStore } from '@/stores';
import { isListedTask } from '@/utils/history';

const { appLocale: formattingLocale } = useFormattingLocale();

// Updates (mockup 05): Check / Update All, running tasks, available updates (frontend summary lookup, 06 §6.8), and local history.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const updates = useUpdatesStore();
const tasks = useTasksStore();
const settings = useSettingsStore();
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

async function check() {
  try {
    await updates.check(settings.value?.runBrewUpdateOnCheck ?? true);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function updateAll() {
  try {
    const targets = updates.actionable.map(item => ({ kind: item.kind, token: item.token }));
    const queued = await tasks.enqueueMany('upgrade', targets, 'manual', { greedy: true });
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
  ops: ['install', 'upgrade', 'uninstall', 'reinstall', 'clean', 'remove_leftovers'],
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
const { data: history, loading: historyLoading } = useLoader(
  () => readHistoryPage(query()),
  [filter, () => tasks.finished],
  page => {
    nextOffset.value = page.nextOffset;
    moreError.value = false;
  }
);
const historyItems = computed<Task[]>(() => (history.value?.items ?? []).filter(isListedTask));
const hasMore = computed(
  () => historyItems.value.length < MAX_HISTORY && nextOffset.value < (history.value?.total ?? 0)
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
    history.value = {
      total: page.items.length ? page.total : nextOffset.value,
      nextOffset: page.nextOffset,
      items: [...new Map([...current.items, ...page.items].map(task => [task.id, task])).values()].slice(0, MAX_HISTORY)
    };
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

    <OutdatedList v-if="updates.items.length" :items="updates.items" :summaries="summaries" />
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
      <p v-if="historyLoading && !history" role="status">{{ t('common.loading') }}</p>
      <HistoryList v-if="historyItems.length" :tasks="historyItems" />
      <OnEmpty v-else-if="history" :title="t('updates.history.empty')" icon="lucide:rotate-ccw-clock" />
      <div v-if="hasMore" ref="sentinel" class="mt-16px flex justify-center">
        <OnButton variant="secondary" :loading="loadingMore" :disabled="historyLoading" @click="loadMoreHistory">
          {{ t('common.loadMore') }}
        </OnButton>
      </div>
    </section>
  </div>
</template>
