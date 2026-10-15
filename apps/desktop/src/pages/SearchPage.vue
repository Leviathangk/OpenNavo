<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnEmpty, OnSearchField, OnSearchResultsView } from '@opennavo/ui';
import type { OnSearchResult } from '@opennavo/ui';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import { commands, unwrap } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCatalogLoader } from '@/composables/useCatalogLoader';

// Search (local FTS, 06 §7.1): Enter on View all results in the Command-K palette; truncate q to 64 characters, matching deep links. Cask-only (ADR-018).
// Shared with the web view (ADR-017).
const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const route = useRoute();
const router = useRouter();

const query = ref(String(route.query.q ?? '').slice(0, 64));

watch(
  () => route.query.q,
  value => {
    query.value = String(value ?? '').slice(0, 64);
  }
);
watch(query, value => void router.replace({ query: value ? { q: value } : {} }));

const { data, error } = useCatalogLoader(
  async () => {
    const q = query.value.trim();
    if (!q) return null;
    const result = await unwrap(
      commands.catalogSearch({
        q,
        kind: 'cask',
        category: null,
        includeDisabled: false,
        limit: 100,
        offset: 0
      })
    );
    return { q, result };
  },
  [query, appLocale],
  value => value?.result.items.map(hit => hit.item) ?? [],
  // Each keystroke starts a new search; keep the last results on screen until it lands so the page never empties and refills.
  []
);

// The results on screen belong to shown.q, which trails the field by one search while typing. A failed search
// keeps results for the current query on screen, never those of an earlier one.
const shown = computed(() => {
  const q = query.value.trim();
  return q && data.value && (!error.value || data.value.q === q) ? data.value : null;
});
const results = computed<OnSearchResult[] | null>(() =>
  !query.value.trim()
    ? []
    : shown.value
      ? shown.value.result.items.map(hit => ({
          key: `${hit.item.kind}/${hit.item.token}`,
          kind: hit.item.kind,
          token: hit.item.token,
          name: pick(hit.item.displayName, hit.item.name, hit.item.sourceLocale),
          src: hit.item.iconUrl,
          accent: hit.item.accentColor,
          meta: hit.item.token,
          description: pick(hit.item.summary, '', hit.item.sourceLocale) || null,
          matched: hit.matchedName ? t('search.alias', { name: hit.matchedName }) : null,
          href: `/package/cask/${hit.item.token}`
        }))
      : error.value
        ? []
        : null
);
// The count line keeps its height without a count (no query, first search pending), so the field never moves.
const subtitle = computed(() =>
  shown.value
    ? t('search.results', { q: shown.value.q, count: shown.value.result.total }, { plural: shown.value.result.total })
    : '\u00a0'
);
const disabledOf = (token: string) => shown.value?.result.items.find(hit => hit.item.token === token)?.item.disabled;
</script>

<template>
  <OnSearchResultsView :title="t('search.title')" :subtitle="subtitle" :results="results" :link-as="RouterLink">
    <template #search>
      <OnSearchField v-model="query" :placeholder="t('search.prompt')" clearable />
    </template>
    <template #action="{ result }">
      <PackageGetButton :kind="result.kind" :token="result.token" :disabled="disabledOf(result.token)" />
    </template>
    <template #empty>
      <!-- The prompt also covers the first search in flight, so the area never blanks before results arrive. -->
      <OnEmpty v-if="!shown" :title="t('search.prompt')" icon="lucide:search" />
      <OnEmpty
        v-else
        :title="t('search.empty', { q: shown.q })"
        :description="t('search.emptyHint')"
        icon="lucide:search"
      />
    </template>
  </OnSearchResultsView>
</template>
