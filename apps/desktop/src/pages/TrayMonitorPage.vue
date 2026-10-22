<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { OnButton, OnIcon } from '@opennavo/ui';
import { commands, events, unwrap } from '@/ipc/client';
import MonitorModuleBody from '@/components/monitor/MonitorModuleBody.vue';
import MonitorProcesses from '@/components/monitor/MonitorProcesses.vue';
import { MODULE_ICONS } from '@/components/monitor/meta';
import { useTrayWindowSize } from '@/composables/useTrayWindowSize';
import { MODULES, useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';

// The shared menu bar popover (§12): Rust moves one window under the clicked item and says which module via
// monitor:popover; it shows that module and subscribes only while visible.
const content = useTemplateRef<HTMLDivElement>('content');
useTrayWindowSize(content);

const { t } = useI18n();
const route = useRoute();
const store = useMonitorStore();
const fromQuery = () =>
  typeof route.query.module === 'string' && (MODULES as readonly string[]).includes(route.query.module)
    ? (route.query.module as DisplayModule)
    : null;
const module = ref<DisplayModule>(fromQuery() ?? 'cpu');
let stop: (() => void) | undefined;
let disposed = false;

function subscribe() {
  if (disposed || document.visibilityState === 'hidden') return;
  void store.subscribe('popover', [module.value]).catch(() => undefined);
}
function onVisibility() {
  if (document.visibilityState === 'hidden') void store.unsubscribe('popover');
  else subscribe();
}

onMounted(async () => {
  stop = await events.monitorPopover.listen(event => {
    // Fans have no menu bar item of their own; they are shown with the sensors.
    module.value = event.payload === 'fans' ? 'sensors' : event.payload;
  });
  if (disposed) {
    stop();
    return;
  }
  document.addEventListener('visibilitychange', onVisibility);
  subscribe();
});
watch(module, subscribe);
watch(fromQuery, next => {
  if (next) module.value = next;
});
onBeforeUnmount(() => {
  disposed = true;
  stop?.();
  document.removeEventListener('visibilitychange', onVisibility);
  void store.unsubscribe('popover');
});

const openInMain = (target: 'detail' | 'settings') =>
  void unwrap(commands.monitorOpenInMain(target, target === 'detail' ? module.value : null)).catch(() => undefined);
</script>

<template>
  <div ref="content" class="w-332px max-w-full bg-transparent p-6px">
    <section
      class="flex flex-col gap-10px rounded-panel bg-surface-card p-14px text-ink-primary"
      data-testid="monitor-popover"
    >
      <header class="flex items-center gap-8px">
        <span class="grid h-22px w-22px place-items-center rounded-[6px] bg-surface-raised text-ink-secondary">
          <OnIcon :name="MODULE_ICONS[module]" :size="14" />
        </span>
        <b class="text-14px font-600">{{ t(`monitor.modules.${module}`) }}</b>
        <span v-if="store.system" class="ml-auto truncate text-12px text-ink-tertiary">{{
          store.system.modelName
        }}</span>
      </header>
      <MonitorModuleBody :module="module" variant="popover" />
      <MonitorProcesses v-if="module === 'cpu'" sort="cpu" :limit="3" compact />
      <MonitorProcesses v-else-if="module === 'memory'" sort="memory" :limit="3" compact />
      <footer class="flex gap-8px">
        <OnButton variant="primary" size="sm" class="flex-1" @click="openInMain('detail')">{{
          t('monitor.popover.open')
        }}</OnButton>
        <OnButton
          variant="secondary"
          size="sm"
          icon="sliders-horizontal"
          :aria-label="t('monitor.popover.settings')"
          @click="openInMain('settings')"
        />
      </footer>
    </section>
  </div>
</template>

<style>
/* Transparent outer corners like the updates popover; only the panel is opaque. */
html[data-opennavo-window='tray-monitor'] {
  background: transparent;
  color-scheme: normal;
}

html[data-opennavo-window='tray-monitor'] body {
  overflow: hidden;
  background: transparent;
}
</style>
