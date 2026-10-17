<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount, formatVersion, withRecentReturn } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { architectures, displayUrl, OnPackageOverview, useUiMessages } from '@opennavo/ui';
import type { OnKeyValueItem, OnRelatedPackage } from '@opennavo/ui';
import { packagePath } from '~/utils/packageView';
import { appEntityId, softwareAppNode } from '~/utils/structuredData';
import { trackEvent } from '~/utils/analytics';
import { overviewDescription, overviewTitle } from '~/utils/seoTitles';
import { httpsUrl } from '~/utils/linkApp';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Overview (08 §10.5): shared OnPackageOverview (ADR-017); left screenshots → introduction → latest release →
// install and uninstall → sourced facts → confirmed alternatives → questions (WP9, web only) → caveats.
// Right: install command → information → monthly installs → similar apps. Link apps (store/security plan §4)
// show their download panel instead of the command and have no install counts.
const props = defineProps<{ kind: PackageKind }>();

const route = useRoute();
const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const switchLocalePath = useSwitchLocalePath();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, token } = await usePackage(props.kind);
// A cask route can resolve to a link app (usePackage); related apps follow what was found.
const kind = computed(() => pkg.value?.kind ?? props.kind);

const { data: related } = await useAsyncData(
  () => `related:${kind.value}:${token.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/related', {
        params: { path: { kind: kind.value, token: token.value }, query: { limit: 5 } }
      })
    ).catch(() => [])
);

const shots = computed(() =>
  (pkg.value?.screenshots ?? []).map(shot => ({
    src: shot.url,
    thumb: shot.thumbUrl,
    caption: shot.caption ?? undefined
  }))
);

// Introduction is Markdown (03 §5 packages.description, 05 §11.3), falling back to summary.
const description = computed(() => pkg.value?.description ?? pkg.value?.summary ?? '');

// View original navigates to this page's source-language version.
const originalHref = computed(() => {
  const source = pkg.value?.sourceLocale;
  if (!source || !pkg.value?.machineTranslated) return undefined;
  const code = toRouteLocale(source);
  return code && code !== locale.value ? switchLocalePath(code) : undefined;
});

const relatedItems = computed<OnRelatedPackage[]>(() =>
  (related.value ?? []).map(item => ({
    key: `${item.kind}/${item.token}`,
    kind: item.kind,
    token: item.token,
    name: item.displayName,
    src: item.iconUrl,
    accent: item.accentColor,
    description: item.summary,
    href: localePath(packagePath(item.token))
  }))
);

const messages = useUiMessages();

// Download information as on the desktop: official site, GitHub, developer signature, price and bundle IDs.
const linkInfo = computed<OnKeyValueItem[]>(() => {
  const link = pkg.value?.link;
  if (!link) return [];
  const items: OnKeyValueItem[] = [];
  const homepage = httpsUrl(link.homepage);
  if (homepage)
    items.push({ key: 'homepage', label: t('package.info.homepage'), value: displayUrl(homepage), href: homepage });
  if (link.github)
    items.push({
      key: 'github',
      label: 'GitHub',
      value: link.github.repo,
      href: `https://github.com/${link.github.repo}`
    });
  if (link.teamId) items.push({ key: 'team', label: t('package.link.teamId'), value: link.teamId, mono: true });
  items.push({
    key: 'pricing',
    label: t('package.link.pricingLabel'),
    value: t(`package.link.pricing.${link.pricing}`)
  });
  const bundleIds = pkg.value?.bundleIds ?? [];
  if (bundleIds.length) items.push({ key: 'bundle', label: 'Bundle ID', value: bundleIds.join(' '), mono: true });
  return items;
});

const info = computed<OnKeyValueItem[]>(() => {
  const value = pkg.value;
  if (!value) return [];
  if (value.kind === 'link') return linkInfo.value;
  const items: OnKeyValueItem[] = [{ key: 'token', label: 'Token', value: value.token, mono: true }];
  if (value.homepage)
    items.push({
      key: 'homepage',
      label: t('package.info.homepage'),
      value: displayUrl(value.homepage),
      href: value.homepage
    });
  if (value.artifacts.binaries.length)
    items.push({
      key: 'binaries',
      label: t('package.info.binaries'),
      value: value.artifacts.binaries.join(' '),
      mono: true
    });
  if (value.kind === 'cask')
    items.push({
      key: 'auto',
      label: t('package.info.autoUpdates'),
      value: t(value.autoUpdates ? 'package.info.autoYes' : 'package.info.autoNo')
    });
  if (value.artifacts.uninstallNotes?.length)
    items.push({
      key: 'uninstall',
      label: t('package.info.uninstall'),
      value: value.artifacts.uninstallNotes.join('；')
    });
  const arch = architectures(value.supports).map(item => t(`package.arch.${item}`));
  items.push({
    key: 'arch',
    label: t('package.info.arch'),
    value:
      value.supports.status !== 'known' ? messages.value.cask.unknown : arch.join(' · ') || messages.value.cask.noMac
  });
  if (value.supports.requiresRosetta)
    items.push({ key: 'rosetta', label: t('package.arch.arm64'), value: messages.value.cask.rosetta });
  if (value.platforms?.some(platform => platform.version !== value.version))
    items.push({ key: 'platformVersions', label: messages.value.cask.version, value: messages.value.cask.legacy });
  if (value.license) items.push({ key: 'license', label: t('package.info.license'), value: value.license });
  return items;
});

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
  trackEvent('copy_install_command');
}

type SoftwareAppInput = NonNullable<Parameters<typeof defineSoftwareApp>[0]>;

// WP10 SoftwareApplication (utils/structuredData): only what the page shows, offers only for
// verified prices, none for fonts. Breadcrumbs come from PackageDetailLayout.
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
useSchemaOrg(
  computed(() => {
    const value = pkg.value;
    if (!value) return [];
    const app = softwareAppNode(value, `${siteUrl}${localePath(packagePath(value.token))}`, t);
    return [
      ...(app ? [defineSoftwareApp({ '@id': appEntityId(siteUrl, value.token), ...app } as SoftwareAppInput)] : []),
      // Otherwise the WebPage borrows the site logo as its primary image.
      ...(value.iconUrl ? [defineWebPage({ primaryImageOfPage: value.iconUrl })] : [])
    ];
  })
);

// WP1 titles and descriptions (utils/seoTitles). The legacy branch is the rollout fallback
// selected in admin; it goes away together with the switch. Cask-only (ADR-018).
const legacyTitles = useLegacyTitles();
usePageSeo({
  title: () => {
    const value = pkg.value;
    if (!value) return token.value;
    if (!legacyTitles.value) return overviewTitle(value, t);
    return t('package.metaTitleCask', { name: value.displayName, summary: value.summary ?? '' });
  },
  description: () => {
    const value = pkg.value;
    if (!value) return undefined;
    if (!legacyTitles.value) return overviewDescription(value, t);
    return t(
      'package.metaDescription',
      {
        summary: (value.summary ?? value.displayName).replace(/[。.]$/, ''),
        command: value.installCommand,
        version: formatVersion(value.version),
        installs: formatCount(value.installs.d30, { locale: formattingLocale.value })
      },
      { plural: value.installs.d30 }
    );
  }
});
</script>

<template>
  <div v-if="pkg" class="flex flex-col gap-14px">
    <PackageAnswer v-if="pkg.kind === 'cask'" :pkg="pkg" />
    <OnPackageOverview
      :screenshots="shots"
      :screenshots-label="t('package.screenshots')"
      :has-about="Boolean(description)"
      :about-machine-translated="Boolean(pkg.machineTranslated)"
      :about-original-href="originalHref"
      :caveats="pkg.caveats"
      :caveats-title="t('package.caveats')"
      :command="pkg.kind === 'link' ? undefined : pkg.installCommand"
      :info="info"
      :related="relatedItems"
      :related-title="t('package.related')"
      :link-as="NuxtLink"
      @copied="onCopied"
    >
      <template #about>
        <MarkdownContent size="md" :heading-level="3" :source="description" />
      </template>
      <template #main>
        <LatestReleaseCard
          v-if="pkg.latestRelease"
          :release="pkg.latestRelease"
          :total="pkg.releaseCount"
          :versions-href="withRecentReturn(localePath(packagePath(pkg.token, 'versions')), route.query.returnTo)"
        />
        <template v-if="pkg.kind === 'cask'">
          <PackageGuide :pkg="pkg" />
          <PackageFacts :pkg="pkg" />
          <PackageAlternatives :pkg="pkg" />
          <PackageFaq :pkg="pkg" />
          <PackageArticles :pkg="pkg" />
        </template>
      </template>
      <template v-if="pkg.kind === 'link' && pkg.link" #download>
        <LinkDownloadPanel :link="pkg.link" />
      </template>
      <template #aside>
        <MonthlyInstalls v-if="pkg.kind !== 'link'" :installs="pkg.installs" />
      </template>
    </OnPackageOverview>
  </div>
</template>
