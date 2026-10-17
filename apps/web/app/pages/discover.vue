<script setup lang="ts">
import { withRecentReturn } from '@opennavo/shared';
import type { PackageSummary, PublicComponents } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { collectionIcons, OnAppCard, OnButton, OnDiscoverView, OnErrorState, OnIcon } from '@opennavo/ui';
import type {
  OnDiscoverCollection,
  OnDiscoverFeature,
  OnDiscoverHero,
  OnDiscoverLabels,
  OnFeatureHeroIcon
} from '@opennavo/ui';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

type Feature = PublicComponents['schemas']['Feature'];

// Discover (05 §4, §11.2, 08 §10.1): shared desktop view at /discover (root is landing); title is screen-reader-only.
// SiteDiscoverRail contains weekly popular/featured collections. Cask-only (ADR-018).
definePageMeta({ rail: 'discover' });

const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const [{ data: home, error, refresh }, { data: popularApps }, { data: recentApps }] = await Promise.all([
  useHome(),
  usePopularApps(),
  useRecentPreview()
]);

const detailPath = (token: string) => localePath(`/apps/${token}`);

const toIcon = (pkg: PackageSummary): OnFeatureHeroIcon => ({
  kind: pkg.kind,
  token: pkg.token,
  name: pkg.displayName,
  src: pkg.iconUrl,
  accent: pkg.accentColor
});

const hero = computed(
  () => home.value?.features.find(item => item.placement === 'home_hero') ?? home.value?.features[0]
);

// Large icon is featured; small icons are the first two from other features/popular apps.
const heroIcons = computed<OnFeatureHeroIcon[]>(() => {
  const main = hero.value?.package;
  const pool = [
    ...(home.value?.features ?? []).flatMap(item => (item.package && item !== hero.value ? [item.package] : [])),
    ...(home.value?.popularApps ?? [])
  ];
  const extras: PackageSummary[] = [];
  for (const pkg of pool) {
    if (extras.length === 2) break;
    if (pkg.token === main?.token || extras.some(item => item.token === pkg.token)) continue;
    extras.push(pkg);
  }
  return [...(main ? [main] : []), ...extras].map(toIcon);
});

const heroView = computed<OnDiscoverHero | null>(() =>
  hero.value
    ? {
        badge: hero.value.badge ?? t('home.featured'),
        title: hero.value.title,
        subtitle: hero.value.subtitle,
        description: hero.value.body,
        icons: heroIcons.value,
        glowColor: hero.value.glowColor,
        href: featureHref(hero.value)?.href
      }
    : null
);

function featureHref(feature: Feature): { href: string; external: boolean } | null {
  const { target } = feature;
  if (target.type === 'package' && (target.kind === 'cask' || target.kind === 'link') && target.token)
    return { href: detailPath(target.token), external: false };
  if (target.type === 'collection' && target.slug)
    return { href: localePath(`/collections/${target.slug}`), external: false };
  if (target.type === 'url' && target.url) return { href: target.url, external: true };
  return null;
}

const heroLink = computed(() => (hero.value ? featureHref(hero.value) : null));

const secondaryFeatures = computed<OnDiscoverFeature[]>(() =>
  (home.value?.features ?? [])
    .filter(item => item.placement === 'home_secondary' && item.id !== hero.value?.id)
    .map(item => {
      const link = featureHref(item);
      return {
        key: item.id,
        badge: item.badge ?? t('home.featured'),
        title: item.title,
        subtitle: item.subtitle,
        description: item.body,
        icons: item.package ? [toIcon(item.package)] : collectionIconsForFeature(item),
        glowColor: item.glowColor,
        href: link?.href,
        external: link?.external,
        detailLabel: item.ctaLabel ?? t('home.viewDetails')
      };
    })
);

function collectionIconsForFeature(feature: Feature) {
  if (feature.target.type !== 'collection') return [];
  const slug = feature.target.slug;
  const collection = home.value?.collections.find(item => item.slug === slug);
  return collection ? collectionIcons(collection) : [];
}

const chips = computed(() => [
  { value: 'all', label: t('home.allCategories'), href: localePath('/discover') },
  ...(home.value?.categories ?? [])
    .filter(category => !category.hiddenByDefault)
    .map(category => ({ value: category.slug, label: category.name, href: localePath(`/categories/${category.slug}`) }))
]);

const collections = computed<OnDiscoverCollection[]>(() =>
  (home.value?.collections ?? []).map(collection => ({
    key: collection.slug,
    title: collection.title,
    subtitle: collection.subtitle,
    count: collection.itemCount,
    icons: collectionIcons(collection),
    href: localePath(`/collections/${collection.slug}`)
  }))
);

const labels = computed<OnDiscoverLabels>(() => ({
  categories: t('home.categoriesLabel'),
  popularApps: t('home.popularApps'),
  popularAppsHint: t('home.popularAppsHint'),
  viewRankings: t('home.viewRankings'),
  recentlyUpdated: t('home.recentlyUpdated'),
  recentlyUpdatedHint: t('home.recentlyUpdatedHint'),
  collections: t('home.collections'),
  viewAll: t('home.viewAll')
}));

const total = computed(() => home.value?.stats.casks ?? 0);

// The home page describes the site (site.description); Discover describes the catalog it shows.
useSeoMeta({
  title: () => t('home.metaTitle'),
  description: () =>
    t(
      'home.introBody',
      { count: formatCount(total.value, { locale: formattingLocale.value }) },
      { plural: total.value }
    )
});

defineOgImage('OgDefaultSatori', {
  locale: formattingLocale.value,
  variant: 'discover',
  title: t('og.discoverTitle'),
  description: t('og.discoverDescription'),
  ...useOgFrame(),
  icons: pickShowcaseApps([home.value?.popularApps, home.value?.recentlyUpdated], 4).map(pkg => ({
    name: pkg.displayName,
    token: pkg.token,
    src: pkg.iconUrl,
    accent: pkg.accentColor
  }))
});
</script>

<template>
  <h1 class="sr-only">{{ t('home.discoverHeading') }}</h1>
  <OnDiscoverView
    v-if="home"
    :hero="heroView"
    :features="secondaryFeatures"
    :chips="chips"
    :popular="popularApps ?? home.popularApps"
    :popular-rows="3"
    :recent="recentApps ?? home.recentlyUpdated"
    :collections="collections"
    :labels="labels"
    :rankings-href="localePath('/rankings')"
    :recent-href="localePath('/recent')"
    :collections-href="localePath('/collections')"
    :link-as="NuxtLink"
  >
    <template v-if="hero" #heroActions>
      <GetMenu
        v-if="hero.package"
        variant="primary"
        :kind="hero.package.kind"
        :token="hero.package.token"
        :name="hero.package.displayName"
        :link="hero.package.link"
      />
      <OnButton
        v-if="heroLink"
        variant="secondary"
        shape="round"
        :href="heroLink.href"
        :link-as="heroLink.external ? 'a' : NuxtLink"
        :target="heroLink.external ? '_blank' : undefined"
        :rel="heroLink.external ? 'noopener noreferrer' : undefined"
      >
        {{ hero.ctaLabel ?? t('home.viewDetails') }}
        <OnIcon name="arrow-up-right" :size="15" />
      </OnButton>
    </template>
    <template #card="{ pkg, stats, section }">
      <OnAppCard
        :pkg="pkg"
        state="get"
        :stats="stats"
        :href="withRecentReturn(detailPath(pkg.token), section === 'recent' ? '/recent' : undefined)"
        :link-as="NuxtLink"
      >
        <template #action>
          <GetMenu :kind="pkg.kind" :token="pkg.token" :name="pkg.displayName" :link="pkg.link" />
        </template>
      </OnAppCard>
    </template>
  </OnDiscoverView>
  <div v-else-if="error" class="py-64px">
    <OnErrorState @retry="refresh()" />
  </div>
</template>
