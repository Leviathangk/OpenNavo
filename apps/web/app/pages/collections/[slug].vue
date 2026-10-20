<script setup lang="ts">
import { ApiError, unwrap } from '@opennavo/api';
import { buildDeepLink, formatDate, isInstallableKind, LOCALE_METADATA } from '@opennavo/shared';
import { OnButton, OnChip, OnCollectionView, OnErrorState, OnIcon, useUiMessages } from '@opennavo/ui';
import type { OnCollectionEntry } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { launchDeepLink } from '~/utils/deepLink';
import { packagePath } from '~/utils/packageView';

// Collection details (05 §4, §7.2): shared OnCollectionView (ADR-017), title/body, add all, Brewfile export
// by direct API text download, and items preferring recommendations over summaries. F4 turns the collection
// into a setup: its recommendation criteria above the list, then what "Install all with OpenNavo" installs,
// the link apps to download from their developers, price totals and compatibility (CollectionSetupSections).
// F6: the collection's update feed is linked in the head and on the page.
const { t, locale } = useI18n();
const ui = useUiMessages();
const switchLocalePath = useSwitchLocalePath();
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
const linkOf = (token: string) => data.value?.items.find(item => item.package.token === token)?.package.link ?? null;
const meta = computed(() =>
  data.value
    ? `${t('collections.count', { count: data.value.itemCount }, { plural: data.value.itemCount })} · ${t('collections.updated', { date: formatDate(data.value.updatedAt, { locale: appLocale.value }) })}`
    : undefined
);

// Browser downloads API-generated Brewfile text directly; it lists the same casks as setup.installableTokens.
const exportHref = computed(() => `${config.public.apiBase}/collections/${slug.value}/brewfile`);
const installable = computed(() => data.value?.setup?.installableTokens ?? null);

function addAll() {
  let added = 0;
  // F4: the casks the Brewfile holds; link apps have no brew install, so they never enter the list.
  const items = installable.value
    ? installable.value.map(token => ({ kind: 'cask' as const, token }))
    : (data.value?.items ?? []).flatMap(item =>
        isInstallableKind(item.package.kind) ? [{ kind: item.package.kind, token: item.package.token }] : []
      );
  for (const item of items) if (brewfile.add(item.kind, item.token) === 'added') added += 1;
  toasts.push({ tone: 'success', title: t('collections.addedAll', { count: added }, { plural: added }) });
}

// Install all (F4): the client opens this collection and installs its casks after the visitor confirms
// (opennavo://collection/{slug}?action=install, as useOpenInApp does for one app). If the page never loses
// focus the client is not installed; the Brewfile stays available.
const DETECT_MS = 1500;
function installAll() {
  trackEvent('open_in_app');
  let left = false;
  const mark = () => {
    left = true;
  };
  window.addEventListener('blur', mark, { once: true });
  document.addEventListener('visibilitychange', mark, { once: true });
  launchDeepLink(buildDeepLink({ type: 'collection', slug: slug.value, action: 'install' }));
  setTimeout(() => {
    window.removeEventListener('blur', mark);
    document.removeEventListener('visibilitychange', mark);
    if (!left)
      toasts.push({
        tone: 'warning',
        title: t('collections.setup.notDetected'),
        description: t('collections.setup.notDetectedBody'),
        duration: 0
      });
  }, DETECT_MS);
}

// S8: text Hermes translated says so, with a link to the collection in the language it was written in.
const original = computed(() => {
  const value = data.value;
  if (!value?.machineTranslated || !value.sourceLocale || value.sourceLocale === toAppLocale(locale.value)) return null;
  const href = switchLocalePath(toRouteLocale(value.sourceLocale));
  return href ? { href, lang: LOCALE_METADATA[value.sourceLocale].hreflang } : null;
});

// F6: the collection's release feed (server/handlers/collectionFeed.ts).
const feedHref = useReleaseFeed(() =>
  data.value
    ? { section: 'collections', key: slug.value, title: t('feeds.collectionTitle', { title: data.value.title }) }
    : null
);

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
  <div class="flex flex-col gap-32px">
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
        <OnButton v-if="installable?.length" variant="primary" shape="round" icon="download" @click="installAll">{{
          t('collections.setup.installAll')
        }}</OnButton>
        <OnButton :variant="installable?.length ? 'secondary' : 'primary'" shape="round" icon="plus" @click="addAll">{{
          t('collections.addAll')
        }}</OnButton>
        <OnButton shape="round" :href="exportHref" download="Brewfile">
          <OnIcon name="download" :size="15" />
          {{ t('collections.export') }}
        </OnButton>
      </template>
      <template v-if="data?.body || data?.setup?.criteria || original" #body>
        <div class="flex flex-col gap-24px">
          <p v-if="original" class="m-0 inline-flex items-center gap-8px text-12px text-ink-tertiary">
            <OnChip tone="outline">{{ ui.about.machineTranslated }}</OnChip>
            <NuxtLink :to="original.href" :hreflang="original.lang" :lang="original.lang" class="text-ink-secondary">{{
              ui.about.viewOriginal
            }}</NuxtLink>
          </p>
          <MarkdownContent v-if="data?.body" size="md" :heading-level="2" :source="data.body" />
          <CollectionCriteria v-if="data?.setup?.criteria" :criteria="data.setup.criteria" />
        </div>
      </template>
      <template #action="{ entry }">
        <GetMenu :kind="entry.kind" :token="entry.token" :name="entry.name" :link="linkOf(entry.token)" />
      </template>
    </OnCollectionView>
    <template v-if="data">
      <CollectionSetupSections :detail="data" :brewfile-href="exportHref" @install="installAll" />
      <p v-if="feedHref" class="m-0">
        <a
          :href="feedHref"
          type="application/rss+xml"
          class="inline-flex items-center gap-6px text-13px text-ink-secondary no-underline hover:text-ink-primary hover:underline"
          ><OnIcon name="rss" :size="14" />{{ t('feeds.subscribe') }}</a
        >
      </p>
    </template>
  </div>
</template>
