<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount, formatList, formatVersion, packageInstallState } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { imageSrcset, OnPackageOverview, OnPackageSpecCard } from '@opennavo/ui';
import type { OnRelatedPackage } from '@opennavo/ui';
import { capabilities } from '~/utils/packageFacts';
import { packagePath } from '~/utils/packageView';
import { appEntityId, softwareAppNode } from '~/utils/structuredData';
import { overrideDescription, overviewDescription, overviewPageTitle, withVariantNames } from '~/utils/seoTitles';
import { packageSpec } from '~/utils/packageSpec';
import PackageLead from './PackageLead.vue';
import PackagePrice from './PackagePrice.vue';
import PackageRequirements from './PackageRequirements.vue';
import PackageSources from './PackageSources.vue';
import PackageInstallDetailsContent from './PackageInstallDetailsContent.vue';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Overview (08 §10.5, T2): shared OnPackageOverview (ADR-017). One facts model (PackageDetail.facts) feeds every
// block, the questions and the JSON-LD. The lead says what the app is; the main column then follows what people
// search for: the latest release, security facts for link apps, known issues, integrations, install and
// uninstall, questions, alternatives, system requirements and sources for link apps (their Install Details tab
// is hidden), then related articles. Right: install command (or a link app's download panel) → spec card →
// monthly installs → OpenNavo's malware scanning on security-category apps → similar apps (fonts: similar fonts)
// → collections. Blocks follow what the distribution supports (T3): link apps get facts, questions and articles
// but never a Homebrew command.
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
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

// T1: same-kind recommendations (apps for apps, fonts for fonts), four visible rows with a See more link.
const { data: related } = await useAsyncData(
  () => `related:${kind.value}:${token.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/related', {
        params: {
          path: { kind: kind.value, token: token.value },
          query: { limit: 4 }
        }
      })
    ).catch(() => []),
  { transform: withVariantNames }
);

const shots = computed(() =>
  (pkg.value?.screenshots ?? []).map(shot => ({
    src: shot.url,
    thumb: shot.thumbUrl,
    // The card crop at 1x plus the 640 px thumbnail for 2x screens; both fill the same cropped box.
    thumbSrcset: imageSrcset(shot.thumbnailVariants, {
      url: shot.thumbUrl,
      width: 640
    }),
    caption: shot.caption ?? undefined
  }))
);

// Introduction is Markdown (03 §5 packages.description, 05 §11.3), falling back to summary.
const description = computed(() => pkg.value?.description ?? pkg.value?.summary ?? '');

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

const specNow = usePackageSpecNow();
const spec = computed(() => packageSpec(pkg.value!, t, toAppLocale(locale.value), specNow.value));
type SoftwareAppInput = NonNullable<Parameters<typeof defineSoftwareApp>[0]>;

// WP10 SoftwareApplication (utils/structuredData): only what the page shows, offers only for
// verified prices, none for fonts. Breadcrumbs come from PackageDetailLayout. T4: the overview's WebPage
// has the app as mainEntity and the icon as its primary image; the app and the icon have stable @ids,
// the same in every language, so subpages and articles point at them instead of repeating them.
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
const canonical = useCanonicalUrl();
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
        ? [
            defineImage({
              '@id': iconId.value,
              url: value.iconUrl,
              contentUrl: value.iconUrl
            })
          ]
        : [])
    ];
  })
);
usePageSchema(() => ({
  mainEntity: pkg.value && !pkg.value.isFont ? appEntityId(siteUrl, pkg.value.token) : undefined,
  imageId: iconId.value
}));

// WP1 titles and descriptions (utils/seoTitles). An override set for this language wins over both
// templates, one field at a time, unless Homebrew has disabled the app. The legacy branch is the rollout
// fallback selected in admin; it goes away together with the switch. Cask-only (ADR-018).
const legacyTitles = useLegacyTitles();
usePageSeo({
  title: () => {
    const value = pkg.value;
    if (!value) return token.value;
    return overviewPageTitle(value, t, locale.value, legacyTitles.value);
  },
  description: () => {
    const value = pkg.value;
    if (!value) return undefined;
    const custom = overrideDescription(value);
    if (custom) return custom;
    // The legacy template embeds the install command, which a Homebrew-disabled package no longer has.
    if (!legacyTitles.value || packageInstallState(value) === 'disabled')
      return overviewDescription(value, t, values => formatList(values, { locale: formattingLocale.value }));
    return t(
      'package.metaDescription',
      {
        summary: (value.summary ?? value.displayName).replace(/[。.]$/, ''),
        command: value.installCommand,
        version: formatVersion(value.version),
        installs: formatCount(value.installs.d30, {
          locale: formattingLocale.value
        })
      },
      { plural: value.installs.d30 }
    );
  }
});
</script>

<template>
  <div v-if="pkg && can" class="flex flex-col gap-14px">
    <OnPackageOverview
      :screenshots="shots"
      :screenshots-label="t('package.screenshots')"
      :screenshots-name="pkg.displayName"
      :has-about="Boolean(description)"
      related-in-main
      :related="relatedItems"
      :related-title="t(pkg.isFont ? 'package.relatedFonts' : 'package.related')"
      :related-more-href="localePath('/apps')"
      :related-more-label="t('package.relatedMore')"
      :link-as="NuxtLink"
    >
      <template #about>
        <!-- Link apps have no Install Details tab, so their lead, price and requirements stay on the overview. -->
        <PackageLead v-if="pkg.kind === 'link'" class="mb-10px" :pkg="pkg" />
        <MarkdownContent class="package-overview-description" size="md" :heading-level="3" :source="description" />
      </template>
      <template #main>
        <LatestReleaseCard
          v-if="pkg.latestRelease"
          :release="pkg.latestRelease"
          :total="pkg.releaseCount"
          :versions-href="localePath(packagePath(pkg.token, 'versions'))"
        />
        <PackageSafety v-if="can.safety && pkg.kind === 'link'" :pkg="pkg" :show-checked="false" />
        <PackageRequirements v-if="pkg.kind === 'link'" :pkg="pkg" />
        <PackagePrice v-if="pkg.kind === 'link'" :pkg="pkg" />
        <PackageIssues :pkg="pkg" />
        <PackageIntegrations :pkg="pkg" />
        <PackageInstall v-if="can.homebrew" :pkg="pkg" />
        <PackageInstallDetailsContent
          v-if="can.homebrew && pkg.indexing?.detailsRedirect"
          :pkg="pkg"
          :show-command="false"
          uninstall-guide
        />
        <!-- Link apps have no details or uninstall tab, so their declarations and cleanup paths stay here in full. -->
        <PackageUninstall v-if="!can.homebrew && can.uninstall" :pkg="pkg" :homebrew="false" :guide="false" />
        <PackageFaq :pkg="pkg" />
        <PackageAlternatives :pkg="pkg" />
        <PackageSources v-if="pkg.kind === 'link'" :pkg="pkg" />
        <PackageArticles :pkg="pkg" />
      </template>
      <template v-if="pkg.kind === 'link' && pkg.link" #download>
        <LinkDownloadPanel :link="pkg.link" />
      </template>
      <template #spec>
        <OnPackageSpecCard
          :rows="spec.rows"
          :title="t('package.spec.title')"
          :checked-at="spec.checkedAt"
          :checked-label="t('package.spec.checked')"
          :link-as="NuxtLink"
        />
      </template>
      <template #aside>
        <MonthlyInstalls v-if="can.installs" class="hidden lg:block" :installs="pkg.installs" />
        <PackageSecurityPromo v-if="isSecurityApp" />
      </template>
      <template #asideEnd>
        <PackageCollections :pkg="pkg" />
      </template>
    </OnPackageOverview>
  </div>
</template>

<style>
.package-overview-description .on-markdown {
  font-size: 15px;
  line-height: 1.75;
  max-width: 76ch;
}
</style>
