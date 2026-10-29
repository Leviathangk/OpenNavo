<script setup lang="ts">
import { onBeforeUnmount } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatVersionChange } from '@opennavo/shared';
import { OnButton, OnModal } from '@opennavo/ui';
import { useUnsignedUpdateConfirmStore } from '@/stores/unsignedUpdateConfirm';

const { t } = useI18n();
const store = useUnsignedUpdateConfirmStore();
onBeforeUnmount(() => store.choose(false));
</script>

<template>
  <OnModal
    :open="store.pending !== null"
    :title="t('updates.unsigned.title')"
    :description="t('updates.unsigned.body')"
    size="md"
    role="alertdialog"
    initial-focus="[data-unsigned-cancel]"
    @close="store.choose(false)"
  >
    <ul class="m-0 flex list-none flex-col gap-12px p-0">
      <li v-for="item in store.pending" :key="item.appId ?? item.token" class="min-w-0 text-13px">
        <span class="break-words font-600 text-ink-primary">{{ item.name }}</span>
        <span class="ml-8px text-ink-secondary tabular-nums">{{
          formatVersionChange(item.installedVersion, item.currentVersion)
        }}</span>
        <p v-if="item.downloadHost" class="mb-0 mt-4px break-all text-ink-secondary">
          {{ t('updates.meta.from', { host: item.downloadHost }) }}
        </p>
      </li>
    </ul>
    <template #footer>
      <OnButton variant="ghost" data-unsigned-cancel @click="store.choose(false)">{{ t('common.cancel') }}</OnButton>
      <OnButton variant="primary" data-unsigned-confirm @click="store.choose(true)">{{
        t('updates.unsigned.confirm')
      }}</OnButton>
    </template>
  </OnModal>
</template>
