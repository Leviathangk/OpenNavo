<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatDuration, formatSpeed, formatVersionChange } from '@opennavo/shared';
import { OnTaskCard } from '@opennavo/ui';
import type { Task } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useTasksStore } from '@/stores';

const { appLocale: formattingLocale } = useFormattingLocale();

// Running task card (mockup 05): title, version change, steps, progress, speed/ETA, recent logs, queued packages.
const props = defineProps<{ task: Task }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { displayName, taskIcon } = usePackageIdentity();
const tasks = useTasksStore();

const name = computed(() =>
  props.task.op !== 'clean' && props.task.target ? displayName(props.task.target.kind, props.task.target.token) : ''
);
const subtitle = computed(() => {
  const parts: string[] = [];
  if (props.task.fromVersion && props.task.toVersion)
    parts.push(formatVersionChange(props.task.fromVersion, props.task.toVersion));
  if (props.task.stepIndex && props.task.stepCount && props.task.phase)
    parts.push(
      t(
        'tasks.step',
        {
          index: props.task.stepIndex,
          count: props.task.stepCount,
          phase: t(`tasks.phases.${props.task.phase}`)
        },
        { plural: props.task.stepCount }
      )
    );
  return parts.join(' · ');
});
const remaining = computed(() => {
  const { bytesDone, bytesTotal, speedBps } = props.task;
  if (!bytesTotal || bytesDone === null || !speedBps) return undefined;
  return t('tasks.remaining', {
    time: formatDuration((bytesTotal - bytesDone) / speedBps, { locale: appLocale.value })
  });
});
const queue = computed(() =>
  tasks.queued.flatMap(task => {
    const pkg = taskIcon(task);
    return pkg ? [pkg] : [];
  })
);
</script>

<template>
  <OnTaskCard
    :title="t(`tasks.running.${task.op}`, { name })"
    :subtitle="subtitle"
    :pkg="taskIcon(task)"
    :progress="task.percent"
    :transferred="task.bytesDone !== null ? formatBytes(task.bytesDone, { locale: formattingLocale }) : undefined"
    :total="task.bytesTotal ? formatBytes(task.bytesTotal, { locale: formattingLocale }) : undefined"
    :speed="task.speedBps ? formatSpeed(task.speedBps, { locale: formattingLocale }) : undefined"
    :remaining="remaining"
    :log="tasks.logs[task.id] ?? []"
    :queue="queue"
    :queue-note="t('tasks.oneAtATime')"
    cancellable
    @cancel="tasks.cancel(task.id)"
  />
</template>
