<script setup lang="ts">
import { computed } from 'vue';
import { OnButton, OnChip, OnIcon, OnRailPanel, OnRailSection } from '@opennavo/ui';
import { useHermesContent } from '@/composables/useHermesContent';
import HermesProfileSection from '@/components/hermes/HermesProfileSection.vue';
import { useHermesStore } from '@/stores/hermes';
import { useHermesUiStore } from '@/stores/hermesUi';

const content = useHermesContent();
const hermes = useHermesStore();
const ui = useHermesUiStore();

const version = import.meta.env.VITE_APP_VERSION ?? '0.1.29';
const remote = computed(() => hermes.remoteActive);
const profile = computed(() => hermes.activeProfile);
const profileStatus = computed(() =>
  profile.value.status === 'connected'
    ? content.value.profiles.connected
    : profile.value.status === 'error'
      ? content.value.profiles.error
      : content.value.profiles.offline
);
const hermesVersion = computed(() => {
  if (remote.value) return profile.value.version || '—';
  const raw = hermes.runtime?.version ?? '';
  return /(\d+\.\d+\.\d+)/.exec(raw)?.[1] ?? '—';
});
const runtimeUpdateLabel = computed(() =>
  content.value.rail.updateAvailable.replace('{version}', hermes.runtime?.expectedVersion ?? '')
);

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
    <OnRailSection :title="content.profiles.connection">
      <div class="flex flex-col gap-9px">
        <div class="flex items-center gap-8px">
          <span
            class="flex h-28px w-28px shrink-0 items-center justify-center rounded-small border border-solid"
            :class="
              remote
                ? 'border-[rgba(145,135,255,0.18)] bg-[rgba(145,135,255,0.09)] text-ink-accent'
                : 'border-[rgba(87,201,138,0.17)] bg-[rgba(87,201,138,0.07)] text-status-success'
            "
          >
            <OnIcon :name="remote ? 'lucide:cloud' : 'lucide:hard-drive'" :size="15" />
          </span>
          <span class="min-w-0 flex-1"
            ><b class="block truncate text-12px font-600 text-ink-primary">{{ profile.name }}</b
            ><small class="mt-1px block truncate text-10px text-ink-tertiary">{{
              remote ? profile.endpoint : content.profiles.localRuntime
            }}</small></span
          >
          <span
            class="flex shrink-0 items-center gap-5px text-10px"
            :class="
              profile.status === 'connected'
                ? 'text-status-success'
                : profile.status === 'error'
                  ? 'text-status-danger'
                  : 'text-ink-tertiary'
            "
            ><i
              class="h-6px w-6px rounded-full"
              :class="
                profile.status === 'connected'
                  ? 'bg-status-success'
                  : profile.status === 'error'
                    ? 'bg-status-danger'
                    : 'bg-status-warning'
              "
            ></i
            >{{ profileStatus }}</span
          >
        </div>
        <div class="flex items-center justify-between gap-10px text-11px">
          <span class="text-ink-tertiary">{{ content.profiles.protocol }}</span
          ><span class="text-right text-ink-secondary">{{
            remote ? 'Hermes ' + (profile.version || '—') : 'Hermes ' + hermesVersion
          }}</span>
        </div>
        <div v-if="remote" class="flex items-center justify-between gap-10px text-11px">
          <span class="text-ink-tertiary">{{ content.profiles.latency }}</span
          ><span class="text-right tabular-nums text-ink-secondary">{{
            profile.latency ? profile.latency + ' ms' : '—'
          }}</span>
        </div>
        <button
          type="button"
          class="flex h-28px w-full items-center justify-center gap-5px rounded-small border border-solid border-line-default bg-transparent font-sans text-11px text-ink-secondary outline-none hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
          @click="ui.openProfiles()"
        >
          <OnIcon name="lucide:settings" :size="12" />{{ content.profiles.manageConnection }}
        </button>
      </div>
    </OnRailSection>

    <HermesProfileSection />

    <OnRailSection :title="content.rail.hermesTitle">
      <div class="flex flex-col">
        <div
          class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle py-7px text-11px"
        >
          <span class="text-ink-tertiary">{{ content.rail.runtime }}</span
          ><span class="text-right text-ink-secondary">{{
            remote ? content.profiles.remoteOnly : hermes.runtimeReady ? content.rail.local : content.rail.demo
          }}</span>
        </div>
        <div
          v-if="!remote && hermes.runtime?.outdated"
          class="flex flex-col items-start gap-7px border-b border-b-solid border-line-subtle py-8px"
        >
          <span class="text-11px leading-[1.45] text-status-warning">{{ runtimeUpdateLabel }}</span>
          <OnButton variant="accent" size="sm" :loading="hermes.installing" @click="hermes.installRuntime()">{{
            content.install.updateRuntime
          }}</OnButton>
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
      <template v-if="!remote">
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
      </template>
      <div
        v-else
        class="rounded-small border border-solid border-line-subtle bg-surface-card px-10px py-9px text-11px leading-[1.55] text-ink-tertiary"
      >
        <div class="flex items-center gap-6px text-ink-secondary">
          <OnIcon name="lucide:wifi-off" :size="13" />{{ content.profiles.macUnavailable }}
        </div>
        <p class="m-0 mt-5px text-10px text-ink-tertiary">{{ content.profiles.remoteWorkspace }}</p>
      </div>
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
