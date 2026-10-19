<script setup lang="ts">
import { disabledByHomebrew, recentReturnPath, withRecentReturn } from '@opennavo/shared';
import { unwrap } from '@opennavo/api';
import type { PackageKind } from '@opennavo/shared';
import { OnBreadcrumb, OnErrorState, OnPackageNotice, OnTabs } from '@opennavo/ui';
import { allowedHreflangs, consolidatesToOverview, packageRobots } from '~/utils/indexing';
import { packageLink, packagePath } from '~/utils/packageView';

// Detail shell (05 §3): notice/header/stats shared with desktop (ADR-017), tabs with separate URLs, child-route content.
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, error, refresh, token, removed } = await usePackage(props.kind);
// A cask route can resolve to a link app (usePackage); requests and tabs follow what was found.
const kind = computed(() => pkg.value?.kind ?? props.kind);
const isLink = computed(() => kind.value === 'link');

// Share image (08 §14, WP13): every tab reuses the app's card; removed apps keep their name.
const formattingLocale = toAppLocale(locale.value);
if (pkg.value)
  defineOgImage('OgPackageSatori', {
    locale: formattingLocale,
    name: pkg.value.displayName,
    summary: pkg.value.summary ?? undefined,
    kind: pkg.value.kind,
    token: pkg.value.token,
    iconUrl: pkg.value.iconUrl,
    accent: pkg.value.accentColor,
    ...useOgFrame()
  });
else if (removed.value)
  defineOgImage('OgPackageSatori', {
    locale: formattingLocale,
    name: removed.value.displayName,
    summary: removed.value.summary ?? undefined,
    kind: props.kind,
    token: removed.value.token,
    iconUrl: removed.value.iconUrl,
    ...useOgFrame()
  });

// Release cadence uses detail releaseStats; older backends fall back to timeline stats, showing a dash on failure.
const { data: fallbackStats } = await useAsyncData(
  () => `releases:stats:${kind.value}:${token.value}:${locale.value}`,
  () =>
    pkg.value?.releaseStats
      ? Promise.resolve(null)
      : unwrap(
          api.GET('/packages/{kind}/{token}/releases', {
            params: { path: { kind: kind.value, token: token.value }, query: { size: 1 } }
          })
        )
          .then(page => page.stats)
          .catch(() => null)
);
const releaseStats = computed(() => pkg.value?.releaseStats ?? fallbackStats.value);

type Tab = 'overview' | 'versions' | 'dependencies' | 'details';

const tab = computed<Tab>(() => {
  const last = route.path.split('/').at(-1);
  return last === 'versions' || last === 'dependencies' || last === 'details' ? last : 'overview';
});

const returnTo = computed(() => recentReturnPath(route.query.returnTo));
const allTabs = computed(() => [
  {
    value: 'overview' as const,
    label: t('package.tabs.overview'),
    href: withRecentReturn(localePath(packagePath(token.value)), returnTo.value)
  },
  {
    value: 'versions' as const,
    label: t('package.tabs.versions'),
    count: pkg.value?.releaseCount || undefined,
    href: withRecentReturn(localePath(packagePath(token.value, 'versions')), returnTo.value)
  },
  {
    value: 'dependencies' as const,
    label: t('package.tabs.dependencies'),
    href: withRecentReturn(localePath(packagePath(token.value, 'dependencies')), returnTo.value)
  },
  {
    value: 'details' as const,
    label: t('package.tabs.details'),
    href: withRecentReturn(localePath(packagePath(token.value, 'details')), returnTo.value)
  }
]);
// Link apps have no Homebrew definition, so there are no dependencies or install details to show.
const tabs = computed(() =>
  isLink.value ? allTabs.value.filter(item => item.value === 'overview' || item.value === 'versions') : allTabs.value
);

// Breadcrumbs match desktop: Discover › primary category › app. Structured data uses the same list.
const breadcrumb = computed(() => {
  if (returnTo.value)
    return [
      { label: t('recent.title'), href: localePath(returnTo.value) },
      { label: pkg.value?.displayName ?? token.value }
    ];
  const category = pkg.value?.primaryCategory;
  return [
    { label: t('nav.discover'), href: localePath('/discover') },
    ...(category ? [{ label: category.name, href: localePath(`/categories/${category.slug}`) }] : []),
    { label: pkg.value?.displayName ?? token.value }
  ];
});
useBreadcrumbSchema(breadcrumb);

// WP11: the API decides indexability and languages; each tab adds its own value check. Alternates
// for languages that are not indexed are dropped from hreflang (app.vue reads this state).
useSeoMeta({ robots: () => (pkg.value ? packageRobots(pkg.value, tab.value, toAppLocale(locale.value)) : undefined) });
const hreflangs = useState<string[] | null>('page:hreflangs', () => null);
// Install details hand their ranking to the overview through the canonical (app.vue reads this
// state) and keep no alternates, so hreflang cannot pull them back into a cluster of their own.
const canonicalPath = useState<string | null>('page:canonicalPath', () => null);
watchEffect(() => {
  const toOverview = pkg.value ? consolidatesToOverview(pkg.value, tab.value, toAppLocale(locale.value)) : false;
  canonicalPath.value = toOverview ? localePath(packagePath(token.value)) : null;
  hreflangs.value = toOverview ? [] : allowedHreflangs(pkg.value?.indexing);
});
onUnmounted(() => {
  hreflangs.value = null;
  canonicalPath.value = null;
});

const notice = computed(() => {
  const value = pkg.value;
  if (!value) return null;
  // A link app Homebrew later added: point to the one-click install page.
  if (value.link?.adoptedByCask)
    return {
      tone: 'warning' as const,
      text: t('package.link.adopted'),
      replacement: { kind: 'cask' as const, token: value.link.adoptedByCask }
    };
  if (value.disable && disabledByHomebrew(value))
    return {
      tone: 'danger' as const,
      text: t('package.disabledNotice', { reason: value.disable.reason ?? t('package.noReason') }),
      replacement: value.disable.replacement
    };
  if (value.deprecation)
    return {
      tone: 'warning' as const,
      text: t('package.deprecatedNotice', { reason: value.deprecation.reason ?? t('package.noReason') }),
      replacement: value.deprecation.replacement
    };
  // Paused only by OpenNavo's 90-day rule: Homebrew still offers and installs the app.
  if (value.disable)
    return { tone: 'warning' as const, text: t('package.staleNotice'), replacement: value.disable.replacement };
  return null;
});
</script>

<template>
  <div class="box-border flex flex-col gap-14px pt-6px">
    <PackageRemoved v-if="removed" :info="removed" />
    <OnErrorState v-else-if="error && !pkg" @retry="refresh()" />
    <template v-else-if="pkg">
      <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
      <OnPackageNotice
        v-if="notice"
        :tone="notice.tone"
        :text="notice.text"
        :replacement-label="
          notice.replacement ? t('package.replacement', { token: notice.replacement.token }) : undefined
        "
        :replacement-href="
          notice.replacement && packageLink(notice.replacement.kind, notice.replacement.token)
            ? localePath(packagePath(notice.replacement.token))
            : undefined
        "
        :link-as="NuxtLink"
      />
      <PackageHeader :pkg="pkg" />
      <PackageStats v-if="!isLink" :pkg="pkg" :release-stats="releaseStats" />
      <OnTabs
        class="mt-2px"
        :model-value="tab"
        :items="tabs"
        :aria-label="t('package.tabs.label')"
        :link-as="NuxtLink"
      />
      <NuxtPage />
    </template>
  </div>
</template>
