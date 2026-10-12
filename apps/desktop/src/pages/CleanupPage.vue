<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnErrorState, OnPageHeader } from '@opennavo/ui';
import CleanupOverview from '@/components/cleanup/CleanupOverview.vue';
import CleanupResult from '@/components/cleanup/CleanupResult.vue';
import CleanupReview from '@/components/cleanup/CleanupReview.vue';
import CleanupRunning from '@/components/cleanup/CleanupRunning.vue';
import CleanupScanning from '@/components/cleanup/CleanupScanning.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useCleanupStore } from '@/stores/cleanup';

// Clean Up Mac (cleanup proposal §03–§06): overview → scan → review → run → result, all on one page.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const { errorText } = usePackageState();
const store = useCleanupStore();

const subtitle = computed(() => {
  switch (store.stage) {
    case 'idle':
      return t('cleanup.subtitle.idle');
    case 'scanning':
    case 'finishing':
      return t('cleanup.subtitle.scanning');
    case 'review':
    case 'submitting':
      return t('cleanup.subtitle.review', {
        size: formatBytes(store.totals.reclaimable, { locale: appLocale.value }),
        time: formatRelativeTime(store.report?.scannedAt ?? Date.now(), {
          locale: appLocale.value
        })
      });
    case 'running':
      return t('cleanup.subtitle.running');
    case 'done':
      return t('cleanup.subtitle.done');
    default:
      return t('cleanup.subtitle.failed');
  }
});
</script>

<template>
  <div class="relative">
    <OnPageHeader :title="t('cleanup.title')" :subtitle="subtitle">
      <template v-if="store.stage === 'review'" #actions>
        <OnButton variant="secondary" icon="refresh-cw" @click="store.scan()">{{ t('cleanup.rescan') }}</OnButton>
      </template>
    </OnPageHeader>

    <CleanupOverview v-if="store.stage === 'idle'" @scan="store.scan()" />
    <CleanupScanning v-else-if="store.stage === 'scanning' || store.stage === 'finishing'" />
    <CleanupReview v-else-if="store.stage === 'review' || store.stage === 'submitting'" />
    <CleanupRunning v-else-if="store.stage === 'running'" />
    <CleanupResult v-else-if="store.stage === 'done'" />
    <OnErrorState
      v-else
      :title="t('cleanup.failed.title')"
      :description="store.error ? errorText(store.error) : undefined"
      @retry="store.scan()"
    />
  </div>
</template>
