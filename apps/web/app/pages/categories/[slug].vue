<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount, formatList } from '@opennavo/shared';
import { OnCategoryChips, OnEmpty, OnErrorState, OnPackageListView, OnPagination } from '@opennavo/ui';
import { CATALOG_SORTS, GRID_PAGE_SIZE, pageCount as countPages, readEnum, readFlag } from '~/utils/catalogQuery';
import { findCategoryPath } from '~/utils/categories';
import { categoryFamily, otherCategories } from '~/utils/categoryLinks';
import { packagePath } from '~/utils/packageView';
import { categoryDescription } from '~/utils/seoTitles';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Category page (08 §10.2): shared OnPackageListView (ADR-017), breadcrumbs, title/description, sort and Show disabled,
// card grid, pagination. Cask-only (ADR-018). T1: real links to the parent, the subcategories or siblings (the
// chip row) and the other top-level categories, so the tree is crawlable from every category page.
const pageSize = GRID_PAGE_SIZE;

const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
const { query, hrefWith, update } = useCatalogQuery({ page: 1, sort: 'popular', disabled: false });

const slug = computed(() => String(route.params.slug ?? ''));
const { page, assertExists } = useListPage();
useKnownQueryValue('sort', CATALOG_SORTS);
const sort = computed(() => readEnum(query.value.sort, CATALOG_SORTS, 'popular'));
const includeDisabled = computed(() => readFlag(query.value.disabled));

const { data: tree } = await useAsyncData(
  () => `categories:${locale.value}`,
  () => unwrap(api.GET('/categories'))
);

const path = computed(() => findCategoryPath(tree.value ?? [], slug.value));
const category = computed(() => path.value.at(-1));

if (tree.value && !category.value) {
  throw createError({ statusCode: 404, statusMessage: 'Category not found', fatal: true });
}

const { data, error, refresh, status } = await useAsyncData(
  () =>
    `packages:category:${slug.value}:${sort.value}:${page.value}:${pageSize}:${includeDisabled.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages', {
        params: {
          query: {
            category: slug.value,
            includeFonts: true,
            sort: sort.value,
            current: page.value,
            size: pageSize,
            includeDisabled: includeDisabled.value
          }
        }
      })
    )
);

assertExists(() => data.value?.total, pageSize, status);
const pageCount = computed(() => countPages(data.value?.total ?? 0, pageSize));
const sorts = useCatalogSorts();
// Description and count; desktop has only count.
const subtitle = computed(() => {
  const total = data.value
    ? t(
        'catalog.total',
        { total: formatCount(data.value.total, { locale: formattingLocale.value }) },
        { plural: data.value.total }
      )
    : null;
  return [category.value?.description, total].filter(Boolean).join(' · ') || undefined;
});
const crumbs = computed(() => [
  { label: t('categories.title'), href: localePath('/categories') },
  ...path.value.map((node, index) =>
    index === path.value.length - 1
      ? { label: node.name }
      : { label: node.name, href: localePath(`/categories/${node.slug}`) }
  )
]);
useBreadcrumbSchema(crumbs);
const categoryHref = (node: { slug: string }) => localePath(`/categories/${node.slug}`);
const family = computed(() => categoryFamily(path.value));
const chips = computed(() =>
  family.value
    ? [
        {
          value: family.value.parent.slug,
          label: t('categories.allIn', { name: family.value.parent.name }),
          href: categoryHref(family.value.parent)
        },
        ...family.value.children.map(node => ({ value: node.slug, label: node.name, href: categoryHref(node) }))
      ]
    : []
);
const others = computed(() =>
  otherCategories(tree.value ?? [], path.value).map(node => ({
    slug: node.slug,
    name: node.name,
    href: categoryHref(node)
  }))
);
// Share image (WP13): the category beside the first apps it lists.
if (category.value)
  defineOgImage('OgListSatori', {
    locale: formattingLocale.value,
    label: t('og.category'),
    title: category.value.name,
    description: category.value.description ?? undefined,
    icons: (data.value?.records ?? []).slice(0, 4).map(pkg => ({
      name: pkg.displayName,
      token: pkg.token,
      src: pkg.iconUrl,
      accent: pkg.accentColor
    })),
    ...useOgFrame()
  });
useItemListSchema(
  () =>
    error.value
      ? null
      : data.value?.records.map(pkg => ({ name: pkg.displayName, path: localePath(packagePath(pkg.token)) })),
  () => (page.value - 1) * pageSize + 1,
  () => data.value?.total
);

// WP1: no "best" in titles; the legacy title is the rollout fallback selected in admin.
const legacyTitles = useLegacyTitles();
// Sorted and filtered variants stay out of the index through the route query contract (S4).
usePageSeo({
  title: () =>
    category.value?.seoTitle?.trim() ||
    t(legacyTitles.value ? 'categories.itemMetaTitle' : 'categories.seo.itemTitle', {
      name: category.value?.name ?? ''
    }),
  page,
  description: () => {
    const value = category.value;
    if (!value) return t('categories.description');
    if (value.seoDescription?.trim()) return value.seoDescription.trim();
    const total = data.value?.total ?? 0;
    return (
      categoryDescription(
        {
          name: value.name,
          description: value.description,
          total,
          totalLabel: formatCount(total, { locale: formattingLocale.value }),
          apps: (data.value?.records ?? []).map(pkg => pkg.displayName)
        },
        t,
        values => formatList(values, { locale: formattingLocale.value })
      ) ?? t('categories.description')
    );
  }
});
</script>

<template>
  <OnPackageListView
    v-if="category"
    :breadcrumb="crumbs"
    :title="category.name"
    :subtitle="subtitle"
    :sorts="sorts"
    :sort="sort"
    :sort-label="t('catalog.sortLabel')"
    :items="error ? [] : (data?.records ?? null)"
    :link-as="NuxtLink"
    :fit-to-width="false"
    @update:sort="update({ sort: $event })"
  >
    <template #filters>
      <DisabledToggle :model-value="includeDisabled" @update:model-value="update({ disabled: $event })" />
    </template>
    <OnCategoryChips
      v-if="chips.length > 1"
      class="mb-16px"
      :model-value="slug"
      :items="chips"
      :aria-label="t('categories.subcategories')"
      :link-as="NuxtLink"
    />
    <OnErrorState v-if="error" @retry="refresh()" />
    <template #card="{ pkg }">
      <AppCard :pkg="pkg" />
    </template>
    <template #empty>
      <OnEmpty v-if="!error" :title="t('catalog.emptyTitle')" :description="t('catalog.emptyDescription')" />
    </template>
    <template #footer>
      <OnPagination
        v-if="pageCount > 1"
        class="mt-24px self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
      <nav v-if="others.length" class="mt-32px flex flex-col gap-10px" aria-labelledby="category-others">
        <h2 id="category-others" class="m-0 text-15px font-600 text-ink-primary">{{ t('categories.others') }}</h2>
        <ul class="m-0 flex list-none flex-wrap gap-x-18px gap-y-8px p-0">
          <li v-for="item in others" :key="item.slug">
            <NuxtLink
              :to="item.href"
              class="text-13.5px text-ink-secondary no-underline underline-offset-3 hover:text-ink-primary hover:underline"
              >{{ item.name }}</NuxtLink
            >
          </li>
        </ul>
      </nav>
    </template>
  </OnPackageListView>
</template>
