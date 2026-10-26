<script setup lang="ts">
import { formatBytes } from '@opennavo/shared';
import { OnAppIcon, OnIcon } from '@opennavo/ui';

// Uninstall demo (desktop uninstall sheet): the app and its related files, grouped and all selected, then the
// uninstall runs and completes. Sizes are illustrative; the app is a real catalog app.
const props = defineProps<{ app: LandingApp }>();

// Steps: 0 review (representative frame), 1–2 uninstalling, 3 done.
const DURATIONS = [2800, 900, 900, 2600] as const;
const GROUPS = [
  { key: 'app', bytes: 1.21e9 },
  { key: 'app_data', bytes: 846e6 },
  { key: 'caches', bytes: 312e6 },
  { key: 'preferences', bytes: 48e3 },
  { key: 'background', bytes: 12e3 }
] as const;
const ITEMS = 14;

const { t, locale } = useI18n();
const formattingLocale = computed(() => toAppLocale(locale.value));
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);

const size = (bytes: number) => formatBytes(bytes, { locale: formattingLocale.value });
const total = computed(() => size(GROUPS.reduce((sum, group) => sum + group.bytes, 0)));
const title = computed(() => {
  if (step.value === 0) return t('landing.toolkit.demo.uninstallTitle', { name: props.app.name });
  if (step.value < 3) return t('landing.toolkit.demo.running', { name: props.app.name });
  return t('landing.toolkit.demo.done');
});
const progress = computed(() => (step.value === 1 ? 38 : 84));
</script>

<template>
  <div
    ref="root"
    class="mx-auto flex max-w-330px flex-col overflow-hidden rounded-panel border border-solid border-line-default bg-surface-card shadow-popover"
    inert
    aria-hidden="true"
  >
    <div class="flex items-center gap-10px px-14px pb-10px pt-12px">
      <OnAppIcon
        kind="cask"
        :token="app.token"
        :name="app.name"
        :src="app.iconUrl"
        :variants="app.iconVariants"
        :accent="app.accentColor"
        :size="28"
      />
      <span class="min-w-0 flex-1 truncate text-13.5px font-600 text-ink-primary">{{ title }}</span>
      <span
        v-if="step === 3"
        class="grid h-20px w-20px shrink-0 place-items-center rounded-full bg-status-success-subtle text-status-success"
      >
        <OnIcon name="check" :size="12" />
      </span>
    </div>
    <ul class="m-0 flex list-none flex-col border-t border-t-solid border-line-subtle px-14px py-6px">
      <li
        v-for="group in GROUPS"
        :key="group.key"
        class="flex h-26px items-center gap-9px text-12.5px transition-opacity duration-base ease-standard"
        :class="step === 3 ? 'opacity-45' : ''"
      >
        <span class="grid h-14px w-14px shrink-0 place-items-center rounded-tiny bg-brand-coral text-ink-inverse">
          <OnIcon name="check" :size="10" />
        </span>
        <span class="min-w-0 flex-1 truncate text-ink-secondary">{{
          t(`landing.toolkit.demo.groups.${group.key}`)
        }}</span>
        <span class="shrink-0 text-ink-tertiary tabular-nums">{{ size(group.bytes) }}</span>
      </li>
    </ul>
    <div class="flex h-46px items-center gap-10px border-t border-t-solid border-line-subtle px-14px">
      <template v-if="step === 0">
        <span class="min-w-0 flex-1 truncate text-11.5px text-ink-tertiary tabular-nums">{{
          t('landing.toolkit.demo.selection', { selected: ITEMS, total: ITEMS, size: total })
        }}</span>
        <span
          class="inline-flex h-26px shrink-0 items-center rounded-default bg-status-danger-subtle px-11px text-12px font-600 text-status-danger"
          >{{ t('landing.toolkit.demo.uninstall') }}</span
        >
      </template>
      <div v-else-if="step < 3" class="h-4px flex-1 overflow-hidden rounded-full bg-accent-coral-subtle">
        <div class="landing-uninstall-fill h-full rounded-full bg-brand-coral" :style="{ width: `${progress}%` }"></div>
      </div>
      <span v-else class="flex-1 truncate text-12px text-status-success tabular-nums">{{
        t('landing.toolkit.demo.freed', { size: total })
      }}</span>
    </div>
  </div>
</template>

<style>
.landing-uninstall-fill {
  transition: width var(--on-motion-duration-slow) var(--on-motion-easing-standard);
}
</style>
