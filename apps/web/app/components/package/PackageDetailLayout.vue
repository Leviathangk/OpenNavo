<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import type { PackageKind } from '@opennavo/shared';
import { OnBreadcrumb, OnErrorState, OnPackageNotice, OnTabs } from '@opennavo/ui';
import { allowedHreflangs, packageRobots } from '~/utils/indexing';
import { packageLink, packagePath } from '~/utils/packageView';

// Detail shell (05 §3): notice/header/stats shared with desktop (ADR-017), tabs with separate URLs, child-route content.
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const api = useApi();
const route = useRoute();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, error, refresh, token, removed } = await usePackage(props.kind);

// Share image (08 §14, WP13): every tab reuses the app's card; removed apps keep their name.
const formattingLocale = toAppLocale(locale.value);
if (pkg.value)
  defineOgImage('OgPackage', {
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
  defineOgImage('OgPackage', {
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
  () => `releases:stats:${props.kind}:${token.value}:${locale.value}`,
  () =>
    pkg.value?.releaseStats
      ? Promise.resolve(null)
      : unwrap(
          api.GET('/packages/{kind}/{token}/releases', {
            params: { path: { kind: props.kind, token: token.value }, query: { size: 1 } }
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

const tabs = computed(() => [
  {
    value: 'overview' as const,
    label: t('package.tabs.overview'),
    href: localePath(packagePath(token.value))
  },
  {
    value: 'versions' as const,
    label: t('package.tabs.versions'),
    count: pkg.value?.releaseCount || undefined,
    href: localePath(packagePath(token.value, 'versions'))
  },
  {
    value: 'dependencies' as const,
    label: t('package.tabs.dependencies'),
    href: localePath(packagePath(token.value, 'dependencies'))
  },
  {
    value: 'details' as const,
    label: t('package.tabs.details'),
    href: localePath(packagePath(token.value, 'details'))
  }
]);

// Breadcrumbs match desktop: Discover › primary category › app. Structured data uses the same list.
const breadcrumb = computed(() => {
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
watchEffect(() => {
  hreflangs.value = allowedHreflangs(pkg.value?.indexing);
});
onUnmounted(() => {
  hreflangs.value = null;
});

const notice = computed(() => {
  const value = pkg.value;
  if (!value) return null;
  if (value.disable)
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
      <PackageStats :pkg="pkg" :release-stats="releaseStats" />
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
