<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnEmpty, OnErrorState, OnPagination, OnRankingsView } from '@opennavo/ui';
import type { OnRankingsEntry } from '@opennavo/ui';
import { RANKING_PERIODS, readEnum, readPage } from '~/utils/catalogQuery';
import { flattenCategories } from '~/utils/categories';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Rankings (08 §10.3): shared OnRankingsView (ADR-017), 30/90/365 days, category selector, pagination.
// Rows show rank, change, app, full grouped install count, Get menu. Cask-only (ADR-018).
const PAGE_SIZE = 50;

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const selectId = useId();
const { query, hrefWith, update } = useCatalogQuery({ page: 1, period: '30d', category: '' });

const period = computed(() => readEnum(query.value.period, RANKING_PERIODS, '30d'));
const category = computed(() => (typeof query.value.category === 'string' ? query.value.category : ''));
const page = computed(() => readPage(query.value.page));

const { data: tree } = await useAsyncData(
  () => `categories:${locale.value}`,
  () => unwrap(api.GET('/categories'))
);

const { data, error, refresh } = await useAsyncData(
  () => `rankings:${period.value}:${category.value}:${page.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/rankings', {
        params: {
          query: {
            kind: 'cask',
            includeFonts: true,
            period: period.value,
            category: category.value || undefined,
            current: page.value,
            size: PAGE_SIZE
          }
        }
      })
    )
);

const periodOptions = computed(() => RANKING_PERIODS.map(value => ({ value, label: t(`rankings.period.${value}`) })));
// Category selector includes only app categories.
const categoryOptions = computed(() =>
  flattenCategories(tree.value ?? []).filter(
    ({ node }) => !node.hiddenByDefault && (node.appliesTo === 'both' || node.appliesTo === 'cask')
  )
);

const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / PAGE_SIZE)));

const entries = computed<OnRankingsEntry[] | null>(() =>
  data.value
    ? data.value.records.map(entry => ({
        rank: entry.rank,
        change: entry.rankChange ?? null,
        pkg: entry.package,
        value: formatCount(entry.installs, { locale: formattingLocale.value }),
        href: localePath(`/apps/${entry.package.token}`)
      }))
    : null
);

function onCategory(event: Event) {
  update({ category: (event.target as HTMLSelectElement).value || undefined });
}

// Positions follow the ranks on the page.
useItemListSchema(
  () => (error.value ? null : entries.value?.map(entry => ({ name: entry.pkg.displayName, path: entry.href }))),
  () => entries.value?.[0]?.rank ?? 1
);

usePageSeo({ title: () => t('rankings.metaTitle'), description: () => t('rankings.description') });
</script>

<template>
  <OnErrorState v-if="error" class="pt-14px" @retry="refresh()" />
  <OnRankingsView
    v-else
    :title="t('rankings.title')"
    :subtitle="t('rankings.description')"
    :periods="periodOptions"
    :period="period"
    :period-label="t('rankings.periodLabel')"
    :entries="entries"
    :link-as="NuxtLink"
    @update:period="update({ period: $event })"
  >
    <template #filters>
      <span class="inline-flex items-center gap-8px text-13px text-ink-secondary">
        <label :for="selectId">{{ t('rankings.category') }}</label>
        <select
          :id="selectId"
          class="h-28px rounded-default border border-solid border-line-default bg-component-search-bg px-8px font-sans text-13px text-ink-primary outline-none focus-visible:shadow-focus-ring"
          :value="category"
          @change="onCategory"
        >
          <option value="">{{ t('rankings.allCategories') }}</option>
          <option v-for="{ node, depth } in categoryOptions" :key="node.slug" :value="node.slug">
            {{ `${'　'.repeat(depth)}${node.name}` }}
          </option>
        </select>
      </span>
    </template>
    <template #action="{ entry }">
      <GetMenu :kind="entry.pkg.kind" :token="entry.pkg.token" :name="entry.pkg.displayName" />
    </template>
    <template #empty>
      <OnEmpty :title="t('catalog.emptyTitle')" :description="t('catalog.emptyDescription')" />
    </template>
    <template #footer>
      <OnPagination
        v-if="pageCount > 1"
        class="mt-20px self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
    </template>
  </OnRankingsView>
</template>
