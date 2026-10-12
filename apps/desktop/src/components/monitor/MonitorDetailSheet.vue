<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnSheet } from '@opennavo/ui';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import MenuBarItemSettings from './MenuBarItemSettings.vue';
import MonitorModuleBody from './MonitorModuleBody.vue';
import MonitorProcesses from './MonitorProcesses.vue';
import type { MonitorRange } from './useModuleView';

// A module's full detail (§12), opened from its card or from the menu bar popover; subscribes only while open.
const props = defineProps<{ module: DisplayModule | null }>();
const emit = defineEmits<{ close: []; cleanup: [] }>();

const { t } = useI18n();
const store = useMonitorStore();
const range = ref<MonitorRange>('minute');

watch(
  () => props.module,
  module => {
    range.value = 'minute';
    if (module) void store.subscribe('detail', [module]).catch(() => undefined);
    else void store.unsubscribe('detail');
  },
  { immediate: true }
);
onBeforeUnmount(() => void store.unsubscribe('detail'));

/** The hour view reads Rust's 5-second trend once; live samples extend it while the sheet stays open. */
function setRange(next: MonitorRange) {
  range.value = next;
  if (next === 'hour') void store.loadTrend().catch(() => undefined);
}
</script>

<template>
  <OnSheet
    :open="Boolean(module)"
    :title="module ? t(`monitor.modules.${module}`) : ''"
    @update:open="value => (value ? undefined : emit('close'))"
  >
    <div v-if="module" class="flex flex-col gap-16px" data-testid="monitor-detail">
      <MonitorModuleBody
        :module="module"
        variant="detail"
        :range="range"
        @update:range="setRange"
        @cleanup="emit('cleanup')"
      />
      <MonitorProcesses
        v-if="module === 'cpu' || module === 'memory' || module === 'disk' || module === 'network'"
        :sort="module"
        :limit="6"
      />
      <MenuBarItemSettings :module="module" />
    </div>
  </OnSheet>
</template>
