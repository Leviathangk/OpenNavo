<script setup lang="ts">
import { onBeforeUnmount } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCodeText, OnModal } from '@opennavo/ui';
import { useInstallConfirmStore } from '@/stores/installConfirm';

const { t } = useI18n();
const store = useInstallConfirmStore();
onBeforeUnmount(() => store.choose(false));
</script>

<template>
  <OnModal
    :open="store.pending !== null"
    :title="
      t(store.pending?.status === 'blocked' ? 'installConfirm.blockedTitle' : 'installConfirm.title', {
        name: store.pending?.name ?? ''
      })
    "
    :description="
      t(store.pending?.status === 'blocked' ? 'installConfirm.blockedDescription' : 'installConfirm.description')
    "
    size="md"
    role="alertdialog"
    initial-focus="[data-install-cancel]"
    @close="store.choose(false)"
  >
    <ul v-if="store.pending?.paths.length" class="m-0 flex list-none flex-col gap-8px p-0">
      <li v-for="path in store.pending.paths" :key="path" class="text-13px text-ink-secondary">
        <OnCodeText :text="path" />
      </li>
    </ul>
    <p v-if="store.pending?.status === 'conflict'" class="mb-0 mt-16px text-13px leading-relaxed text-ink-secondary">
      {{ t('installConfirm.matchHint') }}
    </p>
    <template #footer>
      <OnButton variant="ghost" data-install-cancel @click="store.choose(false)">{{ t('common.cancel') }}</OnButton>
      <OnButton v-if="store.pending?.status === 'conflict'" variant="primary" @click="store.choose(true)">{{
        t('installConfirm.confirm')
      }}</OnButton>
    </template>
  </OnModal>
</template>
