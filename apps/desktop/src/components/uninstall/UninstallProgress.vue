<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatPercent } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnIcon, OnLogViewer, OnProgressMeter } from '@opennavo/ui';
import type { Task } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useTasksStore } from '@/stores';

// Running uninstall (proposal §03 scenario 9): one row per app task from the shared queue, plus its log.
// Phases reuse the queue's: preparing (quit) → uninstalling (bundle / brew) → cleaning (related files) → finishing.
const props = defineProps<{ tasks: readonly Task[] }>();

const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const queue = useTasksStore();
const { taskIcon, taskName } = usePackageIdentity();

// Leftover clean-ups have no target; fall back to a tile keyed by the task.
const iconOf = (task: Task) =>
  taskIcon(task) ?? {
    kind: 'cask' as const,
    token: task.id,
    name: taskName(task),
    src: null,
    accent: null
  };

const logOpen = ref(false);
const lines = computed(() =>
  props.tasks.flatMap(task =>
    (props.tasks.length > 1 ? [`==> ${taskName(task)}`] : []).concat(queue.logs[task.id] ?? [])
  )
);

function status(task: Task) {
  if (task.state === 'queued') return t('uninstall.waiting');
  if (task.state === 'succeeded') return t('uninstall.result.finished');
  if (task.state === 'failed' || task.state === 'canceled') return t('uninstall.result.notFinished');
  // Uninstall-specific wording for its four phases; any other phase falls back to the queue's labels.
  const key = `uninstall.phases.${task.phase}`;
  const phase = task.phase ? (te(key) ? t(key) : t(`tasks.phases.${task.phase}`)) : t('library.status.runningUnknown');
  return task.percent === null ? phase : `${phase} · ${formatPercent(task.percent / 100, { locale: appLocale.value })}`;
}
</script>

<template>
  <div class="flex flex-col gap-12px pb-4px">
    <div
      v-for="task in tasks"
      :key="task.id"
      class="flex items-center gap-12px rounded-panel border border-solid border-line-subtle bg-surface-card px-14px py-12px"
    >
      <OnAppIcon v-bind="iconOf(task)" :size="32" />
      <span class="min-w-0 flex-1">
        <span class="flex items-center justify-between gap-8px">
          <span class="truncate text-13.5px font-600 text-ink-primary">{{ taskName(task) }}</span>
          <span class="flex shrink-0 items-center gap-5px text-12px text-ink-tertiary">
            <OnIcon v-if="task.state === 'succeeded'" name="check" :size="13" class="text-status-success" />
            {{ status(task) }}
          </span>
        </span>
        <OnProgressMeter
          v-if="task.state === 'running' || task.state === 'queued'"
          class="mt-8px"
          :value="task.state === 'queued' ? 0 : task.percent"
          :label="taskName(task)"
        />
      </span>
    </div>
    <div>
      <OnButton variant="ghost" size="xs" icon="square-terminal" @click="logOpen = !logOpen">
        {{ logOpen ? t('uninstall.hideLog') : t('uninstall.log') }}
      </OnButton>
      <OnLogViewer v-if="logOpen" class="mt-8px" :lines="lines" :height="160" :label="t('uninstall.log')" />
    </div>
  </div>
</template>
