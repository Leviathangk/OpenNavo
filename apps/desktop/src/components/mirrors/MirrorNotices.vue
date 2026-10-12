<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { formatMilliseconds } from '@opennavo/shared';
import { OnButton, OnIcon } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import type { MirrorSuggestion } from '@/composables/useMirrorOptions';

// Source-list notices (first-launch-onboarding §3.3): unavailable mirrors, slow official source (suggest switching, never switch automatically),
// or failed probe for the selected source. Render nothing when no notice applies.
defineProps<{
  configFailed: boolean;
  suggestion?: MirrorSuggestion;
  selectedUnreachable: boolean;
}>();

const emit = defineEmits<{ retry: []; useSuggested: [] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const ms = (value: number) => formatMilliseconds(value, { locale: appLocale.value });
</script>

<template>
  <div v-if="configFailed || suggestion || selectedUnreachable" class="flex flex-col gap-8px text-12.5px">
    <p v-if="configFailed" class="m-0 flex items-center gap-8px text-ink-secondary">
      <OnIcon name="wifi-off" :size="14" class="shrink-0 text-status-warning" />
      <span class="min-w-0 flex-1">{{ t('mirrors.configFailed') }}</span>
      <OnButton variant="ghost" size="xs" @click="emit('retry')">{{ t('common.retry') }}</OnButton>
    </p>
    <p v-if="suggestion" class="m-0 flex items-center gap-8px text-ink-secondary" role="status">
      <OnIcon name="triangle-alert" :size="14" class="shrink-0 text-status-warning" />
      <span class="min-w-0 flex-1">{{
        suggestion.officialMs === null
          ? t('mirrors.failedOfficial', {
              name: suggestion.option.name,
              latency: ms(suggestion.latencyMs)
            })
          : t('mirrors.slowOfficial', {
              official: ms(suggestion.officialMs),
              name: suggestion.option.name,
              latency: ms(suggestion.latencyMs)
            })
      }}</span>
      <OnButton variant="secondary" size="xs" @click="emit('useSuggested')">{{
        t('mirrors.useSuggested', { name: suggestion.option.name })
      }}</OnButton>
    </p>
    <p v-else-if="selectedUnreachable" class="m-0 flex items-center gap-8px text-ink-secondary" role="status">
      <OnIcon name="triangle-alert" :size="14" class="shrink-0 text-status-warning" />
      <span class="min-w-0 flex-1">{{ t('mirrors.selectedFailed') }}</span>
    </p>
  </div>
</template>
