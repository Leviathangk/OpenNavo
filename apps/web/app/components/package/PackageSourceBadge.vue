<script setup lang="ts">
import { computed } from 'vue';
import { packageInstallState } from '@opennavo/shared';
import { OnPackageSourceBadge } from '@opennavo/ui';
import { formatDate } from '~/utils/packageDate';
import { officialSource } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';

const props = defineProps<{ pkg: PackagePage }>();
const { t, locale } = useI18n();
const state = computed(() => packageInstallState(props.pkg));
const source = computed(() => officialSource(props.pkg));
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
      : t('package.officialDownload.active')
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
      ? t('package.sourceBadge.checked', { date: formatDate(value.checkedAt, { locale: toAppLocale(locale.value) }) })
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
