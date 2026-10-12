<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount } from '@opennavo/shared';

// Homepage introduces OpenNavo (05 §4, 08 §10.14); store Discover is /discover.
// Hero → catalog scale/icon wall → releases/install/menu-bar updates → more details → download.
// Counts/apps come from homepage/rankings APIs; if unavailable retain introduction and use name-only demo apps.
definePageMeta({ layout: 'landing' });

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const formattingLocale = computed(() => toAppLocale(locale.value));

// Homepage data shares Discover's cache key; rankings only feed the icon wall and become null on failure.
const [{ data: home }, { data: ranking }] = await Promise.all([
  useHome(),
  useAsyncData(
    () => `landing:ranking:${locale.value}`,
    () =>
      unwrap(
        api.GET('/rankings', {
          params: { query: { kind: 'cask', includeFonts: true, period: '30d', size: 24 } }
        })
      ).catch(() => null)
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

// Hero window/floating icons prefer popular apps.
const showcase = computed(() =>
  pickShowcaseApps([home.value?.popularApps, ranking.value?.records.map(record => record.package)], 8)
);
// Icon wall: top 24 in 30-day rankings plus homepage popular/recent apps.
const wall = computed(() =>
  pickShowcaseApps(
    [ranking.value?.records.map(record => record.package), home.value?.popularApps, home.value?.recentlyUpdated],
    24
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

defineOgImage('OgDefault', {
  locale: formattingLocale.value,
  variant: 'home',
  title: t('og.homeTitle'),
  description: t('og.homeDescription'),
  ...useOgFrame()
});

// Site and site search, with search terms in q.
useSchemaOrg([
  defineWebSite({
    name: 'OpenNavo',
    potentialAction: [defineSearchAction({ target: `${localePath('/search')}?q={search_term_string}` })]
  })
]);
</script>

<template>
  <div ref="root" class="landing flex flex-col gap-112px pb-56px md:gap-160px">
    <LandingHero
      :apps="showcase"
      :count="count"
      :categories="visibleCategories.map(({ slug, name }) => ({ slug, name }))"
    />
    <LandingCatalog v-if="count && wall.length" :apps="wall" :count="count" :updated="updatedToday" />
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
      <LandingTimelineDemo :apps="recentApps" />
    </LandingFeature>
    <LandingFeature
      reverse
      :kicker="t('landing.install.kicker')"
      :title="t('landing.install.title')"
      :body="t('landing.install.body')"
      :points="[
        t('landing.install.points.queue'),
        t('landing.install.points.confirm'),
        t('landing.install.points.history')
      ]"
    >
      <LandingInstallDemo v-if="demoApps[0]" :app="demoApps[0]" />
    </LandingFeature>
    <LandingFeature
      :kicker="t('landing.updates.kicker')"
      :title="t('landing.updates.title')"
      :body="t('landing.updates.body')"
      :points="[
        t('landing.updates.points.schedule'),
        t('landing.updates.points.pin'),
        t('landing.updates.points.mirror')
      ]"
    >
      <LandingTrayDemo :apps="recentApps" />
    </LandingFeature>
    <LandingMore :apps="demoApps" :count="count" />
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
