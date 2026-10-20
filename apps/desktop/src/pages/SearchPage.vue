<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink, useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnButton, OnEmpty, OnSearchField, OnSearchResultsView, OnSegmented } from '@opennavo/ui';
import type { OnSearchResult, OnSegmentedOption } from '@opennavo/ui';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import MasResults from '@/components/appstore/MasResults.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCatalogLoader } from '@/composables/useCatalogLoader';
import { searchApps } from '@/composables/searchApps';
import { useMasSearch } from '@/composables/useMasSearch';
import { useSettingsStore } from '@/stores';
import { useMasStore } from '@/stores/mas';
import { useCatalogStore } from '@/stores/catalog';

// Search (local FTS, 06 §7.1): Enter on View all results in the Command-K palette; truncate q to 64 characters, matching deep links. Cask-only (ADR-018).
// Shared with the web view (ADR-017). App Store results (store/security plan §5) follow in their own group, straight from Apple.
const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const route = useRoute();
const router = useRouter();
const catalog = useCatalogStore();
const retrying = ref(false);
void catalog.loadStatus().catch(() => undefined);
async function retryCatalog() {
  if (retrying.value) return;
  retrying.value = true;
  try {
    await catalog.sync();
  } catch {
    // The persisted source error remains visible; cached search results stay usable.
  } finally {
    retrying.value = false;
  }
}

const query = ref(String(route.query.q ?? '').slice(0, 64));

watch(
  () => route.query.q,
  value => {
    query.value = String(value ?? '').slice(0, 64);
  }
);
type Source = 'all' | 'catalog' | 'appstore';
const SOURCES: readonly Source[] = ['all', 'catalog', 'appstore'];
const source = ref<Source>(SOURCES.find(value => value === route.query.source) ?? 'all');
watch([query, source], ([value, from]) => {
  const next: Record<string, string> = {};
  if (value) next.q = value;
  if (value && from !== 'all') next.source = from;
  void router.replace({ query: next });
});

const mas = useMasStore();
void mas.loadStatus().catch(() => undefined);
// Installed App Store apps offer Open instead of Get; a failure only leaves Get on those rows.
void mas.loadInstalled().catch(() => undefined);
// Settings → App Store can turn the App Store group off; the catalog search is unaffected.
const settings = useSettingsStore();
const masOn = computed(() => settings.value?.appStoreSearch ?? true);
const masSearch = useMasSearch(
  query,
  computed(() => masOn.value && source.value !== 'catalog')
);

const { data, error } = useCatalogLoader(
  async () => {
    const q = query.value.trim();
    if (!q) return null;
    const result = await searchApps(q, 100);
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
          // The token is already the meta line, so a match on it adds nothing (as on the web).
          matched:
            hit.matchedName && hit.matchedName.toLowerCase() !== hit.item.token.toLowerCase()
              ? t('search.alias', { name: hit.matchedName })
              : null,
          href: `/package/${hit.item.kind}/${hit.item.token}`
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

const sourceOptions = computed<OnSegmentedOption<Source>[]>(() => [
  { value: 'all', label: t('search.sources.all') },
  { value: 'catalog', label: t('search.sources.catalog'), count: shown.value?.result.total },
  { value: 'appstore', label: t('appStore.title'), count: masSearch.results.value?.length }
]);
const showMas = computed(() => masOn.value && source.value !== 'catalog' && query.value.trim().length >= 2);
// With App Store results below, an empty catalog is one line instead of the full empty state.
const masHasResults = computed(() => showMas.value && Boolean(masSearch.results.value?.length));
</script>

<template>
  <OnSearchResultsView
    :title="t('search.title')"
    :subtitle="subtitle"
    :results="source === 'appstore' && masOn ? [] : results"
    :link-as="RouterLink"
  >
    <template #search>
      <OnSearchField v-model="query" :placeholder="t('search.prompt')" clearable />
      <div v-if="catalog.status?.linksError" class="mt-12px flex items-center gap-12px" role="status">
        <span class="text-13px text-ink-secondary">{{ t('search.catalogIncomplete') }}</span>
        <OnButton size="sm" variant="secondary" :disabled="retrying" @click="retryCatalog">
          {{ t('common.retry') }}
        </OnButton>
      </div>
    </template>
    <OnSegmented
      v-if="masOn && query.trim()"
      v-model="source"
      class="mb-16px self-start"
      size="sm"
      :options="sourceOptions"
      :aria-label="t('search.sources.label')"
    />
    <template #action="{ result }">
      <PackageGetButton :kind="result.kind" :token="result.token" :disabled="disabledOf(result.token)" />
    </template>
    <template #empty>
      <template v-if="source !== 'appstore' || !masOn || !query.trim()">
        <!-- The prompt also covers the first search in flight, so the area never blanks before results arrive. -->
        <OnEmpty v-if="!shown" :title="t('search.prompt')" icon="lucide:search" />
        <p v-else-if="masHasResults" class="m-0 text-13px text-ink-tertiary">
          {{ t('search.catalogEmpty', { q: shown.q }) }}
        </p>
        <OnEmpty
          v-else
          :title="t('search.empty', { q: shown.q })"
          :description="t('search.emptyHint')"
          icon="lucide:search"
        />
      </template>
    </template>
    <template #footer>
      <MasResults
        v-if="showMas"
        :results="masSearch.results.value"
        :query="masSearch.shownQuery.value || query.trim()"
        :loading="masSearch.loading.value"
        :error="masSearch.error.value"
        :country="mas.status?.country ?? null"
        :expanded="source === 'appstore'"
      />
    </template>
  </OnSearchResultsView>
</template>
