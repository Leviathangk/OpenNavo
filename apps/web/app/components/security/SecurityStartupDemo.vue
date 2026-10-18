<script setup lang="ts">
import { OnAppIcon, OnIcon } from '@opennavo/ui';

// Startup items demo (desktop Security › Startup Items): items grouped by app with signer trust and switches. A new,
// unsigned background service shows up flagged New, then gets switched off. The flagged frame is representative.
const props = defineProps<{ apps: readonly LandingApp[] }>();

const DURATIONS = [3000, 2600] as const;
const SUSPICIOUS = 'com.helper.updater';

const { t } = useI18n();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);
const off = computed(() => step.value === 1);

const rows = computed(() =>
  props.apps.slice(0, 3).map((app, index) => ({
    app,
    kind: index === 1 ? t('security.startup.demo.service') : t('security.startup.demo.loginItem')
  }))
);
</script>

<template>
  <div
    ref="root"
    class="mx-auto flex max-w-440px flex-col overflow-hidden rounded-big border border-solid border-line-default bg-surface-base shadow-popover"
    inert
    aria-hidden="true"
  >
    <div class="flex items-center justify-between gap-12px border-b border-b-solid border-line-subtle px-16px py-12px">
      <span class="text-13.5px font-600 text-ink-primary">{{
        t('security.startup.demo.summary', { count: rows.length + 1 })
      }}</span>
      <OnIcon name="refresh-cw" :size="14" class="text-ink-tertiary" />
    </div>
    <div
      class="flex items-center gap-12px border-b border-b-solid border-line-subtle px-16px py-12px transition-colors duration-base ease-standard"
      :class="off ? '' : 'bg-status-danger-subtle'"
    >
      <span class="grid h-32px w-32px shrink-0 place-items-center rounded-default bg-surface-raised text-ink-secondary">
        <OnIcon name="square-terminal" :size="17" />
      </span>
      <span class="flex min-w-0 flex-1 flex-col gap-3px">
        <span class="flex min-w-0 items-center gap-6px">
          <span class="truncate font-mono text-12.5px font-600 text-ink-primary">{{ SUSPICIOUS }}</span>
          <span
            v-if="!off"
            class="inline-flex h-18px shrink-0 items-center rounded-full bg-accent-salmon-subtle px-6px text-10.5px font-600 text-brand-salmon"
            >{{ t('security.startup.demo.new') }}</span
          >
        </span>
        <span class="flex min-w-0 items-center gap-6px text-11.5px">
          <span class="shrink-0 text-ink-tertiary">{{ t('security.startup.demo.service') }}</span>
          <span class="text-ink-tertiary">·</span>
          <span class="flex min-w-0 items-center gap-4px truncate text-status-danger">
            <OnIcon name="triangle-alert" :size="12" class="shrink-0" />{{ t('security.startup.demo.untrusted') }}
          </span>
        </span>
      </span>
      <span class="shrink-0 text-11.5px text-ink-tertiary">{{ off ? t('security.startup.demo.disabled') : '' }}</span>
      <span
        class="relative inline-flex h-18px w-30px shrink-0 items-center rounded-full transition-colors duration-fast ease-standard"
        :class="off ? 'bg-component-toggle-track' : 'bg-brand-coral'"
      >
        <span
          class="absolute left-2px top-2px h-14px w-14px rounded-full transition-transform duration-fast ease-standard"
          :class="off ? 'bg-component-toggle-knob' : 'translate-x-12px bg-component-toggle-knob-on'"
        ></span>
      </span>
    </div>
    <div
      v-for="row in rows"
      :key="row.app.token"
      class="flex items-center gap-12px border-b border-b-solid border-line-subtle px-16px py-12px last:border-b-0"
    >
      <OnAppIcon
        kind="cask"
        :token="row.app.token"
        :name="row.app.name"
        :src="row.app.iconUrl"
        :accent="row.app.accentColor"
        :size="32"
      />
      <span class="flex min-w-0 flex-1 flex-col gap-3px">
        <span class="truncate text-13px font-600 text-ink-primary">{{ row.app.name }}</span>
        <span class="flex min-w-0 items-center gap-6px text-11.5px text-ink-tertiary">
          <span class="shrink-0">{{ row.kind }}</span>
          <span>·</span>
          <span class="flex min-w-0 items-center gap-4px truncate text-status-success">
            <OnIcon name="shield-check" :size="12" class="shrink-0" />{{ t('security.startup.demo.known') }}
          </span>
        </span>
      </span>
      <span class="relative inline-flex h-18px w-30px shrink-0 items-center rounded-full bg-brand-coral">
        <span
          class="absolute left-2px top-2px h-14px w-14px translate-x-12px rounded-full bg-component-toggle-knob-on"
        ></span>
      </span>
    </div>
  </div>
</template>
