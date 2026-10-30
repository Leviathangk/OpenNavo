<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { PackageDetail } from '@opennavo/api';
import { packageInstallState, toPackageSource } from '@opennavo/shared';
import { OnPackageSourceBadge } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';

const props = defineProps<{ pkg: PackageDetail }>();
const { t } = useI18n();
const { appLocale } = useAppLocale();
const state = computed(() => packageInstallState(props.pkg));
const source = computed(() => toPackageSource(props.pkg));
const label = computed(() =>
  t(`package.sourceBadge.${state.value === 'active' ? (source.value?.verified ? 'verified' : 'label') : state.value}`)
);
const description = computed(() => {
  const value = source.value;
  if (!value) return '';
  const lines = value.entries.flatMap(entry => [
    t('package.sourceBadge.domain', { domain: entry.host }),
    entry.sha256
      ? t('package.sourceBadge.checksum', { sha256: entry.sha256 })
      : t('package.sourceBadge.checksumUnknown')
  ]);
  lines.push(
    state.value === 'disabled'
      ? t('package.disabledNotice', {
          reason: props.pkg.disable?.reason ?? props.pkg.homebrewStatus?.reason ?? t('package.noReason')
        })
      : t('package.sourceBadge.active')
  );
  if (state.value === 'deprecated')
    lines.push(
      t('package.deprecatedNotice', {
        reason: props.pkg.deprecation?.reason ?? props.pkg.homebrewStatus?.reason ?? t('package.noReason')
      })
    );
  if (state.value === 'paused') lines.push(t('package.staleNotice'));
  lines.push(
    value.checkedAt
      ? t('package.sourceBadge.checked', {
          date: new Intl.DateTimeFormat(appLocale.value, {
            year: 'numeric',
            month: 'short',
            day: 'numeric',
            timeZone: 'UTC'
          }).format(new Date(value.checkedAt))
        })
      : t('package.sourceBadge.unchecked')
  );
  return lines.join('\n');
});
</script>

<template>
  <OnPackageSourceBadge
    v-if="source"
    :label="label"
    :description="description"
    :state="state"
    :verified="source.verified"
  />
</template>
