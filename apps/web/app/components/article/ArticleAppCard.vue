<script setup lang="ts">
import type { PackageSummary } from '@opennavo/api';
import { formatCountCompact, formatVersion } from '@opennavo/shared';
import { OnAppRow } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';

// ::app[token] card (WP17): the app as the catalog shows it now. The label keeps readers from taking
// the live version or install count for what the article tested.
const props = defineProps<{ pkg: PackageSummary }>();

const { t, locale } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const appLocale = computed(() => toAppLocale(locale.value));
const meta = computed(() =>
  [
    formatVersion(props.pkg.version),
    props.pkg.installs30d
      ? t('articles.installs', { count: formatCountCompact(props.pkg.installs30d, { locale: appLocale.value }) })
      : ''
  ]
    .filter(Boolean)
    .join(' · ')
);
</script>

<template>
  <aside
    class="my-16px box-border rounded-big border border-solid border-line-subtle bg-surface-card px-14px"
    :aria-label="pkg.displayName"
    data-testid="article-app-card"
  >
    <OnAppRow
      :kind="pkg.kind"
      :token="pkg.token"
      :name="pkg.displayName"
      :src="pkg.iconUrl"
      :accent="pkg.accentColor"
      :meta="meta"
      :description="pkg.summary"
      :href="localePath(packagePath(pkg.token))"
      :link-as="NuxtLink"
    >
      <template #actions>
        <GetMenu :kind="pkg.kind" :token="pkg.token" :name="pkg.displayName" />
      </template>
    </OnAppRow>
    <p class="m-0 border-t border-t-solid border-line-subtle py-8px text-11.5px text-ink-tertiary">
      {{ t('articles.appCardNote') }}
    </p>
  </aside>
</template>
