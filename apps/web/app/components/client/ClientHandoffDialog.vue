<script setup lang="ts">
import { OnButton, OnIcon, OnModal } from '@opennavo/ui';

// One missing-client dialog for installs, collections, scans, uninstall previews and client pages.
// Download is always the primary action; the brew command stays available for installs.
const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { prompt, opening, retry, close, copyInstead, dontAsk } = useClientHandoff();

const dialogOpen = computed({
  get: () => prompt.value !== null,
  set: value => {
    if (!value) close();
  }
});
</script>

<template>
  <OnModal
    v-model:open="dialogOpen"
    size="sm"
    :title="t('client.handoff.title')"
    :description="t('client.handoff.body')"
  >
    <div
      v-if="prompt?.name"
      class="flex items-start gap-9px rounded-default border border-solid border-line-subtle bg-surface-raised px-12px py-10px"
    >
      <OnIcon name="app-window" :size="16" class="mt-1px shrink-0 text-ink-tertiary" />
      <span class="min-w-0 break-words text-13px font-500 text-ink-primary">{{ prompt.name }}</span>
    </div>
    <button
      v-if="prompt?.copy"
      type="button"
      class="mt-10px rounded-tiny border-none bg-transparent p-0 font-sans text-12px text-ink-tertiary underline-offset-2 outline-none hover:text-ink-secondary hover:underline focus-visible:shadow-focus-ring"
      @click="dontAsk"
    >
      {{ t('openInApp.dontAsk') }}
    </button>
    <template #footer>
      <OnButton variant="ghost" :disabled="opening" @click="close">{{ t('client.handoff.notNow') }}</OnButton>
      <OnButton v-if="prompt?.copy" variant="secondary" :disabled="opening" @click="copyInstead">
        {{ t('client.handoff.copy') }}
      </OnButton>
      <OnButton variant="secondary" :loading="opening" @click="retry">{{ t('client.open') }}</OnButton>
      <OnButton variant="primary" :href="localePath('/download')" :link-as="NuxtLink" @click="close">
        {{ t('download.button') }}
      </OnButton>
    </template>
  </OnModal>
</template>
