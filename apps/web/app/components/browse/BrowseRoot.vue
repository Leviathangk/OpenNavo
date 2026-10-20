<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { OnBreadcrumb, OnEmpty, OnErrorState } from '@opennavo/ui';
import { browsePath, browseTotal, bucketHeading } from '~/utils/browse';
import type { BrowseKind } from '~/utils/browse';
import { decorative } from '~/utils/decorative';
import { brandlessTitle } from '~/utils/seoTitles';

// A–Z directory root (T1, plan 4.2): /browse links every leaf page of the app directory and, in its own
// section, of the font directory; /browse/fonts is the font directory alone. Only apps whose overview
// is indexable in this language are listed (GET /browse reads the server's page decisions).
const props = defineProps<{ kind: BrowseKind }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const api = useApi();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const directory = (kind: BrowseKind) =>
  unwrap(api.GET('/browse', { params: { query: { kind, locale: appLocale.value } } }));

const { data, error, refresh } = await useAsyncData(
  () => `browse:${props.kind}:root:${locale.value}`,
  () => directory(props.kind)
);
// The app root also links the font leaves; that section can fail without failing the page (S7).
const { data: fonts } =
  props.kind === 'apps'
    ? await useAsyncData(
        () => `browse:fonts:root:${locale.value}:section`,
        () => decorative(directory('fonts'), null, 'browse fonts')
      )
    : { data: ref(null) };

const fontBuckets = computed(() => fonts.value?.buckets ?? []);
const buckets = computed(() => (error.value ? [] : (data.value?.buckets ?? [])));
const total = computed(() => browseTotal(data.value?.buckets));
const isFonts = computed(() => props.kind === 'fonts');

const title = computed(() => t(isFonts.value ? 'browse.fontsTitle' : 'browse.title'));
const description = computed(() =>
  t(
    isFonts.value ? 'browse.fontsDescription' : 'browse.description',
    { count: formatCount(total.value, { locale: appLocale.value }) },
    { plural: total.value }
  )
);

const breadcrumb = computed(() => [
  { label: t('site.name'), href: localePath('/') },
  ...(isFonts.value
    ? [{ label: t('browse.crumb'), href: localePath('/browse') }, { label: t('browse.fonts') }]
    : [{ label: t('browse.crumb') }])
]);
useBreadcrumbSchema(breadcrumb);

// CollectionPage → ItemList of the leaf pages, in the order the grid shows them.
useItemListSchema(
  () =>
    buckets.value.map(bucket => {
      const heading = bucketHeading(props.kind, bucket.key);
      return { name: t(heading.key, heading.params), path: localePath(browsePath(props.kind, bucket.key)) };
    }),
  1,
  () => (data.value ? data.value.buckets.length : undefined)
);

// A directory with nothing in it in this language stays out of the index.
useHead(() =>
  data.value && !error.value && !data.value.buckets.length
    ? { meta: [{ name: 'robots', content: 'noindex, follow', tagPriority: 'high' as const }] }
    : {}
);

usePageSeo({
  title: () => t(isFonts.value ? 'browse.fontsMetaTitle' : 'browse.metaTitle'),
  description: () => description.value
});
defineOgImage('OgListSatori', {
  locale: appLocale.value,
  label: t('browse.crumb'),
  title: brandlessTitle(t(isFonts.value ? 'browse.fontsMetaTitle' : 'browse.metaTitle')),
  description: description.value,
  ...useOgFrame()
});

const more = computed(() => [
  ...(isFonts.value ? [{ key: 'browse', label: t('browse.allApps'), href: localePath('/browse') }] : []),
  { key: 'apps', label: t('nav.apps'), href: localePath('/apps') },
  { key: 'categories', label: t('nav.categories'), href: localePath('/categories') },
  { key: 'collections', label: t('nav.collections'), href: localePath('/collections') }
]);
</script>

<template>
  <div class="box-border flex flex-col gap-20px pt-6px">
    <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
    <PageHeader :title="title" :description="data ? description : undefined" />
    <OnErrorState v-if="error" @retry="refresh()" />
    <template v-else-if="data">
      <section v-if="buckets.length" class="flex flex-col gap-10px" aria-labelledby="browse-primary">
        <h2 id="browse-primary" class="m-0 text-15px font-600 text-ink-primary">
          {{ t(isFonts ? 'browse.fonts' : 'browse.apps') }}
        </h2>
        <BrowseBuckets :kind="kind" :buckets="buckets" />
      </section>
      <OnEmpty v-else icon="lucide:inbox" :title="t('browse.empty')" />
      <section v-if="fontBuckets.length" class="flex flex-col gap-10px" aria-labelledby="browse-fonts">
        <div class="flex flex-wrap items-baseline justify-between gap-x-16px gap-y-4px">
          <h2 id="browse-fonts" class="m-0 text-15px font-600 text-ink-primary">{{ t('browse.fonts') }}</h2>
          <NuxtLink
            :to="localePath('/browse/fonts')"
            class="text-13px text-ink-secondary no-underline underline-offset-3 hover:text-ink-primary hover:underline"
            >{{ t('browse.allFonts') }}</NuxtLink
          >
        </div>
        <BrowseBuckets kind="fonts" :buckets="fontBuckets" />
      </section>
    </template>
    <nav class="flex flex-col gap-8px" aria-labelledby="browse-more">
      <h2 id="browse-more" class="m-0 text-13px font-600 text-ink-tertiary">{{ t('browse.more') }}</h2>
      <ul class="m-0 flex list-none flex-wrap gap-x-20px gap-y-8px p-0">
        <li v-for="link in more" :key="link.key">
          <NuxtLink
            :to="link.href"
            class="text-13.5px text-ink-secondary no-underline underline-offset-3 hover:text-ink-primary hover:underline"
            >{{ link.label }}</NuxtLink
          >
        </li>
      </ul>
    </nav>
  </div>
</template>
