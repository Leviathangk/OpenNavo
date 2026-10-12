<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { OnButton, OnCard, OnIcon, OnRailPanel, OnRailSection, OnToggle } from '@opennavo/ui';
import MenuBarWidget from '@/components/monitor/MenuBarWidget.vue';
import { MODULE_ICONS, useMonitorFormat } from '@/components/monitor/meta';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';

// Monitoring rail (§12): what is on the menu bar with a switch per module, fan status, privacy.
const { t } = useI18n();
const router = useRouter();
const store = useMonitorStore();
const settings = useSettingsStore();
const toasts = useToasts();
const f = useMonitorFormat();
const { errorText } = usePackageState();

const shown = computed(() => store.modules.filter(module => store.item(module).enabled));
const seconds = computed(() => (settings.value?.monitorIntervalMs ?? 2000) / 1000);
/** Past six items the notch starts hiding some; macOS does not tell us which. */
const crowded = computed(() => shown.value.length >= 6);
const fanText = computed(() => {
  const fans = store.fans ?? [];
  const manual = fans.find(fan => fan.mode === 'manual');
  return manual ? t('monitor.fans.modeManual', { rpm: f.rpm(manual.targetRpm) }) : t('monitor.fans.modeAuto');
});

async function toggle(module: DisplayModule, enabled: boolean) {
  try {
    await store.setMenuBar(module, { enabled });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <OnRailPanel>
    <OnRailSection :title="t('monitor.rail.menuBar')" :subtitle="t('monitor.rail.menuBarSub')">
      <OnCard padding="md" class="flex flex-col gap-4px">
        <div
          class="mb-8px flex min-h-30px flex-wrap items-center justify-end gap-x-2px gap-y-4px rounded-[8px] border border-solid border-line-default bg-[rgba(38,38,40,0.96)] px-6px py-4px"
          data-testid="monitor-menubar-preview"
        >
          <MenuBarWidget v-for="module in shown" :key="module" :module="module" class="mx-6px" />
          <span v-if="!shown.length" class="px-4px text-12px text-ink-tertiary">{{ t('monitor.rail.empty') }}</span>
        </div>
        <div
          v-for="module in store.modules"
          :key="module"
          class="flex items-center gap-8px py-4px text-12.5px text-ink-secondary"
        >
          <OnIcon :name="MODULE_ICONS[module]" :size="13" class="text-ink-tertiary" />
          <span :id="`monitor-rail-${module}`" class="flex-1">{{ t(`monitor.modules.${module}`) }}</span>
          <OnToggle
            :model-value="store.item(module).enabled"
            :aria-labelledby="`monitor-rail-${module}`"
            @update:model-value="value => toggle(module, value)"
          />
        </div>
        <p v-if="crowded" class="m-0 mt-6px flex items-start gap-6px text-11.5px leading-[1.5] text-status-warning">
          <OnIcon name="triangle-alert" :size="13" class="mt-1px shrink-0" />{{ t('monitor.rail.crowded') }}
        </p>
        <p class="m-0 mt-8px text-12px text-ink-tertiary">
          {{ t('monitor.rail.refresh', { count: seconds }, { plural: seconds }) }}
        </p>
      </OnCard>
    </OnRailSection>

    <OnCard v-if="store.available.includes('fans')" padding="md" class="flex flex-col gap-10px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="fan" :size="15" />{{ t('monitor.modules.fans') }}
      </h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">{{ fanText }}</p>
      <OnButton variant="secondary" @click="router.replace({ path: '/monitor', query: { detail: 'sensors' } })">{{
        t('monitor.rail.adjustFans')
      }}</OnButton>
    </OnCard>

    <OnCard padding="md" class="flex flex-col gap-8px">
      <h2 class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
        <OnIcon name="lock" :size="15" />{{ t('monitor.rail.privacy') }}
      </h2>
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">
        {{ t('monitor.rail.privacyBody') }}
      </p>
    </OnCard>
  </OnRailPanel>
</template>
