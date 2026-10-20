<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import type { PackageSummary } from '@opennavo/api';
import { formatCount, formatList, formatVersion, interpolate } from '@opennavo/shared';
import {
  OnButton,
  OnEmpty,
  OnErrorState,
  OnPagination,
  OnSearchField,
  OnSearchResultsView,
  useUiMessages
} from '@opennavo/ui';
import type { OnSearchResult } from '@opennavo/ui';
import { toAppLocale } from '~/utils/locale';
import { readPage, readSearchQuery } from '~/utils/catalogQuery';

// Search (08 §10.4): shared OnSearchResultsView (ADR-017); query/count/related searches, search box,
// row results with alias hints; zero results offer a listing suggestion. Cask-only (ADR-018).
const PAGE_SIZE = 24;

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const messages = useUiMessages();
const NuxtLink = resolveComponent('NuxtLink');
const { query, hrefWith } = useCatalogQuery({ page: 1 });

const q = computed(() => readSearchQuery(query.value.q));
const page = computed(() => readPage(query.value.page));

const { data, error, refresh } = await useAsyncData(
  () => `search:${q.value}:${page.value}:${locale.value}`,
  () =>
    q.value
      ? unwrap(
          api.GET('/search', {
            params: {
              query: {
                q: q.value,
                current: page.value,
                size: PAGE_SIZE,
                platform: 'web'
              }
            }
          })
        )
      : Promise.resolve(null)
);

const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / PAGE_SIZE)));

// Enter submits a new query and resets page to one.
const input = ref(q.value);
watch(q, value => {
  input.value = value;
});
function submit(value: string) {
  const next = value.trim().slice(0, 64);
  if (next) void navigateTo({ path: localePath('/search'), query: { q: next } });
}

// Report click positions anonymously to improve ranking; failure never blocks navigation.
function reportClick(result: OnSearchResult, index: number) {
  const queryId = data.value?.queryId;
  if (!queryId) return;
  const position = (page.value - 1) * PAGE_SIZE + index + 1;
  void api
    .POST('/search/clicks', { body: { queryId, kind: result.kind, token: result.token, position } })
    .catch(() => undefined);
}

// Show matched alias/old-name hints only when different from display name.
function aliasOf(hit: { package: PackageSummary; matchedName?: string | null }): string | null {
  const name = hit.matchedName?.trim();
  if (!name) return null;
  const same = [hit.package.displayName, hit.package.name, hit.package.token].some(
    value => value.toLowerCase() === name.toLowerCase()
  );
  return same ? null : name;
}

// Link apps carry their download addresses, so the row's Download opens the developer's page directly.
const linkOf = (token: string) => data.value?.records.find(hit => hit.package.token === token)?.package.link ?? null;

const results = computed<OnSearchResult[] | null>(() => {
  if (!q.value || error.value) return null;
  return data.value
    ? data.value.records.map(hit => {
        const alias = aliasOf(hit);
        return {
          key: `${hit.package.kind}/${hit.package.token}`,
          kind: hit.package.kind,
          token: hit.package.token,
          name: hit.package.displayName,
          src: hit.package.iconUrl,
          variants: hit.package.iconVariants,
          accent: hit.package.accentColor,
          meta: formatVersion(hit.package.version),
          description: hit.package.summary,
          matched: alias ? t('search.matched', { name: alias }) : null,
          href: localePath(`/apps/${hit.package.token}`)
        };
      })
    : null;
});

usePageSeo({
  title: () => (q.value ? t('search.metaTitle', { q: q.value }) : t('search.title')),
  noindex: true
});
</script>

<template>
  <OnSearchResultsView
    :title="q ? t('search.resultsTitle', { q }) : t('search.title')"
    :subtitle="
      q && data
        ? t('search.count', { total: formatCount(data.total, { locale: toAppLocale(locale) }) }, { plural: data.total })
        : undefined
    "
    :hint="
      data?.expandedTerms?.length
        ? t('search.alsoSearched', { terms: formatList(data.expandedTerms, { locale: toAppLocale(locale) }) })
        : undefined
    "
    :results="results"
    :link-as="NuxtLink"
    @navigate="reportClick"
  >
    <template #search>
      <OnSearchField v-model="input" :placeholder="t('nav.search')" clearable @submit="submit" />
    </template>
    <OnErrorState v-if="q && error" @retry="refresh()" />
    <template #action="{ result }">
      <GetMenu :kind="result.kind" :token="result.token" :name="result.name" :link="linkOf(result.token)" />
    </template>
    <template #empty>
      <OnEmpty
        v-if="q && data && !error"
        icon="lucide:search"
        :title="interpolate(messages.state.noResultsTitle, { q })"
        :description="messages.state.noResultsDescription"
      >
        <template #actions>
          <OnButton :href="localePath(`/feedback?type=suggest_package&q=${encodeURIComponent(q)}`)" :link-as="NuxtLink">
            {{ t('search.suggest') }}
          </OnButton>
        </template>
      </OnEmpty>
      <OnEmpty
        v-else-if="!q"
        icon="lucide:search"
        :title="t('search.emptyQueryTitle')"
        :description="t('search.emptyQueryDescription')"
      />
    </template>
    <template #footer>
      <OnPagination
        v-if="q && pageCount > 1"
        class="mt-24px self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
    </template>
  </OnSearchResultsView>
</template>
