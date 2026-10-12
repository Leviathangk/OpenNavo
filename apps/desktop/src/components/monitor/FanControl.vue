<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCheckbox, OnIcon, OnSegmented } from '@opennavo/ui';
import type { FanMode } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useMonitorStore } from '@/stores/monitor';
import { useMonitorFormat, useMonitorNames } from './meta';
import { useHelperApproval } from './useHelperApproval';

// Fan control (monitor proposal §14): automatic by default. Manual speeds go through the helper, which is installed
// right here, in the detail sheet and in the menu bar popover alike.
// Rust hands fans back to the system on quit, crash, sleep, overheating and write failures.
const props = defineProps<{ compact?: boolean }>();

const { t } = useI18n();
const store = useMonitorStore();
const settings = useSettingsStore();
const toasts = useToasts();
const f = useMonitorFormat();
const names = useMonitorNames();
const { errorText } = usePackageState();
useHelperApproval();

const fans = computed(() => store.fans ?? []);
const helper = computed(() => store.helper);
const status = computed(() => helper.value?.status ?? null);
const mode = computed<FanMode>(() => (fans.value.some(fan => fan.mode === 'manual') ? 'manual' : 'auto'));
const modes = computed(() => [
  { value: 'auto' as const, label: t('monitor.fans.auto') },
  { value: 'manual' as const, label: t('monitor.fans.manual') }
]);
/** Unknown chips only show speeds: there is nothing to switch. */
const locked = computed(() => helper.value != null && !helper.value.manualAllowed && mode.value === 'auto');
/** The switch appears once the helper answers; fans that are manual can always be handed back. */
const switchable = computed(() => status.value === 'enabled' || mode.value === 'manual');
const busy = ref(false);
const sync = ref(true);
/** Slider positions while dragging; committed on release. */
const drafts = ref<Record<number, number>>({});
const rows = computed(() =>
  sync.value && fans.value.length
    ? [
        {
          id: -1,
          name: t('monitor.fans.all'),
          min: Math.max(...fans.value.map(fan => fan.minRpm)),
          max: Math.min(...fans.value.map(fan => fan.maxRpm))
        }
      ]
    : fans.value.map(fan => ({ id: fan.id, name: names.fan(fan, fans.value.length), min: fan.minRpm, max: fan.maxRpm }))
);
const target = (id: number) => {
  if (drafts.value[id] != null) return drafts.value[id]!;
  const fan = id < 0 ? fans.value[0] : fans.value.find(item => item.id === id);
  return fan?.targetRpm ?? fan?.rpm ?? 0;
};
const lastManual = computed(() => settings.value?.fanLastManual ?? null);
/** The menu bar popover has no toast region, so it reports results in place. */
const notice = ref<{ tone: 'success' | 'danger'; text: string } | null>(null);

onMounted(() => void store.loadHelper().catch(() => undefined));
watch(mode, () => {
  drafts.value = {};
});
// Allowed in System Settings (or reinstalled): manual control is one click away now.
watch(status, (next, previous) => {
  if (next === 'enabled' && previous != null && previous !== 'enabled')
    report('success', t('monitor.fans.toasts.ready'), t('monitor.fans.toasts.readyBody'));
});

function report(tone: 'success' | 'danger', title: string, description?: string) {
  if (props.compact) notice.value = { tone, text: tone === 'danger' && description ? description : title };
  else toasts.push({ tone, title, description });
}

async function attempt(operation: () => Promise<unknown>, done?: { title: string; description?: string }) {
  busy.value = true;
  notice.value = null;
  try {
    await operation();
    if (done) report('success', done.title, done.description);
  } catch (error) {
    report('danger', t('errors.actionFailed'), errorText(error));
  } finally {
    busy.value = false;
  }
}

function choose(next: FanMode) {
  if (next === mode.value) return;
  if (next === 'auto') {
    void attempt(() => store.setFanMode(null, 'auto'), { title: t('monitor.fans.toasts.auto') });
    return;
  }
  // Every fan switches to manual, starting from its current speed.
  void attempt(() => store.setFanMode(null, 'manual'), {
    title: t('monitor.fans.toasts.manual'),
    description: t('monitor.fans.toasts.manualBody')
  });
}

/** A helper that is installed but not answering is registered again from scratch. */
const install = () => attempt(() => (status.value === 'error' ? store.reinstallHelper() : store.installHelper()));
const recheck = () => attempt(() => store.loadHelper());

function draft(id: number, event: Event) {
  drafts.value = { ...drafts.value, [id]: Number((event.target as HTMLInputElement).value) };
}
function commit(id: number) {
  const rpm = drafts.value[id];
  if (rpm == null) return;
  void attempt(() => store.setFanSpeed(id < 0 ? null : id, rpm));
}
const fullSpeed = () =>
  attempt(() => store.setFanSpeed(null, Math.max(...fans.value.map(fan => fan.maxRpm))), {
    title: t('monitor.fans.toasts.full')
  });
const restoreLast = () => attempt(() => store.restoreLastFans(), { title: t('monitor.fans.toasts.manual') });
</script>

<template>
  <div
    class="flex flex-col gap-10px rounded-panel border border-solid border-line-subtle p-12px"
    :class="props.compact ? 'bg-[rgba(255,255,255,0.03)]' : 'bg-surface-card'"
    data-testid="fan-control"
  >
    <div class="flex items-center gap-8px">
      <b class="flex-1 text-13px font-600 text-ink-primary">{{ t('monitor.fans.control') }}</b>
      <OnSegmented
        v-if="switchable"
        :model-value="mode"
        :options="modes"
        size="sm"
        :aria-label="t('monitor.fans.control')"
        @update:model-value="choose"
      />
      <span v-else class="text-12px text-ink-tertiary">{{ t('monitor.fans.auto') }}</span>
    </div>

    <p v-if="locked" class="m-0 text-12px leading-[1.55] text-ink-tertiary">{{ t('monitor.fans.unsupported') }}</p>

    <template v-else-if="mode === 'manual'">
      <div v-for="row in rows" :key="row.id" class="flex flex-col gap-4px">
        <div class="flex justify-between text-12px text-ink-tertiary">
          <span>{{ row.name }}</span>
          <b class="font-600 tabular-nums text-ink-primary">{{ f.rpm(target(row.id)) }}</b>
        </div>
        <input
          type="range"
          class="w-full accent-[var(--on-brand-coral)]"
          :min="row.min"
          :max="row.max"
          step="100"
          :value="target(row.id)"
          :aria-label="row.name"
          :disabled="busy"
          @input="draft(row.id, $event)"
          @change="commit(row.id)"
        />
        <div class="flex justify-between text-10.5px text-ink-tertiary">
          <span>{{ t('monitor.fans.min', { rpm: f.rpm(row.min) }) }}</span>
          <span>{{ t('monitor.fans.max', { rpm: f.rpm(row.max) }) }}</span>
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-x-6px gap-y-8px">
        <label
          v-if="fans.length > 1"
          class="flex shrink-0 cursor-pointer items-center gap-8px whitespace-nowrap text-12.5px text-ink-secondary"
        >
          <OnCheckbox v-model="sync" :aria-label="t('monitor.fans.sync')" />{{ t('monitor.fans.sync') }}
        </label>
        <span class="ml-auto flex gap-6px">
          <OnButton variant="secondary" size="xs" :disabled="busy" @click="fullSpeed">{{
            t('monitor.fans.full')
          }}</OnButton>
          <OnButton variant="secondary" size="xs" :disabled="busy" @click="choose('auto')">{{
            t('monitor.fans.restore')
          }}</OnButton>
        </span>
      </div>
    </template>

    <!-- Until the helper answers, the next step is offered right here: install it, allow it, or reinstall it. -->
    <div
      v-else-if="status === 'requires_approval'"
      class="flex items-start gap-10px rounded-default border border-solid border-[rgba(102,164,250,0.25)] bg-status-info-subtle px-12px py-10px text-12.5px text-ink-secondary"
    >
      <OnIcon name="info" :size="15" class="mt-2px shrink-0 text-status-info" />
      <div class="min-w-0">
        <b class="text-ink-primary">{{ t('monitor.fans.approvalTitle') }}</b>
        <p class="m-0 mb-8px mt-4px leading-[1.55]">{{ t('monitor.fans.approvalBody') }}</p>
        <div class="flex flex-wrap gap-6px">
          <OnButton variant="primary" size="sm" @click="attempt(() => store.openApproval())">{{
            t('monitor.settings.openSettings')
          }}</OnButton>
          <OnButton variant="secondary" size="sm" :loading="busy" @click="recheck">{{
            t('monitor.fans.recheck')
          }}</OnButton>
        </div>
      </div>
    </div>
    <div
      v-else-if="status === 'not_installed' || status === 'error'"
      class="flex items-start gap-10px rounded-default border border-solid border-[rgba(102,164,250,0.25)] bg-status-info-subtle px-12px py-10px text-12.5px text-ink-secondary"
      data-testid="fan-helper-offer"
    >
      <OnIcon
        :name="status === 'error' ? 'circle-alert' : 'key-round'"
        :size="15"
        class="mt-2px shrink-0 text-status-info"
      />
      <div class="min-w-0">
        <b class="text-ink-primary">{{
          status === 'error' ? t('monitor.fans.brokenTitle') : t('monitor.fans.helperTitle')
        }}</b>
        <p class="m-0 mb-8px mt-4px leading-[1.55]">
          {{ status === 'error' ? t('monitor.fans.brokenBody') : t('monitor.fans.helperBody') }}
        </p>
        <OnButton variant="primary" size="sm" :loading="busy" @click="install">{{
          status === 'error' ? t('monitor.fans.reinstall') : t('monitor.fans.install')
        }}</OnButton>
      </div>
    </div>

    <template v-else-if="status === 'enabled'">
      <p class="m-0 text-12px leading-[1.55] text-ink-tertiary">{{ t('monitor.fans.autoNote') }}</p>
      <OnButton
        v-if="lastManual"
        variant="secondary"
        size="xs"
        class="self-start"
        :disabled="busy"
        @click="restoreLast"
        >{{ t('monitor.fans.restoreLast') }}</OnButton
      >
    </template>

    <p
      v-if="notice"
      role="status"
      class="m-0 text-12px leading-[1.55]"
      :class="notice.tone === 'danger' ? 'text-status-danger' : 'text-status-success'"
      data-testid="fan-notice"
    >
      {{ notice.text }}
    </p>

    <p v-if="!props.compact && !locked" class="m-0 flex items-start gap-8px text-12px leading-[1.55] text-ink-tertiary">
      <OnIcon name="shield-check" :size="14" class="mt-2px shrink-0 text-status-success" />{{
        t('monitor.fans.safety')
      }}
    </p>
  </div>
</template>
