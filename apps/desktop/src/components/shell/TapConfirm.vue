<script setup lang="ts">
import { onBeforeUnmount } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCodeText, OnModal } from '@opennavo/ui';
import { useTapConfirmStore } from '@/stores/tapConfirm';

const { t } = useI18n();
const store = useTapConfirmStore();
onBeforeUnmount(() => store.choose(false));
</script>

<template>
  <OnModal
    :open="store.pending !== null"
    :title="t('tapConfirm.title', { name: store.pending?.name ?? '' })"
    :description="t('tapConfirm.description', { name: store.pending?.name ?? '' })"
    size="md"
    role="alertdialog"
    initial-focus="[data-tap-cancel]"
    @close="store.choose(false)"
  >
    <p v-if="store.pending" class="m-0 text-13px text-ink-secondary">
      <OnCodeText :text="store.pending.tap" />
    </p>
    <p class="mb-0 mt-16px text-13px leading-relaxed text-ink-secondary">{{ t('tapConfirm.hint') }}</p>
    <template #footer>
      <OnButton variant="ghost" data-tap-cancel @click="store.choose(false)">{{ t('common.cancel') }}</OnButton>
      <OnButton variant="primary" data-tap-confirm @click="store.choose(true)">{{ t('tapConfirm.confirm') }}</OnButton>
    </template>
  </OnModal>
</template>
