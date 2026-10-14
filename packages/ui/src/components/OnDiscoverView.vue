<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import type { Component } from 'vue';
import type { PackageSummary } from '@opennavo/api';
import OnCategoryChips from './OnCategoryChips.vue';
import type { OnCategoryChip } from './OnCategoryChips.vue';
import OnCollectionCard from './OnCollectionCard.vue';
import type { OnCollectionIcon } from './OnCollectionCard.vue';
import OnFeatureHero from './OnFeatureHero.vue';
import type { OnFeatureHeroIcon } from './OnFeatureHero.vue';
import OnSectionHeader from './OnSectionHeader.vue';
import OnSkeleton from './OnSkeleton.vue';
import type { OnAppCardStat } from './OnAppCard.vue';
import OnAppIcon from './OnAppIcon.vue';
import OnHighlightText from './OnHighlightText.vue';
import OnIcon from './OnIcon.vue';

// Shared Discover presentation (ADR-017): compact primary/secondary picks, categories, apps, and collections.
// Host supplies data and card slots, with local-state desktop Get buttons or web open/copy/download menus.
export interface OnDiscoverHero {
  badge: string;
  title: string;
  subtitle?: string | null;
  description?: string | null;
  icons: readonly OnFeatureHeroIcon[];
  glowColor?: string | null;
  /** Primary destination, used to avoid repeating the same collection among secondary picks. */
  href?: string;
}

export interface OnDiscoverCollection {
  key: string;
  title: string;
  subtitle?: string | null;
  count: number;
  icons: readonly OnCollectionIcon[];
  href: string;
}

export interface OnDiscoverFeature extends Omit<OnDiscoverHero, 'icons'> {
  key: number | string;
  icons: readonly OnCollectionIcon[];
  href?: string;
  external?: boolean;
  detailLabel: string;
}

export interface OnDiscoverLabels {
  categories: string;
  popularApps: string;
  popularAppsHint: string;
  viewRankings: string;
  recentlyUpdated: string;
  recentlyUpdatedHint: string;
  collections: string;
  viewAll: string;
}

export interface OnDiscoverViewProps {
  hero?: OnDiscoverHero | null;
  features?: readonly OnDiscoverFeature[];
  chips: readonly OnCategoryChip<string>[];
  /** null means loading; show skeletons. */
  popular: readonly PackageSummary[] | null;
  /** Fit popular-app rows to the available width, with a partial final row; omitted keeps the six-app preview. */
  popularRows?: number;
  recent: readonly PackageSummary[] | null;
  collections?: readonly OnDiscoverCollection[];
  labels: OnDiscoverLabels;
  rankingsHref: string;
  /** Full recently updated list behind the section's See All link. */
  recentHref: string;
  collectionsHref: string;
  linkAs?: string | Component;
}

const props = withDefaults(defineProps<OnDiscoverViewProps>(), {
  features: () => [],
  collections: () => [],
  linkAs: 'a'
});

defineSlots<{
  /** Environment notice before hero, e.g. desktop offline state. */
  notice?: () => unknown;
  heroActions?: () => unknown;
  card: (props: { pkg: PackageSummary; section: 'popular' | 'recent'; stats?: readonly OnAppCardStat[] }) => unknown;
}>();

const RECENT_STATS: readonly OnAppCardStat[] = ['version', 'installs30d'];
const appGrid = 'grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-12px';
const root = ref<HTMLElement>();
const contentWidth = ref(0);
const popularLimit = computed(() => {
  if (!props.popularRows) return 6;
  const columns = Math.max(1, Math.floor((contentWidth.value + 12) / (220 + 12)));
  const lastRow = columns === 1 ? 1 : Math.min(columns - 1, Math.ceil(columns * 0.6));
  return columns * (props.popularRows - 1) + lastRow;
});
let observer: ResizeObserver | undefined;
let resizeFrame = 0;

onMounted(() => {
  if (!props.popularRows || !root.value) return;
  contentWidth.value = root.value.clientWidth;
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(([entry]) => {
      if (!entry || entry.contentRect.width === contentWidth.value) return;
      const width = entry.contentRect.width;
      cancelAnimationFrame(resizeFrame);
      // Changing the card count changes height; apply it after observer delivery to avoid resize loops.
      resizeFrame = requestAnimationFrame(() => {
        contentWidth.value = width;
      });
    });
    observer.observe(root.value);
  }
});
onBeforeUnmount(() => {
  observer?.disconnect();
  cancelAnimationFrame(resizeFrame);
});

// Retain editorial order and fill vacant slots with real collections, without repeating destinations.
const secondaryPicks = computed<OnDiscoverFeature[]>(() => {
  const picks: OnDiscoverFeature[] = [];
  const destinations = new Set(props.hero?.href ? [props.hero.href] : []);
  for (const feature of props.features) {
    if (feature.href && destinations.has(feature.href)) continue;
    picks.push(feature);
    if (feature.href) destinations.add(feature.href);
    if (picks.length === 3) return picks;
  }
  if (!props.hero && !picks.length) return picks;
  for (const collection of props.collections) {
    if (destinations.has(collection.href)) continue;
    picks.push({
      key: `collection:${collection.key}`,
      badge: '',
      title: collection.title,
      subtitle: collection.subtitle,
      icons: collection.icons,
      href: collection.href,
      detailLabel: props.labels.viewAll
    });
    destinations.add(collection.href);
    if (picks.length === 3) break;
  }
  return picks;
});

function featureLinkAttrs(feature: OnDiscoverFeature) {
  if (!feature.href) return {};
  return {
    ...(feature.external || props.linkAs === 'a' ? { href: feature.href } : { to: feature.href }),
    target: feature.external ? '_blank' : undefined,
    rel: feature.external ? 'noopener noreferrer' : undefined
  };
}
</script>

<template>
  <div ref="root" class="on-discover-view flex flex-col gap-24px pt-8px">
    <slot name="notice" />

    <section v-if="hero || secondaryPicks.length">
      <OnSectionHeader
        :title="hero?.badge || secondaryPicks[0]?.badge || labels.collections"
        :more-label="labels.viewAll"
        :more-href="collectionsHref"
        :link-as="linkAs"
      />
      <div
        class="on-discover-picks grid min-w-0 gap-10px"
        :class="{ 'on-discover-picks-split': hero && secondaryPicks.length }"
      >
        <OnFeatureHero
          v-if="hero"
          compact
          :title="hero.title"
          :subtitle="hero.subtitle ?? undefined"
          :description="hero.description ?? undefined"
          :icons="hero.icons"
          :glow-color="hero.glowColor"
          :heading-level="3"
        >
          <template #actions>
            <slot name="heroActions" />
          </template>
        </OnFeatureHero>

        <div v-if="secondaryPicks.length" class="grid min-w-0 content-start gap-10px">
          <component
            :is="feature.href ? (feature.external ? 'a' : linkAs) : 'article'"
            v-for="feature in secondaryPicks"
            :key="feature.key"
            v-bind="featureLinkAttrs(feature)"
            class="on-discover-feature box-border flex min-w-0 items-center gap-12px rounded-big border border-solid border-line-subtle bg-surface-card p-14px text-ink-primary no-underline outline-none"
            :class="{
              'on-discover-feature-link hover:bg-surface-raised focus-visible:shadow-focus-ring': feature.href
            }"
          >
            <OnAppIcon
              v-if="feature.icons[0]?.kind && feature.icons[0]?.token && feature.icons[0]?.name"
              :kind="feature.icons[0].kind"
              :token="feature.icons[0].token"
              :name="feature.icons[0].name"
              :src="feature.icons[0].src"
              :accent="feature.icons[0].accent"
              :size="40"
            />
            <img
              v-else-if="feature.icons[0]?.src"
              :src="feature.icons[0].src"
              alt=""
              width="40"
              height="40"
              loading="lazy"
              class="h-40px w-40px shrink-0 rounded-app-icon object-cover"
            />
            <span
              v-else
              class="grid h-40px w-40px shrink-0 place-items-center rounded-app-icon bg-surface-raised text-ink-secondary"
              aria-hidden="true"
            >
              <OnIcon name="library" :size="22" />
            </span>
            <div class="min-w-0 flex-1">
              <h3 class="m-0 truncate text-15px font-600" :title="feature.title">{{ feature.title }}</h3>
              <OnHighlightText
                v-if="feature.subtitle || feature.description"
                as="p"
                :text="feature.subtitle || feature.description || ''"
                class="m-0 mt-4px truncate text-12px leading-[1.5] text-ink-secondary"
              />
            </div>
            <OnIcon
              v-if="feature.href"
              :name="feature.external ? 'arrow-up-right' : 'chevron-right'"
              :size="16"
              class="shrink-0 text-ink-secondary"
            />
          </component>
        </div>
      </div>
    </section>

    <OnCategoryChips
      v-if="chips.length > 1"
      :model-value="chips[0]?.value ?? 'all'"
      :items="chips"
      :aria-label="labels.categories"
      :link-as="linkAs"
    />

    <template v-if="popular && recent">
      <section v-if="popular.length">
        <OnSectionHeader
          :title="labels.popularApps"
          :hint="labels.popularAppsHint"
          :more-label="labels.viewRankings"
          :more-href="rankingsHref"
          :link-as="linkAs"
        />
        <div :class="appGrid">
          <template v-for="pkg in popular.slice(0, popularLimit)" :key="`${pkg.kind}/${pkg.token}`">
            <slot name="card" :pkg="pkg" section="popular" :stats="RECENT_STATS" />
          </template>
        </div>
      </section>

      <section v-if="recent.length">
        <OnSectionHeader
          :title="labels.recentlyUpdated"
          :hint="labels.recentlyUpdatedHint"
          :more-label="labels.viewAll"
          :more-href="recentHref"
          :link-as="linkAs"
        />
        <div :class="appGrid">
          <template v-for="pkg in recent.slice(0, 8)" :key="`${pkg.kind}/${pkg.token}`">
            <slot name="card" :pkg="pkg" section="recent" :stats="RECENT_STATS" />
          </template>
        </div>
      </section>
    </template>
    <div v-else :class="appGrid" aria-hidden="true">
      <OnSkeleton v-for="index in popularLimit" :key="index" height="148px" radius="big" />
    </div>

    <section v-if="collections.length">
      <OnSectionHeader
        :title="labels.collections"
        :more-label="labels.viewAll"
        :more-href="collectionsHref"
        :link-as="linkAs"
      />
      <div class="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-12px">
        <OnCollectionCard
          v-for="collection in collections.slice(0, 6)"
          :key="collection.key"
          :title="collection.title"
          :subtitle="collection.subtitle"
          :count="collection.count"
          :icons="collection.icons"
          :href="collection.href"
          :link-as="linkAs"
        />
      </div>
    </section>
  </div>
</template>

<style scoped>
.on-discover-view {
  container-type: inline-size;
}

.on-discover-feature {
  min-height: 74px;
}

.on-discover-feature-link {
  transition: background-color var(--on-motion-duration-fast) var(--on-motion-easing-standard);
}

/* Use the available content width: desktop sidebars can leave little room even in a wide window. */
@container (min-width: 640px) {
  .on-discover-picks-split {
    grid-template-columns: minmax(0, 1.65fr) minmax(240px, 1fr);
  }
}
</style>
