<script setup lang="ts">
import { onBeforeUnmount } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatVersionChange } from '@opennavo/shared';
import { OnButton, OnModal } from '@opennavo/ui';
import { useUpdateReviewConfirmStore } from '@/stores/updateReviewConfirm';
const { t } = useI18n();
const store = useUpdateReviewConfirmStore();
onBeforeUnmount(() => store.choose(false));
</script>

<template>
  <OnModal
    :open="store.pending !== null"
    :title="t('updates.review.title')"
    :description="t('updates.review.body')"
    role="alertdialog"
    initial-focus="[data-review-cancel]"
    @close="store.choose(false)"
  >
    <template v-if="store.pending">
      <p class="m-0 font-600 text-ink-primary">
        {{ store.pending.item.name }}
        <span class="ml-8px font-400 text-ink-secondary">{{
          formatVersionChange(store.pending.item.installedVersion, store.pending.item.currentVersion)
        }}</span>
      </p>
      <p v-if="store.pending.item.downloadHost" class="mb-0 mt-4px break-all">
        {{ t('updates.meta.from', { host: store.pending.item.downloadHost }) }}
      </p>
      <ul class="mb-0 mt-12px pl-20px">
        <li v-for="warning in store.pending.consent.warnings" :key="warning" class="mt-8px">
          {{ t(`updates.review.${warning}`) }}
        </li>
      </ul>
    </template>
    <template #footer>
      <OnButton variant="ghost" data-review-cancel @click="store.choose(false)">{{ t('common.cancel') }}</OnButton>
      <OnButton variant="primary" data-review-confirm @click="store.choose(true)">{{
        t('updates.unsigned.confirm')
      }}</OnButton>
    </template>
  </OnModal>
</template>
