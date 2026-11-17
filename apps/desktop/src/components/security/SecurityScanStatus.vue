<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnButton, OnIcon, OnProgressMeter } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useSecurityStore } from '@/stores/security';
import { shortPath } from './meta';

// A scan keeps running while the user is on another tab. This strip keeps it in view on every other Security tab, so
// leaving the page and coming back never looks like the scan stopped.
const emit = defineEmits<{ open: [] }>();
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useSecurityStore();

const scan = computed(() => store.scan);
const title = computed(() => {
  const value = scan.value;
  if (!value) return '';
  return t('security.scan.running', {
    kind: t(`security.scan.${value.kind}.title`),
    count: (store.progress?.filesScanned ?? value.filesScanned).toLocaleString(appLocale.value),
    size: formatBytes(store.progress?.bytesScanned ?? value.bytesScanned, { locale: appLocale.value })
  });
});
const fullPath = computed(() => store.progress?.currentPath ?? '');
const currentPath = computed(() => (fullPath.value ? shortPath(fullPath.value, 100) : ''));
const runningLabel = computed(() => {
  const value = scan.value;
  return value ? t('security.scan.runningLabel', { kind: t(`security.scan.${value.kind}.title`) }) : '';
});
</script>

<template>
  <section
    v-if="scan?.state === 'running'"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-20px py-18px"
    data-testid="security-scan-status"
  >
    <span class="sr-only" role="status">{{ runningLabel }}</span>
    <div class="flex flex-wrap items-center gap-10px">
      <OnIcon name="loader-circle" :size="16" class="security-scan-spin shrink-0 text-brand-coral" />
      <h2 class="m-0 min-w-0 flex-1 truncate text-14px font-600 text-ink-primary">{{ title }}</h2>
      <OnButton variant="secondary" size="sm" @click="emit('open')">{{ t('security.toasts.view') }}</OnButton>
    </div>
    <OnProgressMeter class="mt-12px" :value="null" size="sm" :label="t('security.scan.progressLabel')" />
    <p v-if="currentPath" class="m-0 mt-8px h-16px truncate font-mono text-11.5px text-ink-tertiary" :title="fullPath">
      {{ currentPath }}
    </p>
  </section>
</template>

<style scoped>
.security-scan-spin {
  animation: security-scan-spin 1s linear infinite;
}
@keyframes security-scan-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (prefers-reduced-motion: reduce) {
  .security-scan-spin {
    animation: none;
  }
}
</style>
