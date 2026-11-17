<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { OnButton, OnIcon } from '@opennavo/ui';
import { commands, events, unwrap } from '@/ipc/client';
import type { MonitorModule } from '@/ipc/bindings';
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
const tabs = ref<DisplayModule[]>([]);
const combined = ref(false);
let stop: (() => void) | undefined;
let disposed = false;

const normalize = (value: MonitorModule): DisplayModule => (value === 'fans' ? 'sensors' : value);
const canShow = (value: DisplayModule) => (MODULES as readonly string[]).includes(value);

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
    const modules = [...new Set(event.payload.modules.map(normalize).filter(canShow))];
    const selected = normalize(event.payload.selected);
    combined.value = event.payload.combined;
    tabs.value = modules;
    module.value = modules.includes(selected) ? selected : (modules[0] ?? 'cpu');
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

function selectModule(next: DisplayModule) {
  module.value = next;
}
</script>

<template>
  <div ref="content" class="w-332px max-w-full bg-transparent p-6px">
    <section
      class="flex flex-col gap-10px rounded-panel border border-solid border-line-default bg-surface-overlay p-14px text-ink-primary backdrop-blur-[20px]"
      data-testid="monitor-popover"
    >
      <header class="flex items-center gap-8px">
        <span
          class="grid h-22px w-22px place-items-center rounded-[6px] border border-solid border-line-subtle bg-surface-card-alt text-ink-secondary"
        >
          <OnIcon :name="MODULE_ICONS[module]" :size="14" />
        </span>
        <b class="text-14px font-600">{{ t(`monitor.modules.${module}`) }}</b>
        <span v-if="store.system" class="ml-auto truncate text-12px text-ink-tertiary">{{
          store.system.modelName
        }}</span>
      </header>
      <div
        v-if="combined && tabs.length > 1"
        class="flex flex-wrap gap-2px"
        role="group"
        :aria-label="t('monitor.title')"
        data-testid="monitor-popover-tabs"
      >
        <button
          v-for="tab in tabs"
          :key="tab"
          type="button"
          class="grid h-24px w-24px place-items-center rounded-[6px] border border-solid outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring"
          :class="
            tab === module
              ? 'border-line-strong bg-component-segment-active text-ink-primary'
              : 'border-transparent bg-transparent text-ink-tertiary hover:text-ink-primary'
          "
          :aria-label="t(`monitor.modules.${tab}`)"
          :aria-pressed="tab === module"
          :title="t(`monitor.modules.${tab}`)"
          :data-module="tab"
          data-testid="monitor-popover-tab"
          @click="selectModule(tab)"
        >
          <OnIcon :name="MODULE_ICONS[tab]" :size="14" />
        </button>
      </div>
      <MonitorModuleBody :module="module" variant="popover" @open-fans="openInMain('detail')" />
      <MonitorProcesses v-if="module === 'cpu'" sort="cpu" :limit="3" compact />
      <MonitorProcesses v-else-if="module === 'memory'" sort="memory" :limit="3" compact />
      <MonitorProcesses v-else-if="module === 'network'" sort="network" :limit="3" compact />
      <footer class="flex gap-8px border-t border-t-solid border-line-subtle pt-10px">
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
