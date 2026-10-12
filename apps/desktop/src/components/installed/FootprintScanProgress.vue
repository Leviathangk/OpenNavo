<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnIcon, OnProgressMeter } from '@opennavo/ui';
import type { FootprintProgress } from '@/ipc/bindings';

const props = defineProps<{ progress: FootprintProgress }>();
const { t } = useI18n();
const percent = computed(() =>
  props.progress.total ? Math.floor((props.progress.completed / props.progress.total) * 100) : 100
);
const title = computed(() =>
  t(
    props.progress.running
      ? 'library.scanProgress.running'
      : props.progress.failed
        ? 'library.scanProgress.partial'
        : 'library.scanProgress.done'
  )
);
</script>

<template>
  <div
    class="mb-14px rounded-16px border border-solid border-line-default bg-surface-card px-18px py-14px"
    data-testid="footprint-scan-progress"
  >
    <div class="mb-12px flex items-center justify-between gap-12px">
      <div class="flex items-center gap-8px text-13px font-semibold text-ink-primary" aria-live="polite">
        <OnIcon
          :name="progress.running ? 'loader-circle' : progress.failed ? 'circle-alert' : 'check'"
          :size="16"
          :class="
            progress.running
              ? 'footprint-scan-spinner text-brand-coral'
              : progress.failed
                ? 'text-status-warning'
                : 'text-status-success'
          "
        />
        {{ title }}
      </div>
      <div class="flex shrink-0 items-center gap-16px text-12px text-ink-secondary tabular-nums">
        <span>{{
          t('library.scanProgress.completed', {
            completed: progress.completed,
            total: progress.total
          })
        }}</span>
        <span class="text-ink-primary">{{ percent }}%</span>
      </div>
    </div>
    <OnProgressMeter :value="percent" :label="title" />
    <div class="mt-10px flex items-center justify-between gap-12px text-12px text-ink-tertiary">
      <span>{{ t('library.scanProgress.scope') }}</span>
      <span v-if="progress.retrying" class="text-status-warning">{{
        t('library.scanProgress.retrying', { count: progress.retrying })
      }}</span>
      <span v-else-if="progress.failed" class="text-status-warning">{{
        t('library.scanProgress.failed', { count: progress.failed })
      }}</span>
    </div>
  </div>
</template>

<style scoped>
.footprint-scan-spinner {
  animation: footprint-scan-spin 1s linear infinite;
}
@keyframes footprint-scan-spin {
  to {
    transform: rotate(360deg);
  }
}
:deep(.on-meter-fill) {
  transition: width var(--on-motion-duration-slow) var(--on-motion-easing-standard);
}
@media (prefers-reduced-motion: reduce) {
  .footprint-scan-spinner {
    animation: none;
  }
  :deep(.on-meter-fill) {
    transition: none;
  }
}
</style>
