<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatDate, formatDuration, formatTime, formatVersionChange } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnChip, OnIcon, OnLogViewer, OnModal } from '@opennavo/ui';
import type { Task } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useUninstallUndo } from '@/composables/useUninstallUndo';
import { useTasksStore } from '@/stores';
import { isDeferredUpdate } from '@/stores/runningApps';
import { recheckRoute, retryOptions } from '@/stores/permissionPrompt';

// Local history (mockup 05, 06 §8: completed tasks), grouped by day; failures show reasons, logs, and retry (trigger=retry).
const props = defineProps<{ tasks: readonly Task[] }>();

const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const { taskIcon, taskName } = usePackageIdentity();
const { errorText } = usePackageState();
const queue = useTasksStore();
const router = useRouter();
const toasts = useToasts();
const { undo, purge } = useUninstallUndo();

// Uninstall and clean-up runs that moved files to the Trash stay reversible here, also after a restart.
const settled = ref(new Set<string>());
const reversible = (task: Task) =>
  (task.op === 'uninstall' || task.op === 'clean') && task.outcome?.restorable === true && !settled.value.has(task.id);
async function reverse(task: Task, action: 'undo' | 'purge') {
  if (await (action === 'undo' ? undo([task.id]) : purge([task.id])))
    settled.value = new Set([...settled.value, task.id]);
}

const DAY = 24 * 60 * 60 * 1000;

function dayLabel(at: number): string {
  const date = formatDate(at, { locale: appLocale.value });
  const start = new Date();
  start.setHours(0, 0, 0, 0);
  if (at >= start.getTime()) return t('updates.history.today', { date });
  if (at >= start.getTime() - DAY) return t('updates.history.yesterday', { date });
  return date;
}

const groups = computed(() => {
  const map = new Map<string, Task[]>();
  for (const task of props.tasks) {
    const label = dayLabel(task.finishedAt ?? task.createdAt);
    map.set(label, [...(map.get(label) ?? []), task]);
  }
  return [...map.entries()].map(([label, items]) => ({ label, items }));
});

/** Clean Up runs and uninstalled apps fall back to the op or the name recorded with the task. */
function title(task: Task): string {
  return taskName(task) || t(`tasks.ops.${task.op}`);
}

function meta(task: Task): string {
  const parts: string[] = [];
  if (task.fromVersion && task.toVersion) parts.push(formatVersionChange(task.fromVersion, task.toVersion));
  else if (task.toVersion) parts.push(task.toVersion);
  if (task.op !== 'upgrade') parts.push(t(`tasks.ops.${task.op}`));
  parts.push(t(`tasks.triggers.${task.trigger}`));
  if (task.startedAt && task.finishedAt)
    parts.push(formatDuration((task.finishedAt - task.startedAt) / 1000, { locale: appLocale.value }));
  return parts.join(' · ');
}

function failure(task: Task): string | null {
  if ((task.state !== 'failed' && !isDeferredUpdate(task)) || !task.error) return null;
  const key = `errors.${task.error.code}`;
  return te(key) ? t(key) : task.error.message;
}

const logOpen = ref(false);
const logTitle = ref('');
const logLines = ref<string[]>([]);

async function viewLog(task: Task) {
  logTitle.value = t('updates.log.title', { name: title(task) });
  const text = await unwrap(commands.taskLog(task.id, 2000)).catch(() => '');
  logLines.value = text ? text.split('\n') : [];
  logOpen.value = true;
}

async function retry(task: Task) {
  try {
    const route = recheckRoute(task);
    if (route) await router.push(route);
    else await queue.enqueue(task.op, task.target, 'retry', retryOptions(task));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div class="flex flex-col gap-18px">
    <section v-for="group in groups" :key="group.label">
      <h3 class="m-0 mb-8px text-13px font-500 text-ink-tertiary">{{ group.label }}</h3>
      <ul class="m-0 flex list-none flex-col gap-8px p-0">
        <li
          v-for="task in group.items"
          :key="task.id"
          class="flex items-center gap-14px rounded-big border border-solid bg-surface-card px-16px py-12px"
          :class="task.state === 'failed' ? 'border-status-danger' : 'border-line-subtle'"
        >
          <span class="w-44px shrink-0 font-mono text-12.5px text-ink-tertiary">{{
            formatTime(task.finishedAt ?? task.createdAt)
          }}</span>
          <OnAppIcon v-if="taskIcon(task)" v-bind="taskIcon(task)!" :size="28" />
          <span
            v-else-if="task.op === 'clean'"
            class="grid h-28px w-28px shrink-0 place-items-center rounded-[7px] bg-surface-raised text-ink-secondary"
            aria-hidden="true"
          >
            <OnIcon name="eraser" :size="15" />
          </span>
          <span class="min-w-0 flex-1">
            <span class="block truncate text-13.5px text-ink-primary">
              <b class="font-600">{{ title(task) }}</b>
              <span class="ml-6px text-12.5px text-ink-tertiary">{{ meta(task) }}</span>
            </span>
            <span
              v-if="failure(task)"
              class="mt-2px block text-12.5px"
              :class="isDeferredUpdate(task) ? 'text-ink-secondary' : 'text-status-danger'"
              >{{ failure(task) }}</span
            >
          </span>
          <template v-if="task.state === 'failed' || isDeferredUpdate(task)">
            <OnChip v-if="isDeferredUpdate(task)">{{ t('runningApps.deferred') }}</OnChip>
            <OnButton variant="ghost" size="sm" @click="viewLog(task)">{{ t('updates.history.viewLog') }}</OnButton>
            <OnButton variant="secondary" size="sm" @click="retry(task)">{{
              t(recheckRoute(task) ? 'updates.history.recheck' : 'updates.history.retry')
            }}</OnButton>
          </template>
          <!-- A partly finished uninstall can be undone as well. -->
          <template v-if="reversible(task)">
            <OnButton variant="ghost" size="sm" @click="reverse(task, 'purge')">{{
              t('updates.history.purge')
            }}</OnButton>
            <OnButton variant="secondary" size="sm" icon="undo-2" @click="reverse(task, 'undo')">{{
              t('updates.history.undo')
            }}</OnButton>
          </template>
          <template v-else-if="task.state !== 'failed' && !isDeferredUpdate(task)">
            <OnChip v-if="task.state === 'succeeded'" tone="success" icon="check">{{
              t('updates.history.succeeded')
            }}</OnChip>
            <OnChip v-else>{{ t('updates.history.canceled') }}</OnChip>
          </template>
        </li>
      </ul>
    </section>
    <OnModal v-model:open="logOpen" size="lg" :title="logTitle">
      <OnLogViewer v-if="logLines.length" :lines="logLines" :height="360" :label="logTitle" />
      <p v-else class="m-0 text-13px text-ink-tertiary">{{ t('updates.log.empty') }}</p>
    </OnModal>
  </div>
</template>
