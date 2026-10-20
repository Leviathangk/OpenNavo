<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnCategoriesView, OnErrorState } from '@opennavo/ui';
import type { OnCategoriesItem } from '@opennavo/ui';
import { brandlessTitle } from '~/utils/seoTitles';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Category overview (05 §4): shared OnCategoriesView (ADR-017), top-level cards with descriptions/subcategories.
const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const { data, error, refresh } = await useAsyncData(
  () => `categories:${locale.value}`,
  () => unwrap(api.GET('/categories'))
);

const items = computed<OnCategoriesItem[] | null>(() =>
  data.value
    ? data.value
        .filter(node => !node.hiddenByDefault)
        .map(node => ({
          key: node.slug,
          name: node.name,
          icon: node.icon,
          count: t(
            'categories.count',
            { count: formatCount(node.packageCount, { locale: formattingLocale.value }) },
            { plural: node.packageCount }
          ),
          description: node.description,
          children: node.children.map(child => child.name),
          // T1: every subcategory is a real link, not only text inside its parent's card.
          childLinks: node.children
            .filter(child => !child.hiddenByDefault)
            .map(child => ({ name: child.name, href: localePath(`/categories/${child.slug}`) })),
          href: localePath(`/categories/${node.slug}`)
        }))
    : null
);

// The search snippet names what the categories cover; the page subtitle and share image keep the short line.
usePageSeo({ title: () => t('categories.metaTitle'), description: () => t('categories.metaDescription') });
// Share image (WP13): category icons are not app icons, so the brand icon stands beside the text.
defineOgImage('OgListSatori', {
  locale: formattingLocale.value,
  label: t('categories.title'),
  title: brandlessTitle(t('categories.metaTitle')),
  description: t('categories.description'),
  ...useOgFrame()
});
</script>

<template>
  <OnCategoriesView
    :title="t('categories.title')"
    :subtitle="t('categories.description')"
    :items="error ? [] : items"
    :link-as="NuxtLink"
  >
    <OnErrorState v-if="error" @retry="refresh()" />
  </OnCategoriesView>
</template>
