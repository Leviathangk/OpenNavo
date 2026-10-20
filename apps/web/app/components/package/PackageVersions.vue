<script setup lang="ts">
import { LOCALE_METADATA } from '@opennavo/shared';
import { unwrap } from '@opennavo/api';
import type { ReleaseEntry } from '@opennavo/api';
import { formatDate } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { OnEmpty, OnErrorState, OnIcon, OnPagination, OnSegmented, OnStatTile, OnVersionTimeline } from '@opennavo/ui';
import { pageCount as countPages } from '~/utils/catalogQuery';
import { decorative } from '~/utils/decorative';
import { versionsDescription, versionsTitle } from '~/utils/seoTitles';

// Version history (08 §10.6, mockup 04): toolbar (all / with notes, Chinese / original), timeline, stats/sources on right.
// Filters/original switch live in SSR URL (notes=1, original=1); canonical retains page only. No desktop installation markers or Update to this version.
const props = defineProps<{ kind: PackageKind }>();

// The backend decides each versions page at this size (S5); other sizes are never indexed.
const PAGE_SIZE = 24;
type Source = ReleaseEntry['source'];

const { t, locale } = useI18n();
const api = useApi();
const NuxtLink = resolveComponent('NuxtLink');
const appLocale = computed(() => toAppLocale(locale.value));
const { query, hrefWith, update } = useCatalogQuery({ page: 1, notes: false, original: false });
const { page, assertExists } = useListPage();
const onlyWithNotes = computed(() => query.value.notes === 'true' || query.value.notes === '1');
const original = computed(() => query.value.original === 'true' || query.value.original === '1');

const { data: pkg, token } = await usePackage(props.kind);
// F6: each version's macOS and chip compatibility, "Unknown" without data.
const compatibilityLabels = useVersionCompatibilityLabels();
// F6: the app's release feed (server/handlers/packageFeed.ts), linked from the head and next to the sources.
const feedHref = useReleaseFeed(() =>
  pkg.value
    ? { section: 'apps', key: pkg.value.token, title: t('feeds.appTitle', { name: pkg.value.displayName }) }
    : null
);
// A cask route can resolve to a link app (usePackage); its releases live under that kind.
const kind = computed(() => pkg.value?.kind ?? props.kind);

const releases = (options: { current: number; size: number; onlyWithNotes: boolean; original: boolean }) =>
  unwrap(
    api.GET('/packages/{kind}/{token}/releases', {
      params: { path: { kind: kind.value, token: token.value }, query: { ...options, locale: appLocale.value } }
    })
  );

const { data, error, refresh, status } = await useAsyncData(
  () => `releases:${kind.value}:${token.value}:${page.value}:${onlyWithNotes.value}:${original.value}:${locale.value}`,
  () => releases({ current: page.value, size: PAGE_SIZE, onlyWithNotes: onlyWithNotes.value, original: original.value })
);

// Segmented label totals (All 36 / With notes 29) are read per app, not taken from the shown page: while a filter
// switch loads, Nuxt keeps the previous page on screen, and its total belongs to the other filter. They are
// decorative (S7): if they fail, the labels show a dash and the timeline still renders.
const { data: totals } = await useAsyncData(
  () => `releases-count:${kind.value}:${token.value}:${appLocale.value}:${original.value}`,
  () =>
    decorative(
      Promise.all([
        releases({ current: 1, size: 1, onlyWithNotes: false, original: original.value }),
        releases({ current: 1, size: 1, onlyWithNotes: true, original: original.value })
      ]).then(([all, withNotes]) => ({ all: all.total, withNotes: withNotes.total })),
      null,
      'release filter counts'
    )
);

const counts = computed(() => ({ all: totals.value?.all ?? null, withNotes: totals.value?.withNotes ?? null }));

assertExists(() => data.value?.total, PAGE_SIZE, status);
// S5: this page's decision comes with its releases; the layout keeps the state when the tab changes.
usePageDecision(() => data.value?.indexing);
const pageCount = computed(() => countPages(data.value?.total ?? 0, PAGE_SIZE));

const filterOptions = computed(() => [
  {
    value: 'all' as const,
    label: t('package.release.filterAll', { count: counts.value.all ?? '—' }, { plural: counts.value.all ?? 0 })
  },
  {
    value: 'notes' as const,
    label: t(
      'package.release.filterWithNotes',
      { count: counts.value.withNotes ?? '—' },
      { plural: counts.value.withNotes ?? 0 }
    )
  }
]);
const languageOptions = computed(() => [
  { value: 'translated' as const, label: LOCALE_METADATA[appLocale.value].name },
  { value: 'original' as const, label: t('package.release.languageOriginal') }
]);

// Median Homebrew lag behind upstream; within one hour counts as synchronized.
function lag(minutes: number) {
  const value = Math.abs(minutes);
  if (value < 60) return t('package.release.lagMinutes', { n: value });
  if (value < 1440) return t('package.release.lagHours', { n: Math.round(value / 60) });
  return t('package.release.lagDays', { n: Math.round(value / 1440) }, { plural: Math.round(value / 1440) });
}

const brew = computed(() => {
  const stats = data.value?.stats;
  // Link apps never pass through Homebrew, so there is no adoption lag to compare.
  if (!stats || kind.value === 'link') return null;
  const median = stats.brewLag.medianMinutes;
  const value =
    median === null || median === undefined
      ? '—'
      : Math.abs(median) <= 60
        ? t('package.release.brewSync')
        : median > 0
          ? t('package.release.brewLater', { time: lag(median) })
          : t('package.release.brewEarlier', { time: lag(median) });
  return {
    value,
    hint: stats.brewLag.compared
      ? t('package.release.brewHint', { earlier: stats.brewLag.earlierCount, compared: stats.brewLag.compared })
      : undefined
  };
});

// Source explanation: upstream sources actually used by page entries, plus Homebrew adoption records.
const sources = computed(() => {
  const used = new Set<Source>();
  for (const entry of data.value?.records ?? []) {
    used.add(entry.source);
    if (entry.brewCommittedAt) used.add('homebrew');
  }
  const order: Source[] = ['editorial', 'webpage', 'github_release', 'sparkle', 'manual', 'homebrew'];
  return order.filter(source => used.has(source));
});

// WP1: the legacy title is the rollout fallback selected in admin. The description never uses
// versionChangedAt as a release date (utils/seoTitles currentReleaseDate).
const legacyTitles = useLegacyTitles();
usePageSeo({
  title: () => {
    const value = pkg.value;
    if (legacyTitles.value) return t('package.versionsMetaTitle', { name: value?.displayName ?? token.value });
    return value ? versionsTitle(value, t) : t('package.seo.versionsTitle', { name: token.value });
  },
  page,
  description: () =>
    pkg.value ? versionsDescription(pkg.value, t, iso => formatDate(iso, { locale: appLocale.value })) : undefined
});
</script>

<template>
  <OnErrorState v-if="error" @retry="refresh()" />
  <div v-else-if="data" class="grid gap-20px pt-4px lg:grid-cols-[minmax(0,1fr)_280px]">
    <section class="flex min-w-0 flex-col gap-16px">
      <div class="flex flex-wrap items-center justify-between gap-12px">
        <OnSegmented
          :model-value="onlyWithNotes ? 'notes' : 'all'"
          :options="filterOptions"
          :aria-label="t('package.release.filterLabel')"
          @update:model-value="value => update({ notes: value === 'notes' ? 1 : undefined })"
        />
        <OnSegmented
          :model-value="original ? 'original' : 'translated'"
          :options="languageOptions"
          :aria-label="t('package.release.languageLabel')"
          @update:model-value="value => update({ original: value === 'original' ? 1 : undefined, page: page })"
        />
      </div>
      <OnEmpty v-if="!data.records.length" icon="lucide:rotate-ccw-clock" :title="t('package.release.emptyTitle')" />
      <OnVersionTimeline
        v-else
        class="web-timeline"
        :entries="data.records"
        :show-original="original"
        :expanded-count="page === 1 ? 3 : 1"
        :heading-level="2"
        :compatibility-labels="compatibilityLabels"
      >
        <template #markdown="{ source }">
          <MarkdownContent :source="source" :heading-level="3" />
        </template>
      </OnVersionTimeline>
      <OnPagination
        v-if="pageCount > 1"
        class="self-center"
        :page="page"
        :page-count="pageCount"
        :href-for="target => hrefWith({ page: target })"
        :link-as="NuxtLink"
      />
    </section>
    <aside class="flex flex-col gap-16px">
      <div class="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-8px">
        <OnStatTile
          :label="t('package.release.count30d')"
          :value="String(data.stats.count30d)"
          :unit="t('package.release.count30dUnit', data.stats.count30d)"
          :hint="t(`package.release.cadenceHint.${data.stats.cadence}`)"
        />
        <OnStatTile v-if="brew" :label="t('package.release.brewLabel')" :value="brew.value" :hint="brew.hint" />
      </div>
      <a
        v-if="feedHref"
        :href="feedHref"
        type="application/rss+xml"
        class="inline-flex items-center gap-6px self-start text-13px text-ink-secondary no-underline hover:text-ink-primary hover:underline"
        ><OnIcon name="rss" :size="14" />{{ t('feeds.subscribe') }}</a
      >
      <section
        v-if="sources.length"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-14px"
      >
        <h2 class="m-0 mb-10px text-13.5px font-600 text-ink-primary">{{ t('package.release.sourcesTitle') }}</h2>
        <ol class="m-0 flex list-none flex-col gap-12px p-0">
          <li v-for="(source, index) in sources" :key="source" class="flex gap-10px">
            <span
              class="mt-1px h-20px w-20px flex shrink-0 items-center justify-center rounded-tiny text-11px font-600 text-ink-primary"
              :class="source === 'homebrew' ? 'bg-chart-series2' : 'bg-chart-series1'"
            >
              {{ index + 1 }}
            </span>
            <span class="min-w-0">
              <span class="block text-13px font-600 text-ink-primary">{{
                t(`package.release.sourceInfo.${source}.title`)
              }}</span>
              <span class="block text-12px leading-[1.5] text-ink-tertiary">{{
                t(`package.release.sourceInfo.${source}.body`)
              }}</span>
            </span>
          </li>
        </ol>
      </section>
    </aside>
  </div>
</template>

<style>
/* Web page background is surface.page; timeline dot rings follow it. */
.web-timeline .on-version-timeline {
  --on-version-timeline-bg: var(--on-surface-page);
}
</style>
