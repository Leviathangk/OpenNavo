<script setup lang="ts">
import { interpolate, interpolatePlural } from '@opennavo/shared';
import { OnAppIcon, OnChip, OnGetButton, OnLogo, useUiLocale, useUiMessages } from '@opennavo/ui';

// Menu-bar update demo (08 §10.14, desktop 06 §4.2): Update All updates three apps sequentially,
// decreasing the count until all are current. Step zero shows three pending updates.
const props = defineProps<{ apps: readonly LandingApp[] }>();

const DURATIONS = [2600, 520, 520, 520, 520, 520, 520, 2800] as const;

const { t } = useI18n();
const messages = useUiMessages();
const uiLocale = useUiLocale();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);

const rows = computed(() =>
  props.apps.slice(0, 3).map((app, index) => {
    const start = 1 + index * 2;
    const current = step.value;
    const state = current < start ? 'update' : current < start + 2 ? 'running' : 'done';
    return { app, state, progress: current === start ? 35 : 82 } as const;
  })
);

const count = computed(() => rows.value.filter(row => row.state !== 'done').length);
const updateAll = computed(() =>
  interpolatePlural(messages.value.action.updateAll, { count: count.value }, count.value, uiLocale.value)
);
</script>

<template>
  <div ref="root" class="mx-auto flex max-w-380px flex-col" inert aria-hidden="true">
    <div
      class="flex h-30px items-center justify-end gap-14px rounded-default border border-solid border-line-subtle bg-surface-inset px-12px text-12px text-ink-secondary"
    >
      <span class="flex items-center gap-6px rounded-tiny bg-surface-raised px-6px py-2px text-ink-primary">
        <OnLogo :size="14" />
        <span v-if="count" class="font-600 tabular-nums">{{ count }}</span>
      </span>
      <span class="tabular-nums">9:41</span>
    </div>
    <div
      class="mt-8px overflow-hidden rounded-panel border border-solid border-line-default bg-surface-card shadow-popover"
    >
      <div class="flex items-center gap-8px px-14px py-12px">
        <OnLogo :size="18" inline />
        <span class="text-13.5px font-600 text-ink-primary">{{
          count ? t('landing.updates.available', { count }, { plural: count }) : t('landing.updates.upToDate')
        }}</span>
      </div>
      <div
        v-for="row in rows"
        :key="row.app.token"
        class="flex items-center gap-10px border-t border-t-solid border-line-subtle px-14px py-10px"
      >
        <OnAppIcon
          kind="cask"
          :token="row.app.token"
          :name="row.app.name"
          :src="row.app.iconUrl"
          :variants="row.app.iconVariants"
          :accent="row.app.accentColor"
          :size="30"
        />
        <span class="flex min-w-0 flex-1 flex-col">
          <span class="truncate text-13px font-600 text-ink-primary">{{ row.app.name }}</span>
          <span v-if="row.app.version" class="truncate text-11.5px text-ink-tertiary">{{
            interpolate(messages.action.updateTo, { version: row.app.version })
          }}</span>
        </span>
        <OnChip v-if="row.state === 'done'" tone="success" dot>{{ messages.version.latest }}</OnChip>
        <OnGetButton v-else :state="row.state" :progress="row.state === 'running' ? row.progress : undefined" />
      </div>
      <div class="flex flex-col gap-10px border-t border-t-solid border-line-subtle px-14px py-12px">
        <span
          class="flex h-30px items-center justify-center rounded-default text-12.5px font-600 transition-colors duration-base ease-standard"
          :class="
            count ? 'bg-button-primary-bg text-button-primary-text' : 'bg-button-disabled-bg text-button-disabled-text'
          "
          >{{ updateAll }}</span
        >
        <span class="flex justify-between text-12px text-ink-tertiary">
          <span>{{ t('landing.updates.open') }}</span>
          <span>{{ t('landing.window.settings') }}</span>
        </span>
      </div>
    </div>
  </div>
</template>
