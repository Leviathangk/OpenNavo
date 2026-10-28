<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnIcon } from '@opennavo/ui';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import MonitorModuleBody from './MonitorModuleBody.vue';
import { MODULE_ICONS } from './meta';
import { useModuleView } from './useModuleView';

// One card of the overview (§12): current value, last minute, a few numbers; the whole card opens its detail.
// preview (permission gate locked): only what the module is, no readings even when the menu bar keeps sampling.
const props = defineProps<{ module: DisplayModule; preview?: boolean }>();
const emit = defineEmits<{ open: [module: DisplayModule]; cleanup: [] }>();

const { t } = useI18n();
const store = useMonitorStore();
const onMenuBar = computed(() => store.item(props.module).enabled);
const { view } = useModuleView(
  () => props.module,
  () => 'card'
);
</script>

<template>
  <article
    class="group relative flex min-w-0 cursor-pointer flex-col gap-8px rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px transition-colors duration-fast ease-standard hover:border-line-default focus-visible:shadow-focus-ring focus-visible:outline-none"
    role="button"
    tabindex="0"
    :aria-label="t('monitor.detail.open', { name: t(`monitor.modules.${module}`) })"
    :data-module="module"
    @click="emit('open', module)"
    @keydown.enter.prevent="emit('open', module)"
    @keydown.space.prevent="emit('open', module)"
  >
    <header class="flex items-center gap-9px">
      <span class="grid h-26px w-26px shrink-0 place-items-center rounded-[7px] bg-surface-raised text-ink-secondary">
        <OnIcon :name="MODULE_ICONS[module]" :size="15" />
      </span>
      <b class="whitespace-nowrap text-13.5px font-600 text-ink-primary">{{ t(`monitor.modules.${module}`) }}</b>
      <span
        v-if="onMenuBar"
        class="shrink-0 rounded-[5px] bg-status-success-subtle px-6px text-10.5px font-600 leading-18px text-status-success"
        :title="t('monitor.menuBar.onHint')"
        >{{ t('monitor.menuBar.on') }}</span
      >
      <span
        class="ml-auto whitespace-nowrap text-20px font-600 tabular-nums tracking-[-0.01em]"
        :class="!preview && view?.warn ? 'text-status-warning' : 'text-ink-primary'"
        >{{ (!preview && view?.value) || '—' }}</span
      >
      <OnIcon
        name="chevron-right"
        :size="14"
        class="-ml-4px shrink-0 text-ink-tertiary opacity-55 transition-opacity duration-fast group-hover:opacity-100"
      />
    </header>
    <MonitorModuleBody v-if="!preview" :module="module" variant="card" @cleanup="emit('cleanup')" />
  </article>
</template>
