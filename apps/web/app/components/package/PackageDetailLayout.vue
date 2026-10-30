<script setup lang="ts">
import { subpageAppNode } from '~/utils/packageFacts';
import { disabledByHomebrew, recentReturnPath } from '@opennavo/shared';
import { unwrap } from '@opennavo/api';
import type { PackageKind } from '@opennavo/shared';
import { OnBreadcrumb, OnErrorState, OnIcon, OnPackageNotice, OnTabs } from '@opennavo/ui';
import { packageLink, packagePath } from '~/utils/packageView';
import { appEntityId } from '~/utils/structuredData';
import { hasTroubleshootingPage, troubleshootingPath } from '~/utils/troubleshooting';

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
    // Revalidated at request time so a URL signed before the package went private stops rendering.
    visibility: `${pkg.value.kind}:${pkg.value.token}`,
    iconUrl: pkg.value.iconUrl,
    contentVersion: pkg.value.version,
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
    // Removed apps keep their image; a private app never reaches this branch.
    visibility: `${props.kind}:${removed.value.token}`,
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

type Tab = 'overview' | 'versions' | 'dependencies' | 'details' | 'troubleshooting';

const tab = computed<Tab>(() => {
  const last = route.path.split('/').at(-1);
  return last === 'versions' || last === 'dependencies' || last === 'details' || last === 'troubleshooting'
    ? last
    : 'overview';
});
// F7: the troubleshooting page exists (and gets a tab) only with enough verified known issues.
const troubleshooting = computed(() => hasTroubleshootingPage(pkg.value));
const tabLabel = (value: Exclude<Tab, 'overview'>) =>
  value === 'troubleshooting' ? t('troubleshooting.tab') : t(`package.tabs.${value}`);

const allTabs = computed(() => [
  { value: 'overview' as const, label: t('package.tabs.overview'), href: localePath(packagePath(token.value)) },
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
  },
  ...(troubleshooting.value
    ? [
        {
          value: 'troubleshooting' as const,
          label: t('troubleshooting.tab'),
          href: localePath(troubleshootingPath(token.value))
        }
      ]
    : [])
]);
// Link apps have no Homebrew definition, so there are no dependencies or install details to show.
// H9: once the overview carries what install details showed and the server turns the redirect on (per
// language, so a target that is not ready never receives it), the details tab answers one 301 to the
// overview and drops out of the tabs, so no page links it.
const detailsRedirect = computed(() => Boolean(pkg.value?.indexing?.detailsRedirect));
// The API's DecidePage uses HasDependencyContent (including reverse dependencies). Other noindex
// reasons and older APIs keep their links; an empty tab remains directly accessible with its decision.
const emptyDependencies = computed(() => pkg.value?.indexing?.pages?.dependencies?.reason === 'duplicate_of_overview');
if (tab.value === 'details' && detailsRedirect.value)
  await navigateTo(localePath(packagePath(token.value)), { redirectCode: 301, replace: true });
const tabs = computed(() =>
  isLink.value
    ? allTabs.value.filter(
        item => item.value === 'overview' || item.value === 'versions' || item.value === 'troubleshooting'
      )
    : allTabs.value.filter(
        item =>
          (item.value !== 'details' || !detailsRedirect.value) &&
          (item.value !== 'dependencies' || !emptyDependencies.value)
      )
);

// Breadcrumbs are always Discover › primary category › app (› tab on subpages), the same in SSR and on the
// client. Structured data uses the same list, so a subpage's last item names that page.
const breadcrumb = computed(() => {
  const category = pkg.value?.primaryCategory;
  const name = pkg.value?.displayName ?? token.value;
  return [
    { label: t('nav.discover'), href: localePath('/discover') },
    ...(category ? [{ label: category.name, href: localePath(`/categories/${category.slug}`) }] : []),
    ...(tab.value === 'overview'
      ? [{ label: name }]
      : [{ label: name, href: localePath(packagePath(token.value)) }, { label: tabLabel(tab.value) }])
  ];
});
useBreadcrumbSchema(breadcrumb);
// Subpages are about the app; its full SoftwareApplication lives on the overview only (T4).
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
usePageSchema(() => (tab.value === 'overview' || !pkg.value ? {} : { about: appEntityId(siteUrl, pkg.value.token) }));
useSchemaOrg(
  computed(() =>
    tab.value === 'overview' || !pkg.value
      ? []
      : [
          defineSoftwareApp(
            subpageAppNode(
              pkg.value,
              appEntityId(siteUrl, pkg.value.token),
              `${siteUrl}${localePath(packagePath(pkg.value.token))}`
            )
          )
        ]
  )
);
// The versions and troubleshooting pages' h1 names the page (S1, F7); the other subpages keep the app name.
const headingSuffix = computed(() =>
  tab.value === 'versions' || tab.value === 'troubleshooting' ? tabLabel(tab.value) : undefined
);

// Opened from Recently updated: the list position travels in history.state (S3), never in the URL, so
// crawlers see one URL per page. The link is client-only: it is set after mount and so never hydrates.
const back = ref<string>();
const readBack = () => {
  back.value = recentReturnPath((window.history.state as { onvReturn?: unknown } | null)?.onvReturn);
};
onMounted(readBack);
// Tabs keep the layout mounted; only a different app re-reads the state of its own history entry.
watch(token, () => nextTick(readBack));

// S5: the API decides each page (robots, canonical, hreflang) and the web applies it. The versions tab
// applies its own page's decision from the releases endpoint (PackageVersions), and the troubleshooting
// page its own (PackageTroubleshooting, F7), so both are skipped here.
usePageDecision(() =>
  tab.value === 'versions' || tab.value === 'troubleshooting' ? undefined : pkg.value?.indexing?.pages[tab.value]
);

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
      <div class="flex flex-wrap items-center justify-between gap-x-12px gap-y-4px">
        <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
        <NuxtLink
          v-if="back"
          :to="localePath(back)"
          class="flex items-center gap-4px text-13px text-ink-secondary no-underline hover:text-ink-primary"
        >
          <OnIcon name="lucide:arrow-left" :size="14" />
          {{ t('recent.title') }}
        </NuxtLink>
      </div>
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
      <PackageHeader :pkg="pkg" :heading-suffix="headingSuffix" :show-official-download="tab === 'overview'" />
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
