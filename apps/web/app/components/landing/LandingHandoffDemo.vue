<script setup lang="ts">
import { installCommand } from '@opennavo/shared';
import { OnAppIcon, OnIcon, useUiMessages } from '@opennavo/ui';

// Web-to-desktop handoff demo (05 §7.1, 06 §10): click Open in OpenNavo; desktop shows full-command install consent.
// Step zero shows confirmation. Web card height 112, dialog starts at 104, covering only bottom padding and leaving the button visible.
const props = defineProps<{ app: LandingApp }>();

const DURATIONS = [3000, 1300] as const;

const { t } = useI18n();
const messages = useUiMessages();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);
const dialog = computed(() => step.value === 0);
// Address bar displays this site's domain (NUXT_PUBLIC_SITE_URL in production).
const siteHost = (() => {
  try {
    return new URL(useRuntimeConfig().public.siteUrl).host;
  } catch {
    return '';
  }
})();
const address = computed(() => `${siteHost}/apps/${props.app.token}`);
</script>

<template>
  <div ref="root" class="relative mx-auto h-224px max-w-320px" inert aria-hidden="true">
    <div
      class="absolute left-0 top-0 box-border h-112px w-80% rounded-default border border-solid border-line-subtle bg-surface-base p-10px"
    >
      <div class="flex items-center gap-6px">
        <span class="h-6px w-6px rounded-full bg-surface-chip"></span>
        <span class="h-6px w-6px rounded-full bg-surface-chip"></span>
        <span
          class="ml-4px min-w-0 flex-1 truncate rounded-tiny bg-surface-inset px-6px py-2px font-mono text-10px text-ink-tertiary"
          >{{ address }}</span
        >
      </div>
      <div class="mt-10px flex items-center gap-8px">
        <OnAppIcon
          kind="cask"
          :token="app.token"
          :name="app.name"
          :src="app.iconUrl"
          :variants="app.iconVariants"
          :accent="app.accentColor"
          :size="28"
        />
        <span class="min-w-0 flex-1 truncate text-12.5px font-600 text-ink-primary">{{ app.name }}</span>
      </div>
      <span
        class="landing-handoff-button mt-10px inline-flex h-26px max-w-full items-center gap-6px whitespace-nowrap rounded-full bg-button-primary-bg px-10px text-11.5px font-600 text-button-primary-text"
        :class="dialog ? '' : 'is-pressing'"
      >
        <OnIcon name="arrow-up-right" :size="12" />
        {{ messages.action.openInApp }}
      </span>
    </div>
    <div
      class="landing-handoff-dialog absolute right-0 top-104px box-border w-80% rounded-panel border border-solid border-line-default bg-component-modal-bg p-10px shadow-modal"
      :class="dialog ? 'is-open' : ''"
    >
      <span class="block truncate text-12.5px font-600 text-ink-primary">{{
        t('landing.more.handoff.dialogTitle', { name: app.name })
      }}</span>
      <span class="mt-4px block truncate text-10.5px leading-[1.5] text-ink-tertiary">{{
        t('landing.more.handoff.dialogBody')
      }}</span>
      <code
        class="mt-6px block truncate rounded-tiny bg-surface-inset px-6px py-4px font-mono text-10px text-component-mono-text"
        >{{ installCommand('cask', app.token) }}</code
      >
      <span class="mt-8px flex justify-end gap-6px text-11px font-600">
        <span class="rounded-full bg-button-secondary-bg px-10px py-4px text-button-secondary-text">{{
          messages.dialog.cancel
        }}</span>
        <span class="rounded-full bg-button-primary-bg px-10px py-4px text-button-primary-text">{{
          t('landing.more.handoff.install')
        }}</span>
      </span>
    </div>
  </div>
</template>

<style>
.landing-handoff-button.is-pressing {
  animation: landing-press 1.3s var(--on-motion-easing-standard) infinite;
}

.landing-handoff-dialog {
  opacity: 0;
  transform: translateY(10px) scale(0.96);
  transition:
    opacity var(--on-motion-duration-base) var(--on-motion-easing-exit),
    transform var(--on-motion-duration-base) var(--on-motion-easing-exit);
}

.landing-handoff-dialog.is-open {
  opacity: 1;
  transform: none;
  transition-timing-function: var(--on-motion-easing-emphasized);
  transition-duration: 0.36s;
}

@keyframes landing-press {
  0%,
  40%,
  100% {
    transform: none;
    box-shadow: 0 0 0 0 transparent;
  }
  55% {
    transform: scale(0.96);
    box-shadow: 0 0 0 6px var(--on-accent-coral-subtle);
  }
}
</style>
