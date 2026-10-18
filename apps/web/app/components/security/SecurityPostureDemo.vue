<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// System check-up demo (desktop Security › Overview): the eleven read-only checks, one of them needing attention
// with a shortcut to the matching System Settings pane. Static: the list is the point.
const ITEMS = [
  'sip',
  'ssv',
  'gatekeeper',
  'filevault',
  'firewall',
  'xprotect',
  'updates',
  'screenlock',
  'autologin',
  'guest',
  'sharing'
] as const;
const WARN = 'firewall';

const { t } = useI18n();
</script>

<template>
  <div
    class="mx-auto flex max-w-440px flex-col overflow-hidden rounded-big border border-solid border-line-default bg-surface-base shadow-popover"
    inert
    aria-hidden="true"
  >
    <div
      v-for="item in ITEMS"
      :key="item"
      class="flex items-center gap-10px border-b border-b-solid border-line-subtle px-16px py-8px last:border-b-0"
    >
      <span
        class="grid h-20px w-20px shrink-0 place-items-center rounded-full"
        :class="
          item === WARN
            ? 'bg-status-warning-subtle text-status-warning'
            : 'bg-status-success-subtle text-status-success'
        "
      >
        <OnIcon :name="item === WARN ? 'triangle-alert' : 'check'" :size="12" />
      </span>
      <span class="flex min-w-0 flex-1 flex-col">
        <span class="truncate text-12.5px font-500 text-ink-primary">{{ t(`security.posture.items.${item}`) }}</span>
        <span v-if="item === WARN" class="truncate text-11.5px text-status-warning">{{
          t('security.posture.firewallOff')
        }}</span>
      </span>
      <span
        v-if="item === WARN"
        class="inline-flex h-24px shrink-0 items-center rounded-default bg-button-secondary-bg px-10px text-11.5px font-600 text-button-secondary-text"
        >{{ t('security.posture.openSettings') }}</span
      >
      <span v-else class="shrink-0 text-11.5px text-ink-tertiary">{{ t('security.posture.ok') }}</span>
    </div>
  </div>
</template>
