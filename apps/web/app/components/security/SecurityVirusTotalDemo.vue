<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// VirusTotal demo (desktop VirusTotal lookup): the file's menu offers a lookup, and the answer comes back by hash.
// Static; the file name, hash and engine count are illustrative.
const FILE = 'Setup-2.4.dmg';
const SHA256 = '9f2c4e81b7d0…53a1c7e2f604';

const { t } = useI18n();
</script>

<template>
  <div
    class="mx-auto flex w-full max-w-400px flex-col gap-10px rounded-panel border border-solid border-line-default bg-surface-card p-14px shadow-popover"
    inert
    aria-hidden="true"
  >
    <div class="flex items-center gap-10px">
      <span class="grid h-30px w-30px shrink-0 place-items-center rounded-default bg-surface-raised text-ink-secondary">
        <OnIcon name="file-text" :size="16" />
      </span>
      <span class="flex min-w-0 flex-1 flex-col">
        <span class="truncate text-12.5px font-600 text-ink-primary">{{ FILE }}</span>
        <span class="truncate font-mono text-11px text-ink-tertiary">SHA-256 {{ SHA256 }}</span>
      </span>
      <span
        class="inline-flex h-24px shrink-0 items-center gap-5px rounded-default bg-button-secondary-bg px-9px text-11.5px font-600 text-button-secondary-text"
      >
        <OnIcon name="scan-search" :size="13" />{{ t('security.extras.vtDemo.lookup') }}
      </span>
    </div>
    <div class="flex flex-col gap-4px rounded-default bg-status-success-subtle px-12px py-10px">
      <span class="flex items-center gap-6px text-12.5px font-600 text-status-success">
        <OnIcon name="shield-check" :size="14" />{{ t('security.extras.vtDemo.clean') }}
      </span>
      <span class="text-11.5px text-ink-secondary">{{ t('security.extras.vtDemo.engines', { total: 72 }) }}</span>
    </div>
    <span class="flex items-center gap-6px text-11px text-ink-tertiary">
      <OnIcon name="lock" :size="12" />{{ t('security.extras.vtDemo.hashOnly') }}
    </span>
  </div>
</template>
