<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnIcon } from '@opennavo/ui';
import type { HealthIssue } from '@/ipc/bindings';
import { useMonitorStore } from '@/stores/monitor';
import { useMonitorFormat } from './meta';

// Health in three levels with concrete issues and where to act (decision 6); no score.
const emit = defineEmits<{ processes: [sort: 'cpu' | 'memory']; cleanup: []; fans: []; disk: [] }>();

const { t } = useI18n();
const store = useMonitorStore();
const f = useMonitorFormat();

const health = computed(() => store.sample?.health ?? null);
const issues = computed(() => (health.value?.issues ?? []).filter(issue => issue.level !== 'info'));
const notes = computed(() => (health.value?.issues ?? []).filter(issue => issue.level === 'info'));
const fansAvailable = computed(() => store.available.includes('fans'));

function text(issue: HealthIssue) {
  switch (issue.key) {
    case 'cpu_busy':
      return t('monitor.health.issues.cpuBusy');
    case 'memory_pressure':
      return issue.level === 'critical'
        ? t('monitor.health.issues.memoryCritical')
        : t('monitor.health.issues.memoryPressure');
    case 'disk_low':
      return t('monitor.health.issues.diskLow', { size: f.bytes(issue.value) });
    case 'battery_health':
      return t('monitor.health.issues.batteryHealth', { percent: f.percent(issue.value) });
    case 'battery_hot':
      return t('monitor.health.issues.batteryHot', { temp: f.celsius(issue.value) });
    case 'temperature':
      return t('monitor.health.issues.temperature', { temp: f.celsius(issue.value) });
    case 'disk_smart':
      return t('monitor.health.issues.diskSmart', { name: issue.subject ?? t('monitor.modules.disk') });
    case 'uptime': {
      const days = Math.round(issue.value ?? 0);
      return t('monitor.health.issues.uptime', { count: days }, { plural: days });
    }
  }
  return '';
}
function actions(issue: HealthIssue) {
  switch (issue.key) {
    case 'cpu_busy':
      return [
        {
          key: 'cpu',
          label: t('monitor.health.actions.processes'),
          run: () => emit('processes', 'cpu')
        }
      ];
    case 'memory_pressure':
      return [
        {
          key: 'memory',
          label: t('monitor.health.actions.processes'),
          run: () => emit('processes', 'memory')
        }
      ];
    case 'disk_low':
      return [{ key: 'cleanup', label: t('monitor.health.actions.cleanup'), run: () => emit('cleanup') }];
    case 'disk_smart':
      return [
        {
          key: 'disk',
          label: t('monitor.detail.open', { name: t('monitor.modules.disk') }),
          run: () => emit('disk')
        }
      ];
    case 'temperature':
      return [
        {
          key: 'cpu',
          label: t('monitor.health.actions.processes'),
          run: () => emit('processes', 'cpu')
        },
        ...(fansAvailable.value
          ? [{ key: 'fans', label: t('monitor.health.actions.fans'), run: () => emit('fans') }]
          : [])
      ];
  }
  return [];
}
</script>

<template>
  <div v-if="health" data-testid="monitor-health">
    <section
      v-if="issues.length"
      class="flex items-start gap-10px rounded-big border border-solid px-14px py-12px text-13px text-ink-secondary"
      :class="
        health.level === 'critical'
          ? 'border-[rgba(240,82,82,0.25)] bg-status-danger-subtle'
          : 'border-[rgba(250,178,25,0.25)] bg-status-warning-subtle'
      "
      role="status"
    >
      <OnIcon
        name="triangle-alert"
        :size="16"
        class="mt-1px shrink-0"
        :class="health.level === 'critical' ? 'text-status-danger' : 'text-status-warning'"
      />
      <div class="min-w-0 flex-1">
        <b class="text-ink-primary">{{
          health.level === 'critical'
            ? t('monitor.health.critical', { count: issues.length }, { plural: issues.length })
            : t('monitor.health.attention', { count: issues.length }, { plural: issues.length })
        }}</b>
        <div v-for="issue in issues" :key="issue.key" class="mt-6px flex items-center gap-8px">
          <span class="min-w-0 flex-1">· {{ text(issue) }}</span>
          <OnButton
            v-for="action in actions(issue)"
            :key="action.key"
            variant="secondary"
            size="xs"
            @click="action.run()"
            >{{ action.label }}</OnButton
          >
        </div>
      </div>
    </section>
    <section
      v-else
      class="flex items-center gap-10px rounded-big border border-solid border-[rgba(63,207,108,0.2)] bg-status-success-subtle px-14px py-12px text-13px text-ink-secondary"
      role="status"
    >
      <OnIcon name="check" :size="16" class="shrink-0 text-status-success" />
      <span
        ><b class="text-ink-primary">{{ t('monitor.health.good') }}</b> ·
        {{
          store.available.includes('battery') ? t('monitor.health.goodBody') : t('monitor.health.goodBodyNoBattery')
        }}</span
      >
    </section>
    <p v-for="note in notes" :key="note.key" class="m-0 mt-6px text-12px text-ink-tertiary">
      {{ text(note) }}
    </p>
  </div>
</template>
