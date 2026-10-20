<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnBreadcrumb, OnButton, OnChip, OnEmpty, OnErrorState, OnIcon, OnPagination } from '@opennavo/ui';
import {
  BACKGROUND_PAGE_SIZE,
  declaringApps,
  groupByDeveloper,
  indexPageNoindex,
  itemTypes,
  labelPath,
  readLabelQuery,
  usableContent
} from '~/utils/backgroundItems';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { packagePath } from '~/utils/packageView';
import { brandlessTitle } from '~/utils/seoTitles';

// Background items encyclopedia (F1): every label a catalog app's Homebrew definition declares (launchd
// jobs, login items, package receipts), grouped by the developer of the apps that declare it, paginated,
// with ?q= to find a label. A label is a name, not an identity: rows say which apps declare it, never that
// an item on someone's Mac is theirs. Searches and pages without a ready entry stay out of the index.
const SHOWN_APPS = 3;

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { hrefWith } = useCatalogQuery({ page: 1 });
const { page, assertExists } = useListPage();

const notFound = () => createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
// A repeated ?q= is a 404, like other repeated list parameters (S4).
const query = computed(() => readLabelQuery(route.query.q));
if (query.value === undefined) throw notFound();
watch(query, value => {
  if (value === undefined) showError(notFound());
});

const { data, error, refresh, status } = await useAsyncData(
  () => `background-items:${page.value}:${query.value ?? ''}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/background-items', {
        params: {
          query: { current: page.value, size: BACKGROUND_PAGE_SIZE, ...(query.value ? { q: query.value } : {}) }
        }
      })
    )
);
assertExists(() => data.value?.total, BACKGROUND_PAGE_SIZE, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, BACKGROUND_PAGE_SIZE));

const collator = computed(() => new Intl.Collator(appLocale.value));
const groups = computed(() =>
  data.value && !error.value
    ? groupByDeveloper(data.value.records, collator.value).map(group => ({
        key: group.key,
        name:
          group.developer ??
          (group.several ? t('backgroundItems.severalDevelopers') : t('backgroundItems.unknownDeveloper')),
        rows: group.items.map(item => {
          const apps = declaringApps(item);
          return {
            label: item.label,
            path: localePath(labelPath(item.label)),
            types: itemTypes(item),
            shared: item.shared,
            described: Boolean(usableContent(item)),
            apps: apps.slice(0, SHOWN_APPS).map(app => ({
              token: app.package.token,
              name: app.package.displayName,
              path: localePath(packagePath(app.package.token))
            })),
            more: Math.max(0, apps.length - SHOWN_APPS)
          };
        })
      }))
    : []
);
// Structured data names the entries in the order the page shows them (grouped), numbered across pages.
useItemListSchema(
  () => groups.value.flatMap(group => group.rows.map(row => ({ name: row.label, path: row.path }))),
  () => (page.value - 1) * BACKGROUND_PAGE_SIZE + 1,
  () => (query.value ? undefined : data.value?.total)
);

const breadcrumb = computed(() => [
  { label: t('nav.security'), href: localePath('/security') },
  { label: t('backgroundItems.title') }
]);
useBreadcrumbSchema(breadcrumb);

// A search, or a page none of whose labels has a verified description yet, is noindex, follow.
const noindex = computed(() =>
  data.value && !error.value ? indexPageNoindex(data.value.records, query.value ?? '') : false
);
useHead(() =>
  noindex.value ? { meta: [{ name: 'robots', content: 'noindex, follow', tagPriority: 'high' as const }] } : {}
);

const total = computed(() =>
  data.value
    ? t(
        'backgroundItems.total',
        { total: formatCount(data.value.total, { locale: appLocale.value }) },
        { plural: data.value.total }
      )
    : undefined
);

const draft = ref(query.value ?? '');
watch(query, value => (draft.value = value ?? ''));
const ids = { search: useId(), hint: useId() };
function search() {
  const q = readLabelQuery(draft.value) ?? '';
  void navigateTo(localePath({ path: '/background-items', query: q ? { q } : {} }));
}

usePageSeo({
  title: () => t('backgroundItems.metaTitle'),
  description: () => t('backgroundItems.description'),
  page
});
defineOgImage('OgListSatori', {
  locale: appLocale.value,
  label: t('backgroundItems.ogLabel'),
  title: brandlessTitle(t('backgroundItems.metaTitle')),
  description: t('backgroundItems.description'),
  icons: ogAppIcons(
    [
      ...new Map(
        (data.value?.records ?? []).flatMap(item => item.apps.map(owner => [owner.package.token, owner.package]))
      ).values()
    ].filter(pkg => pkg.iconUrl)
  ),
  ...useOgFrame()
});
</script>

<template>
  <div class="box-border flex flex-col gap-20px pt-6px">
    <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
    <PageHeader :title="t('backgroundItems.title')" :description="t('backgroundItems.description')" :meta="total" />
    <p
      class="m-0 flex max-w-760px items-start gap-8px rounded-big bg-status-warning-subtle px-14px py-10px text-13px leading-[1.6] text-ink-secondary"
    >
      <OnIcon name="triangle-alert" :size="15" class="mt-3px shrink-0 text-status-warning" />
      <span>{{ t('backgroundItems.caution') }}</span>
    </p>
    <form
      role="search"
      method="get"
      :action="localePath('/background-items')"
      class="flex max-w-560px flex-wrap items-center gap-8px"
      @submit.prevent="search"
    >
      <label :for="ids.search" class="sr-only">{{ t('backgroundItems.search.label') }}</label>
      <input
        :id="ids.search"
        v-model="draft"
        type="search"
        name="q"
        maxlength="255"
        autocomplete="off"
        spellcheck="false"
        :aria-describedby="ids.hint"
        :placeholder="t('backgroundItems.search.placeholder')"
        class="box-border h-34px min-w-0 flex-1 rounded-default border border-solid border-line-default bg-component-search-bg px-12px font-mono text-13px text-ink-primary outline-none placeholder:font-sans placeholder:text-ink-tertiary focus-visible:shadow-focus-ring"
      />
      <OnButton type="submit" variant="secondary" icon="search">{{ t('backgroundItems.search.submit') }}</OnButton>
      <span :id="ids.hint" class="w-full text-12px text-ink-tertiary">{{ t('backgroundItems.search.hint') }}</span>
    </form>
    <p v-if="query" class="m-0 flex flex-wrap items-center gap-10px text-13px text-ink-secondary">
      <span>{{ t('backgroundItems.results', { q: query }) }}</span>
      <NuxtLink
        :to="localePath('/background-items')"
        class="text-12.5px text-ink-tertiary underline-offset-3 hover:text-ink-primary hover:underline"
        >{{ t('backgroundItems.clearSearch') }}</NuxtLink
      >
    </p>

    <OnErrorState v-if="error" @retry="refresh()" />
    <template v-else-if="groups.length">
      <section v-for="group in groups" :key="group.key" class="flex flex-col gap-8px">
        <h2 class="m-0 text-15px font-600 text-ink-primary">{{ group.name }}</h2>
        <ul class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
          <li
            v-for="row in group.rows"
            :key="row.label"
            class="flex flex-col gap-6px border-t border-t-solid border-line-subtle px-16px py-12px first:border-t-0"
          >
            <div class="flex flex-wrap items-center gap-x-10px gap-y-6px">
              <NuxtLink
                :to="row.path"
                class="min-w-0 break-all font-mono text-13.5px font-600 text-ink-primary no-underline underline-offset-3 hover:underline"
                >{{ row.label }}</NuxtLink
              >
              <OnChip v-for="type in row.types" :key="type" size="sm">{{ t(`backgroundItems.types.${type}`) }}</OnChip>
              <OnChip v-if="row.shared" tone="warning" size="sm">{{ t('backgroundItems.shared') }}</OnChip>
              <OnChip v-if="row.described" tone="success" size="sm">{{ t('backgroundItems.described') }}</OnChip>
            </div>
            <p v-if="row.apps.length" class="m-0 text-12.5px leading-[1.6] text-ink-tertiary">
              <span>{{ `${t('backgroundItems.declaredBy')} ` }}</span>
              <template v-for="(app, index) in row.apps" :key="app.token"
                ><span v-if="index">{{ t('backgroundItems.listSeparator') }}</span
                ><NuxtLink
                  :to="app.path"
                  class="text-ink-secondary underline-offset-3 hover:text-ink-primary hover:underline"
                  >{{ app.name }}</NuxtLink
                ></template
              ><span v-if="row.more">{{ ` ${t('backgroundItems.moreApps', { count: row.more })}` }}</span>
            </p>
          </li>
        </ul>
      </section>
    </template>
    <OnEmpty
      v-else-if="data"
      icon="lucide:search"
      :title="t('backgroundItems.emptyTitle')"
      :description="query ? t('backgroundItems.emptySearch', { q: query }) : t('backgroundItems.emptyAll')"
    />
    <OnPagination
      v-if="pageCount > 1 && !error"
      class="mt-8px self-center"
      :page="page"
      :page-count="pageCount"
      :href-for="target => hrefWith({ page: target })"
      :link-as="NuxtLink"
    />
  </div>
</template>
