<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import type { Feature, PackageSummary } from '@opennavo/api';
import { collectionIcons, OnButton, OnDiscoverView, OnIcon, OnEmpty } from '@opennavo/ui';
import type {
  OnDiscoverCollection,
  OnDiscoverFeature,
  OnDiscoverHero,
  OnDiscoverLabels,
  OnFeatureHeroIcon
} from '@opennavo/ui';
import OfflineNotice from '@/components/common/OfflineNotice.vue';
import PackageCard from '@/components/package/PackageCard.vue';
import { commands, unwrap } from '@/ipc/client';
import type { ListQuery } from '@/ipc/bindings';
import type { LocalItem } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { refreshPage } from '@/composables/usePageRefresh';
import { fetchHome, fetchPopularApps, peekHome, peekPopularApps } from '@/composables/useHome';
import { useLoader } from '@/composables/useLoader';
import { useCatalogLoader } from '@/composables/useCatalogLoader';
import { usePackageSummary } from '@/composables/usePackageSummary';
import { useCatalogStore } from '@/stores';

// Discover (mockup 02, 06 §13): online API homepage; offline popular apps and recent updates from the local catalog. Cask-only (ADR-018).
const { t } = useI18n();
const { appLocale } = useAppLocale();
const { toSummary } = usePackageSummary();
const catalog = useCatalogStore();

const {
  data: home,
  error: homeError,
  loading: homeLoading
} = useLoader(
  () => fetchHome(appLocale.value),
  [appLocale],
  undefined,
  [appLocale],
  () => peekHome(appLocale.value)
);

const { data: popularApps } = useLoader(
  () => fetchPopularApps(appLocale.value),
  [appLocale],
  undefined,
  [appLocale],
  () => peekPopularApps(appLocale.value)
);

const base: Omit<ListQuery, 'sort' | 'limit'> = {
  kind: 'cask',
  category: null,
  includeFonts: true,
  includeLibraries: false,
  includeDisabled: false,
  offset: 0
};

async function localList(query: Pick<ListQuery, 'sort' | 'limit'>): Promise<LocalItem[]> {
  const page = await unwrap(commands.catalogList({ ...base, ...query }));
  return page.items;
}

// Two offline sections; stop reading them after an online request succeeds.
const { data: offline, error: offlineError } = useCatalogLoader(
  async () => {
    if (home.value) return null;
    const [apps, recent] = await Promise.all([
      localList({ sort: 'installs30d', limit: 30 }),
      localList({ sort: 'updated', limit: 8 })
    ]);
    return { apps, recent };
  },
  [home, appLocale],
  result => (result ? [...result.apps, ...result.recent] : [])
);

const sections = computed(() => {
  if (home.value)
    return {
      apps: popularApps.value ?? home.value.popularApps,
      recent: home.value.recentlyUpdated
    };
  return offline.value
    ? { apps: offline.value.apps.map(toSummary), recent: offline.value.recent.map(toSummary) }
    : null;
});

const hero = computed(
  () => home.value?.features.find(item => item.placement === 'home_hero') ?? home.value?.features[0]
);

const toIcon = (pkg: PackageSummary): OnFeatureHeroIcon => ({
  kind: pkg.kind,
  token: pkg.token,
  name: pkg.displayName,
  src: pkg.iconUrl,
  accent: pkg.accentColor
});

// Large icon is the featured package; small icons are the first two from other featured slots and popular apps.
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

function featureLink(feature: Feature): string | null {
  const { target } = feature;
  if (target.type === 'package' && target.kind === 'cask' && target.token) return `/package/cask/${target.token}`;
  if (target.type === 'collection' && target.slug) return `/collections/${target.slug}`;
  if (target.type === 'url' && target.url) return target.url;
  return null;
}

const secondaryFeatures = computed<OnDiscoverFeature[]>(() =>
  (home.value?.features ?? [])
    .filter(item => item.placement === 'home_secondary' && item.id !== hero.value?.id)
    .map(item => ({
      key: item.id,
      badge: item.badge ?? t('discover.featured'),
      title: item.title,
      subtitle: item.subtitle,
      description: item.body,
      icons: item.package ? [toIcon(item.package)] : collectionIconsForFeature(item),
      glowColor: item.glowColor,
      href: featureLink(item) ?? undefined,
      external: item.target.type === 'url',
      detailLabel: item.ctaLabel ?? t('discover.viewDetails')
    }))
);

const heroView = computed<OnDiscoverHero | null>(() =>
  hero.value
    ? {
        badge: hero.value.badge ?? t('discover.featured'),
        title: hero.value.title,
        subtitle: hero.value.subtitle,
        description: hero.value.body,
        icons: heroIcons.value,
        glowColor: hero.value.glowColor,
        href: featureLink(hero.value) ?? undefined
      }
    : null
);

function collectionIconsForFeature(feature: Feature) {
  if (feature.target.type !== 'collection') return [];
  const slug = feature.target.slug;
  const collection = home.value?.collections.find(item => item.slug === slug);
  return collection ? collectionIcons(collection) : [];
}

const collections = computed<OnDiscoverCollection[]>(() =>
  (home.value?.collections ?? []).map(collection => ({
    key: collection.slug,
    title: collection.title,
    subtitle: collection.subtitle,
    count: collection.itemCount,
    icons: collectionIcons(collection),
    href: `/collections/${collection.slug}`
  }))
);

const labels = computed<OnDiscoverLabels>(() => ({
  categories: t('discover.categoriesLabel'),
  popularApps: t('discover.popularApps'),
  popularAppsHint: t('discover.popularAppsHint'),
  viewRankings: t('common.viewRankings'),
  recentlyUpdated: t('discover.recentlyUpdated'),
  recentlyUpdatedHint: t('discover.recentlyUpdatedHint'),
  collections: t('discover.collections'),
  viewAll: t('common.viewAll')
}));

const chips = computed(() => [
  { value: 'all', label: t('discover.allCategories'), href: '/discover' },
  ...catalog.categories
    .filter(category => !category.parent && !category.hiddenByDefault)
    .map(category => ({
      value: category.slug,
      label: category.name,
      href: `/categories/${category.slug}`
    }))
]);
</script>

<template>
  <OnDiscoverView
    :hero="heroView"
    :features="secondaryFeatures"
    :chips="chips"
    :popular="sections?.apps ?? (offlineError ? [] : null)"
    :popular-rows="3"
    :recent="sections?.recent ?? (offlineError ? [] : null)"
    :collections="collections"
    :labels="labels"
    rankings-href="/rankings"
    recent-href="/recent"
    collections-href="/collections"
    :link-as="RouterLink"
  >
    <template #notice>
      <OfflineNotice v-if="homeError && !home" />
      <OnEmpty
        v-if="
          !homeLoading &&
          sections &&
          !sections.apps.length &&
          !sections.recent.length &&
          !hero &&
          !secondaryFeatures.length &&
          !collections.length
        "
        :title="t('common.empty')"
      >
        <template #actions
          ><OnButton variant="secondary" @click="refreshPage()">{{ t('common.retry') }}</OnButton></template
        >
      </OnEmpty>
    </template>
    <template v-if="hero" #heroActions>
      <OnButton
        v-if="featureLink(hero)"
        variant="secondary"
        shape="round"
        :href="featureLink(hero) ?? undefined"
        :link-as="hero.target.type === 'url' ? 'a' : RouterLink"
        :target="hero.target.type === 'url' ? '_blank' : undefined"
        :rel="hero.target.type === 'url' ? 'noopener noreferrer' : undefined"
      >
        {{ hero.ctaLabel ?? t('discover.viewDetails') }}
        <OnIcon name="arrow-up-right" :size="15" />
      </OnButton>
    </template>
    <template #card="{ pkg, stats }">
      <PackageCard :pkg="pkg" :stats="stats" />
    </template>
  </OnDiscoverView>
</template>
