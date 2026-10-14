<script setup lang="ts">
import { computed, ref } from 'vue';
import { recentReturnPath, withRecentReturn } from '@opennavo/shared';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import type { PackageSummary } from '@opennavo/api';
import { OnEmpty, OnPackageListView, OnPagination } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';
import OfflineNotice from '@/components/common/OfflineNotice.vue';
import RequestError from '@/components/common/RequestError.vue';
import PackageCard from '@/components/package/PackageCard.vue';
import { commands, unwrap } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCatalogLoader } from '@/composables/useCatalogLoader';
import { fetchRecentApps, peekRecentApps } from '@/composables/useHome';
import { useLoader } from '@/composables/useLoader';
import { usePackageSummary } from '@/composables/usePackageSummary';

// Recently Updated: Discover's section in full, newest version first. Online it reads the same API list, so it opens with
// the apps Discover shows; once that request fails it reads the local catalog. The API error stays on its own loader, so
// shell recovery switches back when the service is reachable again. Cask-only (ADR-018).
const STATS: readonly OnAppCardStat[] = ['version', 'installs30d'];

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { toSummary } = usePackageSummary();

const route = useRoute();
const initialQuery = new URLSearchParams(
  recentReturnPath(
    `/recent${route.fullPath.includes('?') ? route.fullPath.slice(route.fullPath.indexOf('?')) : ''}`
  )?.split('?')[1]
);
const pageSize = ref(Number(initialQuery.get('size') ?? 24));
const page = ref(Number(initialQuery.get('page') ?? 1));
const returnTo = computed(() => `/recent?page=${page.value}&size=${pageSize.value}`);
function fitPage(size: number) {
  if (size === pageSize.value) return;
  const first = (page.value - 1) * pageSize.value;
  page.value = Math.floor(first / size) + 1;
  pageSize.value = size;
}

const { data: remote, error: remoteError } = useLoader(
  () => fetchRecentApps(appLocale.value, page.value, pageSize.value),
  [page, appLocale, pageSize],
  undefined,
  [page, appLocale, pageSize],
  () => peekRecentApps(appLocale.value, page.value, pageSize.value)
);

const { data: local, error: localError } = useCatalogLoader(
  async () => {
    if (!remoteError.value) return null;
    return unwrap(
      commands.catalogList({
        kind: 'cask',
        category: null,
        sort: 'updated',
        includeFonts: true,
        includeLibraries: false,
        includeDisabled: false,
        limit: pageSize.value,
        offset: (page.value - 1) * pageSize.value
      })
    );
  },
  [remoteError, page, appLocale, pageSize],
  result => result?.items ?? []
);

const items = computed<PackageSummary[] | null>(() => {
  if (remote.value) return remote.value.records;
  if (local.value) return local.value.items.map(toSummary);
  return localError.value ? [] : null;
});
const pageCount = computed(() =>
  Math.max(1, Math.ceil((remote.value?.total ?? local.value?.total ?? 0) / pageSize.value))
);
</script>

<template>
  <OnPackageListView
    :title="t('recent.title')"
    :subtitle="t('recent.subtitle')"
    :items="items"
    :link-as="RouterLink"
    @page-size="fitPage"
  >
    <RequestError v-if="localError && !remote" :error="localError" />
    <OfflineNotice v-else-if="remoteError && !remote" class="mb-16px" />
    <template #card="{ pkg }">
      <PackageCard :pkg="pkg" :stats="STATS" :href="withRecentReturn(`/package/${pkg.kind}/${pkg.token}`, returnTo)" />
    </template>
    <template #empty>
      <OnEmpty v-if="!localError" :title="t('common.empty')" />
    </template>
    <template #footer>
      <OnPagination v-if="pageCount > 1" v-model:page="page" class="mt-24px" :page-count="pageCount" />
    </template>
  </OnPackageListView>
</template>
