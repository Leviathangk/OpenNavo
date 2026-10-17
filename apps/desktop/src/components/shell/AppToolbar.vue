<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatCount, formatPercent } from '@opennavo/shared';
import { OnButton, OnToolbar } from '@opennavo/ui';
import { usePageRefresh } from '@/composables/usePageRefresh';
import ToolbarIconButton from './ToolbarIconButton.vue';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useCatalogStore, useTasksStore } from '@/stores';

const { appLocale: formattingLocale } = useFormattingLocale();

defineProps<{ showOverviewToggle: boolean; overviewOpen: boolean }>();
const emit = defineEmits<{ toggleOverview: []; openPalette: [] }>();

const { t } = useI18n();
const { refresh, loading } = usePageRefresh();
const { displayName } = usePackageIdentity();
const router = useRouter();
const route = useRoute();
const catalog = useCatalogStore();
const tasks = useTasksStore();

// vue-router records previous/next pages in history.state; use this for navigation buttons and reread on route changes.
interface HistoryState {
  back?: string | null;
  forward?: string | null;
}
const historyState = computed<HistoryState>(() => {
  void route.fullPath;
  return (window.history.state ?? {}) as HistoryState;
});
const canGoBack = computed(() => Boolean(historyState.value.back));
const canGoForward = computed(() => Boolean(historyState.value.forward));

const searchLabel = computed(() =>
  catalog.status?.itemCount
    ? t(
        'toolbar.search',
        { count: formatCount(catalog.status.itemCount, { locale: formattingLocale.value }) },
        { plural: catalog.status.itemCount }
      )
    : t('toolbar.searchShort')
);

// Running task (08 §9.1, toolbar right): progress ring and task title; click to open Updates.
const runningTask = computed(() => tasks.running);
const runningLabel = computed(() => {
  const task = runningTask.value;
  if (!task) return '';
  // Clean Up's target is its report, not a package; the action alone names it.
  const name = task.op !== 'clean' && task.target ? displayName(task.target.kind, task.target.token) : '';
  // The scan before installing can take a minute with no measurable progress; say why the ring holds still.
  if (task.phase === 'scanning') return t('toolbar.taskScanning', { name });
  return t('toolbar.taskRunning', { action: t(`tasks.ops.${task.op}`), name }).trim();
});
const ring = computed(() => {
  const circumference = 2 * Math.PI * 7;
  const percent = runningTask.value ? tasks.shownPercent(runningTask.value) : 0;
  return { circumference, offset: circumference * (1 - percent / 100) };
});

// Use shared @opennavo/ui OnToolbar (ADR-017); back/forward controls and task progress are desktop-specific.
</script>

<template>
  <OnToolbar :search-label="searchLabel" search-shortcut="⌘K" drag-region @search="emit('openPalette')">
    <template #start>
      <ToolbarIconButton
        icon="lucide:chevron-left"
        :label="t('toolbar.back')"
        :disabled="!canGoBack"
        @click="router.back()"
      />
      <ToolbarIconButton
        icon="lucide:chevron-right"
        :label="t('toolbar.forward')"
        :disabled="!canGoForward"
        @click="router.forward()"
      />
      <ToolbarIconButton
        icon="lucide:refresh-cw"
        :label="t('library.refresh')"
        :disabled="loading"
        :aria-busy="loading"
        @click="refresh()"
      />
    </template>
    <template #end>
      <RouterLink
        v-if="runningTask"
        to="/updates"
        class="flex h-32px max-w-260px items-center gap-8px rounded-full border border-solid border-line-subtle bg-surface-control px-10px text-12.5px text-ink-primary no-underline outline-none focus-visible:shadow-focus-ring"
      >
        <svg viewBox="0 0 18 18" class="h-16px w-16px shrink-0 -rotate-90" aria-hidden="true">
          <circle cx="9" cy="9" r="7" fill="none" class="stroke-line-default" stroke-width="2.5" />
          <circle
            cx="9"
            cy="9"
            r="7"
            fill="none"
            class="stroke-brand-coral transition-[stroke-dashoffset] duration-medium"
            stroke-width="2.5"
            stroke-linecap="round"
            :stroke-dasharray="ring.circumference"
            :stroke-dashoffset="ring.offset"
          />
        </svg>
        <span v-if="tasks.knownPercent(runningTask) !== null" class="font-600 tabular-nums">{{
          formatPercent(tasks.shownPercent(runningTask) / 100, { locale: formattingLocale })
        }}</span>
        <span class="min-w-0 truncate text-ink-secondary">{{ runningLabel }}</span>
      </RouterLink>
      <OnButton
        v-if="showOverviewToggle"
        variant="ghost"
        size="sm"
        icon="lucide:info"
        :aria-pressed="overviewOpen"
        @click="emit('toggleOverview')"
      >
        {{ t('rail.title') }}
      </OnButton>
    </template>
  </OnToolbar>
</template>
