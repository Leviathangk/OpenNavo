<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { formatDate } from '@opennavo/shared';
import { OnButton, OnCollectionView, OnErrorState, OnIcon } from '@opennavo/ui';
import type { OnCollectionEntry } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';

// Collection details (05 §4, §7.2): shared OnCollectionView (ADR-017), title/body, add all, Brewfile export
// by direct API text download, and items preferring recommendations over summaries.
const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const config = useRuntimeConfig();
const localePath = useLocalePath();
const toasts = useToasts();
const brewfile = useBrewfileList();
const NuxtLink = resolveComponent('NuxtLink');
const appLocale = computed(() => toAppLocale(locale.value));

const slug = computed(() => String(route.params.slug ?? ''));

const { data, error, refresh } = await useAsyncData(
  () => `collection:${slug.value}:${locale.value}`,
  () =>
    unwrap(api.GET('/collections/{slug}', { params: { path: { slug: slug.value } } })).catch((reason: unknown) => {
      if (reason instanceof ApiError && (reason.isNotFound || reason.status === 404))
        throw createError({ statusCode: 404, statusMessage: 'Collection not found' });
      throw reason;
    })
);

if (error.value?.statusCode === 404)
  throw createError({ statusCode: 404, statusMessage: 'Collection not found', fatal: true });

const entries = computed<OnCollectionEntry[]>(() =>
  (data.value?.items ?? []).map(item => ({
    key: `${item.package.kind}/${item.package.token}`,
    kind: item.package.kind,
    token: item.package.token,
    name: item.package.displayName,
    src: item.package.iconUrl,
    accent: item.package.accentColor,
    description: item.note ?? item.package.summary,
    href: localePath(packagePath(item.package.token))
  }))
);
const meta = computed(() =>
  data.value
    ? `${t('collections.count', { count: data.value.itemCount }, { plural: data.value.itemCount })} · ${t('collections.updated', { date: formatDate(data.value.updatedAt, { locale: appLocale.value }) })}`
    : undefined
);

// Browser downloads API-generated Brewfile text directly.
const exportHref = computed(() => `${config.public.apiBase}/collections/${slug.value}/brewfile`);

function addAll() {
  const items = data.value?.items ?? [];
  let added = 0;
  for (const item of items) if (brewfile.add(item.package.kind, item.package.token) === 'added') added += 1;
  toasts.push({ tone: 'success', title: t('collections.addedAll', { count: added }, { plural: added }) });
}

const crumbs = computed(() => [
  { label: t('collections.title'), href: localePath('/collections') },
  { label: data.value?.title ?? slug.value }
]);
useBreadcrumbSchema(crumbs);
// Share image (WP13): the collection beside its first apps.
if (data.value)
  defineOgImage('OgListSatori', {
    locale: appLocale.value,
    label: t('og.collection'),
    title: data.value.title,
    description: data.value.subtitle ?? undefined,
    icons: data.value.items.slice(0, 4).map(item => ({
      name: item.package.displayName,
      token: item.package.token,
      src: item.package.iconUrl,
      accent: item.package.accentColor
    })),
    ...useOgFrame()
  });
useItemListSchema(() => entries.value.map(entry => ({ name: entry.name, path: entry.href })));

usePageSeo({
  title: () => t('collections.itemMetaTitle', { title: data.value?.title ?? '' }),
  description: () => data.value?.subtitle ?? t('collections.description')
});
</script>

<template>
  <OnCollectionView
    :breadcrumb="crumbs"
    :title="data?.title ?? null"
    :subtitle="data?.subtitle"
    :meta="meta"
    :entries="entries"
    :link-as="NuxtLink"
  >
    <OnErrorState v-if="error && !data" @retry="refresh()" />
    <template #actions>
      <OnButton variant="primary" shape="round" icon="plus" @click="addAll">{{ t('collections.addAll') }}</OnButton>
      <OnButton shape="round" :href="exportHref" download="Brewfile">
        <OnIcon name="download" :size="15" />
        {{ t('collections.export') }}
      </OnButton>
    </template>
    <template v-if="data?.body" #body>
      <MarkdownContent size="md" :heading-level="2" :source="data.body" />
    </template>
    <template #action="{ entry }">
      <GetMenu :kind="entry.kind" :token="entry.token" :name="entry.name" />
    </template>
  </OnCollectionView>
</template>
