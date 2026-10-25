<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount, formatList, formatVersion } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { displayUrl, imageSrcset, OnPackageOverview } from '@opennavo/ui';
import type { OnKeyValueItem, OnRelatedPackage } from '@opennavo/ui';
import { capabilities } from '~/utils/packageFacts';
import { packagePath } from '~/utils/packageView';
import { appEntityId, softwareAppNode } from '~/utils/structuredData';
import { trackEvent } from '~/utils/analytics';
import { overviewDescription, overviewTitle, withVariantNames } from '~/utils/seoTitles';
import type { SeoExperimentAssignment } from '~/utils/seoTitles';
import { httpsUrl } from '~/utils/linkApp';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Overview (08 §10.5, T2): shared OnPackageOverview (ADR-017). One facts model (PackageDetail.facts) feeds every
// block, the questions and the JSON-LD. The lead says what the app is; the facts table opens the main column;
// then, in the order people search for them: price, alternatives, system requirements, install and uninstall
// with optional data clean-up, the latest release, security facts, known issues, MCP servers and Skills,
// dependencies, install details, questions, related articles and sources. Right: install command (or a link
// app's download panel) → information → monthly installs → OpenNavo's malware scanning on security-category
// apps → similar apps (fonts: similar fonts) → collections. Blocks follow what the distribution supports (T3):
// link apps get facts, questions and articles but never a Homebrew command.
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const switchLocalePath = useSwitchLocalePath();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, token } = await usePackage(props.kind);
// A cask route can resolve to a link app (usePackage); related apps follow what was found.
const kind = computed(() => pkg.value?.kind ?? props.kind);
const can = computed(() => (pkg.value ? capabilities(pkg.value) : null));
const isSecurityApp = computed(
  () =>
    pkg.value?.primaryCategory?.slug === 'security' ||
    Boolean(pkg.value?.categories?.some(category => category.slug === 'security'))
);

// H3: stable page assignment, shared with hydration. Run alongside recommendations, with a 250 ms
// budget and a concrete control payload on failure so an outage never breaks SSR or triggers a retry.
const canonical = useCanonicalUrl();
const control: SeoExperimentAssignment = {
  experimentId: null,
  arm: 'control',
  variant: null,
  templateVersion: '',
  eligible: false,
  expiresAt: null
};
const experimentRequest = useAsyncData(
  () => `seo-experiment:${canonical.value}`,
  () =>
    unwrap(
      api.GET('/seo/experiment', {
        params: { query: { url: canonical.value } },
        signal: AbortSignal.timeout(250)
      })
    ).catch(() => control)
);

// T1: same-kind recommendations (apps for apps, fonts for fonts), eight of them.
const { data: related } = await useAsyncData(
  () => `related:${kind.value}:${token.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/related', {
        params: { path: { kind: kind.value, token: token.value }, query: { limit: 8 } }
      })
    ).catch(() => []),
  { transform: withVariantNames }
);
const { data: experiment } = await experimentRequest;

const shots = computed(() =>
  (pkg.value?.screenshots ?? []).map(shot => ({
    src: shot.url,
    thumb: shot.thumbUrl,
    // The card crop at 1x plus the 640 px thumbnail for 2x screens; both fill the same cropped box.
    thumbSrcset: imageSrcset(shot.thumbnailVariants, { url: shot.thumbUrl, width: 640 }),
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
    variants: item.iconVariants,
    accent: item.accentColor,
    description: item.summary,
    href: localePath(packagePath(item.token))
  }))
);

// Download information as on the desktop: official site, GitHub and bundle IDs. The signature and price are in
// the facts table, the chips in the requirements table.
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
  if (value.kind === 'cask')
    items.push({
      key: 'auto',
      label: t('package.info.autoUpdates'),
      value: t(value.autoUpdates ? 'package.info.autoYes' : 'package.info.autoNo')
    });
  return items;
});

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
  trackEvent('copy_install_command');
}

type SoftwareAppInput = NonNullable<Parameters<typeof defineSoftwareApp>[0]>;

// WP10 SoftwareApplication (utils/structuredData): only what the page shows, offers only for
// verified prices, none for fonts. Breadcrumbs come from PackageDetailLayout. T4: the overview's WebPage
// has the app as mainEntity and the icon as its primary image; the app and the icon have stable @ids,
// the same in every language, so subpages and articles point at them instead of repeating them.
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const iconId = computed(() => (pkg.value?.iconUrl ? `${siteUrl}/apps/${pkg.value.token}#icon` : undefined));
useSchemaOrg(
  computed(() => {
    const value = pkg.value;
    if (!value) return [];
    const app = softwareAppNode(value, `${siteUrl}${localePath(packagePath(value.token))}`, t, formattingLocale.value);
    return [
      ...(app
        ? [
            defineSoftwareApp({
              '@id': appEntityId(siteUrl, value.token),
              ...app,
              mainEntityOfPage: { '@id': `${canonical.value}#webpage` },
              ...(iconId.value ? { image: { '@id': iconId.value } } : {})
            } as SoftwareAppInput)
          ]
        : []),
      ...(iconId.value && value.iconUrl
        ? [defineImage({ '@id': iconId.value, url: value.iconUrl, contentUrl: value.iconUrl })]
        : [])
    ];
  })
);
usePageSchema(() => ({
  mainEntity: pkg.value && !pkg.value.isFont ? appEntityId(siteUrl, pkg.value.token) : undefined,
  imageId: iconId.value
}));

// WP1 titles and descriptions (utils/seoTitles). The legacy branch is the rollout fallback
// selected in admin; it goes away together with the switch. Cask-only (ADR-018).
const legacyTitles = useLegacyTitles();
usePageSeo({
  title: () => {
    const value = pkg.value;
    if (!value) return token.value;
    if (!legacyTitles.value) return overviewTitle(value, t, experiment.value);
    return t('package.metaTitleCask', { name: value.displayName, summary: value.summary ?? '' });
  },
  description: () => {
    const value = pkg.value;
    if (!value) return undefined;
    if (!legacyTitles.value)
      return overviewDescription(
        value,
        t,
        values => formatList(values, { locale: formattingLocale.value }),
        experiment.value
      );
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
  <div v-if="pkg && can" class="flex flex-col gap-14px">
    <PackageLead :pkg="pkg" />
    <OnPackageOverview
      :screenshots="shots"
      :screenshots-label="t('package.screenshots')"
      :screenshots-name="pkg.displayName"
      :has-about="Boolean(description)"
      :about-machine-translated="Boolean(pkg.machineTranslated)"
      :about-original-href="originalHref"
      :command="can.homebrew ? pkg.installCommand : undefined"
      :info="info"
      :related="relatedItems"
      :related-title="t(pkg.isFont ? 'package.relatedFonts' : 'package.related')"
      :link-as="NuxtLink"
      @copied="onCopied"
    >
      <template #top>
        <PackageQuickFacts :pkg="pkg" />
      </template>
      <template #about>
        <MarkdownContent size="md" :heading-level="3" :source="description" />
      </template>
      <template #main>
        <PackagePrice :pkg="pkg" />
        <PackageAlternatives :pkg="pkg" />
        <PackageRequirements :pkg="pkg" />
        <PackageInstall v-if="can.homebrew" :pkg="pkg" />
        <PackageUninstall v-if="can.uninstall" :pkg="pkg" :homebrew="can.homebrew" />
        <LatestReleaseCard
          v-if="pkg.latestRelease"
          :release="pkg.latestRelease"
          :total="pkg.releaseCount"
          :versions-href="localePath(packagePath(pkg.token, 'versions'))"
        />
        <PackageSafety v-if="can.safety" :pkg="pkg" />
        <PackageIssues :pkg="pkg" />
        <PackageIntegrations :pkg="pkg" />
        <PackageDependencySummary v-if="can.dependencies" :pkg="pkg" />
        <PackageInstallFacts v-if="can.homebrew" :pkg="pkg" />
        <PackageFaq :pkg="pkg" />
        <PackageArticles :pkg="pkg" />
        <PackageSources :pkg="pkg" />
      </template>
      <template v-if="pkg.kind === 'link' && pkg.link" #download>
        <LinkDownloadPanel :link="pkg.link" />
      </template>
      <template #aside>
        <MonthlyInstalls v-if="can.installs" :installs="pkg.installs" />
        <PackageSecurityPromo v-if="isSecurityApp" />
      </template>
      <template #asideEnd>
        <PackageCollections :pkg="pkg" />
      </template>
    </OnPackageOverview>
  </div>
</template>
