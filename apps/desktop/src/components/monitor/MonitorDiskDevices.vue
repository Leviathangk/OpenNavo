<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnChip } from '@opennavo/ui';
import type { DiskDevice } from '@/ipc/bindings';
import { useMonitorStore } from '@/stores/monitor';
import { useMonitorFormat } from './meta';

// Physical drives and their SMART status (monitor M3); Rust reads SMART at most every 10 minutes.
const { t } = useI18n();
const store = useMonitorStore();
const f = useMonitorFormat();

const devices = computed(() => store.sample?.disk.devices ?? []);
const tone = (device: DiskDevice) =>
  device.smart === 'failing' ? 'danger' : device.smart === 'verified' ? 'success' : 'neutral';
/** Only what the drive reports: older and external drives often have no wear or write totals. */
function rows(device: DiskDevice) {
  const all: [key: string, label: string, value: string | null][] = [
    ['wear', t('monitor.disk.wear'), device.percentUsed == null ? null : f.percent(device.percentUsed)],
    ['temperature', t('monitor.disk.temperature'), device.temperatureC == null ? null : f.celsius(device.temperatureC)],
    ['powerOn', t('monitor.disk.powerOn'), device.powerOnHours == null ? null : f.span(device.powerOnHours * 3600)],
    ['written', t('monitor.disk.written'), device.dataWrittenBytes == null ? null : f.bytes(device.dataWrittenBytes)]
  ];
  return all.filter((row): row is [string, string, string] => row[2] != null);
}
</script>

<template>
  <div v-if="devices.length" class="flex flex-col gap-10px" data-testid="monitor-disk-devices">
    <h3 class="m-0 text-13px font-600 text-ink-primary">{{ t('monitor.disk.devices') }}</h3>
    <div
      v-for="device in devices"
      :key="device.id"
      class="flex flex-col gap-6px rounded-default border border-solid border-line-subtle px-12px py-10px"
    >
      <div class="flex items-center gap-8px text-12.5px">
        <span class="min-w-0 flex-1 truncate font-600 text-ink-primary">{{ device.name }}</span>
        <span class="shrink-0 text-ink-tertiary">{{
          device.internal ? t('monitor.disk.internal') : t('monitor.disk.external')
        }}</span>
        <OnChip :tone="tone(device)" :title="t('monitor.disk.smartLabel')">{{
          t(`monitor.disk.smart.${device.smart}`)
        }}</OnChip>
      </div>
      <dl v-if="rows(device).length" class="m-0 grid grid-cols-[auto_1fr] gap-x-14px gap-y-4px text-12px">
        <template v-for="[key, label, value] in rows(device)" :key="key">
          <dt class="text-ink-tertiary">{{ label }}</dt>
          <dd class="m-0 text-right tabular-nums text-ink-primary">{{ value }}</dd>
        </template>
      </dl>
    </div>
  </div>
</template>
