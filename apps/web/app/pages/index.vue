<script setup lang="ts">
import { unwrap, type CategoryNode } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';
import { withVariantNames } from '~/utils/seoTitles';

// Homepage introduces OpenNavo (05 §4, 08 §10.14); store Discover is /discover.
// Hero → app store (catalog scale, icon wall, where apps come from) → security (antivirus) → releases/install/
// menu-bar updates → uninstall/cleanup/monitor → more details → download. The store and security follow the
// headline's order: the Homebrew app store, with free antivirus built in.
// Counts/apps come from homepage/rankings APIs; if unavailable retain introduction and use name-only demo apps.
definePageMeta({ layout: 'landing' });

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const formattingLocale = computed(() => toAppLocale(locale.value));

// Homepage data shares Discover's cache key; rankings only feed the icon wall and become null on failure.
const [{ data: home }, { data: ranking }, { data: articles }] = await Promise.all([
  useHome(),
  useAsyncData(
    () => `landing:ranking:${locale.value}`,
    () =>
      unwrap(
        api.GET('/rankings', {
          params: { query: { kind: 'cask', includeFonts: true, period: '30d', size: 24 } }
        })
      ).catch(() => null),
    { transform: withVariantNames }
  ),
  useAsyncData(
    () => `landing:articles:${locale.value}`,
    () => unwrap(api.GET('/articles', { params: { query: { current: 1, size: 3 } } })).catch(() => null)
  )
]);

const count = computed(() => home.value?.stats.casks || null);
// Full sync marks every app updated within 24 hours; hide this count when it exceeds one quarter of the catalog.
const updatedToday = computed(() => {
  const value = home.value?.stats.updatedLast24h ?? 0;
  return count.value && value < count.value / 4 ? value : 0;
});
const visibleCategories = computed(() =>
  (home.value?.categories ?? []).filter(category => !category.hiddenByDefault && category.packageCount > 0)
);

// Keep directory links in SSR HTML, including descendants of the category tree.
function flattenCategories(categories: CategoryNode[]): CategoryNode[] {
  return categories
    .filter(category => !category.hiddenByDefault)
    .flatMap(category => [category, ...flattenCategories(category.children)]);
}
const directoryGroups = computed(() => [
  {
    key: 'categories',
    links: flattenCategories(home.value?.categories ?? [])
      .filter(category => category.packageCount > 0)
      .sort((a, b) => b.packageCount - a.packageCount || a.slug.localeCompare(b.slug))
      .slice(0, 8)
      .map(category => ({ slug: category.slug, title: category.name }))
  },
  { key: 'collections', links: (home.value?.collections ?? []).slice(0, 6) },
  { key: 'articles', links: (articles.value?.records ?? []).slice(0, 3) }
]);

// Hero window/floating icons prefer popular apps.
const showcase = computed(() =>
  pickShowcaseApps([home.value?.popularApps, ranking.value?.records.map(record => record.package)], 8)
);
// Icon wall: sixteen distinct apps fill two rows; repeat only once the wall is visible.
const wall = computed(() =>
  pickShowcaseApps(
    [ranking.value?.records.map(record => record.package), home.value?.popularApps, home.value?.recentlyUpdated],
    16
  )
);
const demoApps = computed(() => withSampleApps(showcase.value.map(toLandingApp), 4));
// Recent apps provide real latest versions for release/menu-bar demos.
const recentApps = computed(() =>
  withSampleApps(pickShowcaseApps([home.value?.recentlyUpdated, home.value?.popularApps], 8).map(toLandingApp), 4)
);

const description = computed(() =>
  count.value
    ? t(
        'site.description',
        { count: formatCount(count.value, { locale: formattingLocale.value }) },
        { plural: count.value }
      )
    : t('landing.hero.ledeFallback').replaceAll('**', '')
);

const root = ref<HTMLElement>();
useRevealOnScroll(root);

useSeoMeta({ title: () => t('site.title'), description: () => description.value });

defineOgImage('OgDefaultSatori', {
  locale: formattingLocale.value,
  variant: 'home',
  title: t('og.homeTitle'),
  description: t('og.homeDescription'),
  ...useOgFrame()
});
</script>

<template>
  <div ref="root" class="landing flex flex-col gap-112px pb-56px md:gap-160px">
    <LandingHero
      :apps="showcase"
      :count="count"
      :categories="visibleCategories.map(({ slug, name }) => ({ slug, name }))"
    />
    <LazyLandingCatalog
      v-if="count && wall.length"
      hydrate-on-visible
      :apps="wall"
      :count="count"
      :updated="updatedToday"
    />
    <!-- C5: real entry points into the catalog's directories. -->
    <div class="mx-auto box-border w-full max-w-1240px px-24px">
      <DirectoryLinks />
      <div class="mt-32px grid gap-24px md:grid-cols-3">
        <section
          v-for="group in directoryGroups.filter(group => group.links.length)"
          :key="group.key"
          :aria-labelledby="`directory-${group.key}`"
        >
          <h2 :id="`directory-${group.key}`" class="m-0 text-headline text-ink-primary">{{ t(`nav.${group.key}`) }}</h2>
          <ul class="m-0 mt-16px flex list-none flex-col gap-12px p-0">
            <li v-for="link in group.links" :key="link.slug">
              <NuxtLink
                :to="localePath(`/${group.key}/${link.slug}`)"
                class="text-ink-secondary underline underline-offset-4 hover:text-ink-primary focus-visible:shadow-focus-ring"
                >{{ link.title }}</NuxtLink
              >
            </li>
          </ul>
        </section>
      </div>
    </div>
    <!-- G4: what OpenNavo is and how it differs from the Homebrew command line and the Mac App Store. -->
    <LandingAbout />
    <!-- Below the fold: server-rendered in full, hydrated only when scrolled into view. -->
    <LazyLandingSecurity hydrate-on-visible />
    <LandingFeature
      :kicker="t('landing.details.kicker')"
      :title="t('landing.details.title')"
      :body="t('landing.details.body')"
      :points="[
        t('landing.details.points.about'),
        t('landing.details.points.deps'),
        t('landing.details.points.languages')
      ]"
    >
      <LandingDemoViewport class="min-h-402px">
        <LazyLandingTimelineDemo :apps="recentApps" />
      </LandingDemoViewport>
    </LandingFeature>
    <LandingFeature
      reverse
      :kicker="t('landing.install.kicker')"
      :title="t('landing.install.title')"
      :body="t('landing.install.body')"
      :points="[
        t('landing.install.points.queue'),
        t('landing.install.points.scan'),
        t('landing.install.points.history')
      ]"
    >
      <LazyLandingInstallDemo v-if="demoApps[0]" hydrate-on-visible :app="demoApps[0]" />
    </LandingFeature>
    <LandingFeature
      :kicker="t('landing.updates.kicker')"
      :title="t('landing.updates.title')"
      :body="t('landing.updates.body')"
      :points="[
        t('landing.updates.points.schedule'),
        t('landing.updates.points.verify'),
        t('landing.updates.points.rollback'),
        t('landing.updates.points.pin')
      ]"
    >
      <LandingDemoViewport class="min-h-341px">
        <LazyLandingTrayDemo :apps="recentApps" />
      </LandingDemoViewport>
    </LandingFeature>
    <LazyLandingToolkit hydrate-on-visible :apps="demoApps" />
    <LazyLandingMore hydrate-on-visible :apps="demoApps" :count="count" />
    <LandingCta />
  </div>
</template>

<style>
/* Shared landing motion (08 §10.14): SSR renders static complete scenes; scripts add pending data-reveal state after mounting. */

[data-reveal='pending'] {
  opacity: 0;
  transform: translateY(28px);
}

[data-reveal='shown'] {
  animation: landing-reveal 0.8s var(--on-motion-easing-emphasized) both;
  animation-delay: var(--landing-delay, 0ms);
}

@keyframes landing-reveal {
  from {
    opacity: 0;
    transform: translateY(28px);
  }
}

/* Slow breathing glow (08 §5: eight-second cycle). */
@keyframes landing-breathe {
  0%,
  100% {
    opacity: 1;
    scale: 1;
  }
  50% {
    opacity: 0.86;
    scale: 1.03;
  }
}

/* Reduced motion: stop loops/translations on a static frame; unlike base.css duration shortening, also remove delays/iterations. */
@media (prefers-reduced-motion: reduce) {
  .landing *,
  .landing *::before,
  .landing *::after {
    animation: none !important;
    transition-duration: 0.01ms !important;
  }
}
</style>
