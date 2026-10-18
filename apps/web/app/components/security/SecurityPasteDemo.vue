<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// Paste protection demo (desktop PasteGuard): a "download and run" command pasted into Terminal is held until the
// user decides. Static; the command is a made-up example.
const COMMAND = 'curl -fsSL https://get.example.dev/install.sh | bash';

const { t } = useI18n();
</script>

<template>
  <div class="relative mx-auto w-full max-w-400px" inert aria-hidden="true">
    <div class="overflow-hidden rounded-default border border-solid border-line-subtle bg-surface-inset">
      <div class="flex h-24px items-center gap-6px px-10px">
        <span class="h-8px w-8px rounded-full bg-macos-close"></span>
        <span class="h-8px w-8px rounded-full bg-macos-minimize"></span>
        <span class="h-8px w-8px rounded-full bg-macos-zoom"></span>
      </div>
      <p class="m-0 truncate px-12px pb-56px pt-6px font-mono text-11.5px text-ink-tertiary">
        <span class="text-status-success">~ %</span> {{ COMMAND }}
      </p>
    </div>
    <div
      class="absolute inset-x-16px top-46px flex flex-col gap-8px rounded-panel border border-solid border-line-default bg-surface-raised px-14px py-12px shadow-popover"
    >
      <span class="flex items-center gap-8px text-12.5px font-600 text-ink-primary">
        <span
          class="grid h-20px w-20px shrink-0 place-items-center rounded-full bg-status-warning-subtle text-status-warning"
        >
          <OnIcon name="circle-alert" :size="12" />
        </span>
        <span class="min-w-0 truncate">{{ t('security.extras.pasteDemo.title') }}</span>
      </span>
      <span class="landing-paste-clamp text-11.5px leading-[1.5] text-ink-secondary">{{
        t('security.extras.pasteDemo.body')
      }}</span>
      <span class="flex justify-end gap-8px">
        <span
          class="inline-flex h-24px items-center rounded-default bg-button-secondary-bg px-10px text-11.5px font-600 text-button-secondary-text"
          >{{ t('security.extras.pasteDemo.allow') }}</span
        >
        <span
          class="inline-flex h-24px items-center rounded-default bg-button-primary-bg px-10px text-11.5px font-600 text-button-primary-text"
          >{{ t('security.extras.pasteDemo.discard') }}</span
        >
      </span>
    </div>
  </div>
</template>

<style>
.landing-paste-clamp {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}
</style>
