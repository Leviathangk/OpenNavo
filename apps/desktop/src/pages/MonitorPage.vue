<script setup lang="ts">
import { usePermissionGateStore } from '@/stores/permissionGate';
import PermissionGate from '@/components/permissions/PermissionGate.vue';
import { computed, nextTick, onBeforeUnmount, watch, ref, useTemplateRef } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { OnButton, OnPageHeader } from '@opennavo/ui';
import MonitorCard from '@/components/monitor/MonitorCard.vue';
import MonitorDetailSheet from '@/components/monitor/MonitorDetailSheet.vue';
import MonitorHealth from '@/components/monitor/MonitorHealth.vue';
import MonitorProcesses from '@/components/monitor/MonitorProcesses.vue';
import type { ProcessSort } from '@/components/monitor/meta';
import { useMonitorFormat } from '@/components/monitor/meta';
import { MODULES, useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';

// Computer monitoring (monitor proposal §11–§12): health, a card per module, top processes; a card opens its detail.
const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const store = useMonitorStore();
const gate = usePermissionGateStore();
const f = useMonitorFormat();
const sort = ref<ProcessSort>('cpu');
const processes = useTemplateRef<HTMLElement>('processes');

const detail = computed<DisplayModule | null>(() => {
  if (!gate.ready) return null;
  const value = route.query.detail;
  return typeof value === 'string' && (MODULES as readonly string[]).includes(value) ? (value as DisplayModule) : null;
});
const subtitle = computed(() => {
  const system = store.system;
  if (!system) return gate.ready ? t('monitor.subtitleLoading') : undefined;
  return t('monitor.subtitle', {
    model: system.modelName,
    chip: system.chip,
    memory: f.bytes(system.memoryBytes),
    os: system.osVersion,
    uptime: f.span(Math.max(0, ((store.sample?.at ?? Date.now()) - system.bootedAt) / 1000))
  });
});

watch(
  () => gate.ready,
  ready => {
    if (ready) void store.subscribe('page', [...MODULES]).catch(() => undefined);
    // Locked: no sampling, but list this Mac's modules so the page shows what it offers (cards read 「—」).
    else
      void store
        .unsubscribe('page')
        .then(() => store.loadAvailable())
        .catch(() => undefined);
  },
  { immediate: true }
);
onBeforeUnmount(() => void store.unsubscribe('page'));

function open(module: DisplayModule) {
  void router.replace({ query: { ...route.query, detail: module } });
}
function close() {
  const query = { ...route.query };
  delete query.detail;
  void router.replace({ query });
}
async function showProcesses(next: ProcessSort) {
  sort.value = next;
  await nextTick();
  processes.value?.scrollIntoView({ behavior: 'smooth', block: 'start' });
}
const cleanup = () => void router.push('/cleanup');
</script>

<template>
  <div class="relative flex flex-col gap-12px">
    <PermissionGate feature="monitor">
      <template #header>
        <OnPageHeader :title="t('monitor.title')" :subtitle="subtitle">
          <template #actions>
            <OnButton variant="secondary" icon="sliders-horizontal" @click="router.push('/settings/monitor')">{{
              t('monitor.menuBarButton', { count: store.menuBarCount }, { plural: store.menuBarCount })
            }}</OnButton>
          </template>
        </OnPageHeader>
      </template>
      <MonitorHealth
        v-if="gate.ready"
        @processes="showProcesses"
        @cleanup="cleanup"
        @fans="open('sensors')"
        @disk="open('disk')"
      />

      <div class="grid grid-cols-2 gap-10px" data-testid="monitor-cards">
        <MonitorCard
          v-for="module in store.modules"
          :key="module"
          :module="module"
          :preview="!gate.ready"
          @open="open"
          @cleanup="cleanup"
        />
      </div>

      <div ref="processes">
        <MonitorProcesses v-model:sort="sort" tabs :waiting="gate.ready" />
      </div>

      <MonitorDetailSheet :module="detail" @close="close" @cleanup="cleanup" />
    </PermissionGate>
  </div>
</template>
