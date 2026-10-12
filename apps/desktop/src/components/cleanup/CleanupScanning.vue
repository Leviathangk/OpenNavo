<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnButton, OnIcon, OnProgressMeter } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCleanupStore } from '@/stores/cleanup';
import { displayPath } from '@/utils/paths';

// Read-only scan (cleanup proposal §03 ②): current category and location; results appear once the bar reaches 100%.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();
const panel = ref<HTMLElement | null>(null);

const percent = computed(() =>
  store.stage === 'finishing'
    ? 100
    : store.progress && store.progress.total > 0
      ? Math.round((store.progress.checked / store.progress.total) * 100)
      : null
);
const title = computed(() =>
  store.progress
    ? t('cleanup.scan.scanning', { name: t(`cleanup.categories.${store.progress.category}`) })
    : t('cleanup.scan.starting')
);
const found = computed(() =>
  store.progress?.foundBytes ? formatBytes(store.progress.foundBytes, { locale: appLocale.value }) : null
);
const location = computed(() => (store.progress?.location ? displayPath(store.progress.location) : ''));
const nextFrame = () => new Promise<void>(resolve => requestAnimationFrame(() => resolve()));

watch(
  () => store.stage,
  async stage => {
    if (stage !== 'finishing') return;
    const id = store.requestId;
    if (!id) return;
    await nextTick();
    // Let the browser paint the 100% target and run its width transition before showing results.
    await nextFrame();
    await nextFrame();
    const fill = panel.value?.querySelector<HTMLElement>('.on-meter-fill');
    await Promise.allSettled((fill?.getAnimations?.() ?? []).map(animation => animation.finished));
    await nextFrame();
    store.finishScan(id);
  },
  { flush: 'post', immediate: true }
);
</script>

<template>
  <section
    ref="panel"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-18px"
    data-testid="cleanup-scanning"
  >
    <div class="flex items-center gap-10px" aria-live="polite">
      <OnIcon name="loader-circle" :size="16" class="cleanup-spin shrink-0 text-brand-coral" />
      <h2 class="m-0 min-w-0 truncate text-15px font-600 text-ink-primary">{{ title }}</h2>
      <span class="min-w-0 flex-1 truncate text-13px text-ink-tertiary">{{
        found ? t('cleanup.scan.found', { size: found }) : t('cleanup.scan.readOnly')
      }}</span>
      <OnButton variant="ghost" size="sm" :disabled="store.stage === 'finishing'" @click="store.cancel()">{{
        t('cleanup.scan.cancel')
      }}</OnButton>
    </div>
    <OnProgressMeter class="mt-12px" :value="percent" :label="title" />
    <p class="m-0 mt-8px h-16px truncate font-mono text-11.5px text-ink-tertiary" :title="location">
      {{ location }}
    </p>
  </section>
</template>

<style scoped>
.cleanup-spin {
  animation: cleanup-spin 1s linear infinite;
}
@keyframes cleanup-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .cleanup-spin {
    animation: none;
  }
}
</style>
