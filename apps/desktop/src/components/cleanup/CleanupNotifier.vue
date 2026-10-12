<script setup lang="ts">
import { watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { formatBytes } from '@opennavo/shared';
import { useAppLocale } from '@/composables/useAppLocale';
import { useToasts } from '@/composables/useToasts';
import { useCleanupStore } from '@/stores/cleanup';

// Cleanup keeps running after leaving the page (cleanup proposal §06 step 5); say when it finishes elsewhere.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const route = useRoute();
const router = useRouter();
const toasts = useToasts();
const store = useCleanupStore();

watch(
  () => store.stage,
  (stage, previous) => {
    if (stage !== 'done' || previous !== 'running' || route.name === 'cleanup') return;
    const failed = store.task?.state !== 'succeeded';
    toasts.pushAction(
      {
        tone: failed ? 'danger' : 'success',
        title: failed ? t('cleanup.result.failedTitle') : t('cleanup.toasts.done'),
        description: failed
          ? undefined
          : t('cleanup.toasts.doneBody', {
              size: formatBytes(store.freedNow, { locale: appLocale.value })
            }),
        actionLabel: t('cleanup.toasts.view')
      },
      () => void router.push('/cleanup')
    );
  }
);
</script>

<template>
  <span hidden></span>
</template>
