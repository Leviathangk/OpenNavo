<script setup lang="ts">
import { computed } from 'vue';
import { OnChip, OnIcon, OnRailPanel, OnRailSection } from '@opennavo/ui';
import { useHermesContent } from '@/composables/useHermesContent';
import { useHermesStore } from '@/stores/hermes';
import { useHermesUiStore } from '@/stores/hermesUi';

const content = useHermesContent();
const hermes = useHermesStore();
const ui = useHermesUiStore();

const version = import.meta.env.VITE_APP_VERSION ?? '0.1.29';
const hermesVersion = computed(() => {
  const raw = hermes.runtime?.version ?? '';
  return /(\d+\.\d+\.\d+)/.exec(raw)?.[1] ?? (hermes.runtime?.installed ? version : '—');
});

const macLabel = computed(() => {
  if (!hermes.mac.enabled) return content.value.common.paused;
  if (hermes.mac.level === 'open') return content.value.common.open;
  if (hermes.mac.level === 'guarded') return content.value.common.guarded;
  return content.value.common.full;
});

const macTone = computed<'success' | 'neutral'>(() =>
  hermes.mac.enabled && hermes.mac.level !== 'open' ? 'success' : 'neutral'
);

function capabilityText(enabled: boolean) {
  if (!hermes.mac.enabled || hermes.mac.level === 'open') return content.value.common.off;
  if (hermes.mac.level === 'guarded') return content.value.common.ask;
  return enabled ? content.value.common.allowed : content.value.common.off;
}

const terminalText = computed(() => capabilityText(hermes.mac.terminal));
const screenText = computed(() => capabilityText(hermes.mac.screen));
const confirmText = computed(() => (hermes.mac.confirmWrites ? content.value.common.on : content.value.common.off));
</script>

<template>
  <OnRailPanel>
    <OnRailSection :title="content.rail.hermesTitle">
      <div class="flex flex-col">
        <div
          class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle py-7px text-11px"
        >
          <span class="text-ink-tertiary">{{ content.rail.runtime }}</span
          ><span class="text-right text-ink-secondary">{{
            hermes.runtimeReady ? content.rail.local : content.rail.demo
          }}</span>
        </div>
        <div
          class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle py-7px text-11px"
        >
          <span class="text-ink-tertiary">{{ content.rail.model }}</span
          ><span class="text-right text-ink-secondary">{{ hermes.modelShort }}</span>
        </div>
        <div
          class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle py-7px text-11px"
        >
          <span class="text-ink-tertiary">{{ content.rail.effort }}</span
          ><span class="text-right text-ink-secondary">{{ content.effort[hermes.model.effort] }}</span>
        </div>
        <div class="flex items-center justify-between gap-12px py-7px text-11px">
          <span class="text-ink-tertiary">{{ content.rail.memory }}</span
          ><span class="text-right text-ink-secondary">{{ content.rail.memoryLocal }}</span>
        </div>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-5px self-start font-sans text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
        @click="ui.openSettings('model')"
      >
        {{ content.rail.changeModel }} <OnIcon name="lucide:arrow-up-right" :size="12" />
      </button>
    </OnRailSection>

    <OnRailSection :title="content.rail.macTitle">
      <div class="flex items-center justify-between gap-10px">
        <span class="text-11px text-ink-tertiary">{{ content.rail.level }}</span>
        <OnChip :tone="macTone" dot>{{ macLabel }}</OnChip>
      </div>
      <div class="flex flex-col">
        <div
          class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle py-7px text-11px"
        >
          <span class="text-ink-tertiary">{{ content.rail.terminal }}</span
          ><span class="text-right text-ink-secondary">{{ terminalText }}</span>
        </div>
        <div
          class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle py-7px text-11px"
        >
          <span class="text-ink-tertiary">{{ content.rail.screen }}</span
          ><span class="text-right text-ink-secondary">{{ screenText }}</span>
        </div>
        <div class="flex items-center justify-between gap-12px py-7px text-11px">
          <span class="text-ink-tertiary">{{ content.rail.confirm }}</span
          ><span class="text-right text-ink-secondary">{{ confirmText }}</span>
        </div>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-5px self-start font-sans text-11px text-ink-tertiary outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
        @click="ui.openSettings('mac')"
      >
        {{ content.rail.manage }} <OnIcon name="lucide:arrow-up-right" :size="12" />
      </button>
    </OnRailSection>

    <OnRailSection :title="content.rail.versionTitle">
      <div class="rounded-default border border-solid border-[rgba(170,170,255,0.16)] bg-[#1B1926] p-12px">
        <div class="flex items-center justify-between gap-10px text-11px text-ink-tertiary">
          <span>OpenNavo</span><b class="text-11.5px font-600 tabular-nums text-white">{{ version }}</b>
        </div>
        <div class="mt-5px flex items-center justify-between gap-10px text-11px text-ink-tertiary">
          <span>Hermes</span><b class="text-11.5px font-600 tabular-nums text-white">{{ hermesVersion }}</b>
        </div>
        <div
          class="mt-9px flex items-center gap-5px border-t border-t-solid border-[rgba(170,170,255,0.12)] pt-8px text-11px text-ink-tertiary"
        >
          <OnIcon name="lucide:lock" :size="12" />{{ content.rail.samePackage }}
        </div>
      </div>
      <a
        href="https://hermes-agent.nousresearch.com/"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex items-center gap-5px self-start text-11px text-ink-tertiary no-underline outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
      >
        {{ content.rail.nousResearch }} <OnIcon name="lucide:arrow-up-right" :size="12" />
      </a>
    </OnRailSection>
  </OnRailPanel>
</template>
