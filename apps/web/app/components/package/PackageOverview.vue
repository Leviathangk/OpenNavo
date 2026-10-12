<script setup lang="ts">
import { unwrap } from '@opennavo/api';
import { formatCount, formatVersion } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
import { architectures, displayUrl, OnPackageOverview, useUiMessages } from '@opennavo/ui';
import type { OnKeyValueItem, OnRelatedPackage } from '@opennavo/ui';
import { applicationCategory, packagePath } from '~/utils/packageView';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

// Overview (08 §10.5): shared OnPackageOverview (ADR-017); left screenshots → introduction → latest release → caveats.
// Right: install command → information → monthly installs → similar apps.
const props = defineProps<{ kind: PackageKind }>();

const { t, locale } = useI18n();
const api = useApi();
const localePath = useLocalePath();
const switchLocalePath = useSwitchLocalePath();
const toasts = useToasts();
const NuxtLink = resolveComponent('NuxtLink');

const { data: pkg, token } = await usePackage(props.kind);

const { data: related } = await useAsyncData(
  () => `related:${props.kind}:${token.value}:${locale.value}`,
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/related', {
        params: { path: { kind: props.kind, token: token.value }, query: { limit: 5 } }
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

const info = computed<OnKeyValueItem[]>(() => {
  const value = pkg.value;
  if (!value) return [];
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
  if (value.platforms?.length) {
    const requirements = architectures(value.supports).map(platformArch => {
      const conditions = [
        ...new Set(
          value.platforms
            ?.filter(platform => platform.arch === platformArch)
            .map(platform => platform.dependsOn.macos ?? messages.value.cask.unknown)
        )
      ];
      return `${t(`package.arch.${platformArch}`)}: ${conditions.join(' / ')}`;
    });
    items.push({ key: 'macos', label: messages.value.cask.requirements, value: requirements.join(' · ') });
  } else if (value.minMacos)
    items.push({ key: 'macos', label: t('package.info.minMacos'), value: `macOS ${value.minMacos}` });
  if (value.license) items.push({ key: 'license', label: t('package.info.license'), value: value.license });
  return items;
});

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
}

type SoftwareAppInput = NonNullable<Parameters<typeof defineSoftwareApp>[0]>;

// Share image (08 §14).
if (pkg.value)
  defineOgImage('OgPackage', {
    locale: formattingLocale.value,
    name: pkg.value.displayName,
    summary: pkg.value.summary ?? undefined,
    kind: pkg.value.kind,
    token: pkg.value.token,
    iconUrl: pkg.value.iconUrl,
    accent: pkg.value.accentColor,
    ...useOgFrame()
  });

// 05 §6.2: SoftwareApplication and breadcrumbs; URL matches absolute canonical.
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
useSchemaOrg(
  computed(() => {
    const value = pkg.value;
    if (!value) return [];
    return [
      // 05 §6.2: omit offers because price is unknown; never label paid apps free. Assert against definer input despite its required offers type.
      defineSoftwareApp({
        name: value.displayName,
        alternateName: value.names.filter(name => name !== value.displayName),
        operatingSystem: 'macOS',
        applicationCategory: applicationCategory(value.primaryCategory?.slug),
        softwareVersion: value.version,
        url: `${siteUrl}${localePath(packagePath(value.token))}`,
        ...(value.homepage ? { sameAs: value.homepage } : {}),
        ...(value.summary ? { description: value.summary } : {}),
        ...(value.iconUrl ? { image: value.iconUrl } : {})
      } as SoftwareAppInput),
      defineBreadcrumb({
        itemListElement: [
          { name: t('nav.discover'), item: localePath('/discover') },
          {
            name: t('catalog.appsTitle'),
            item: localePath('/apps')
          },
          { name: value.displayName }
        ]
      })
    ];
  })
);

// 05 §6.1: title is name — summary; grouped numbers. Cask-only (ADR-018).
usePageSeo({
  title: () => {
    const value = pkg.value;
    if (!value) return token.value;
    const summary = value.summary ?? '';
    return t('package.metaTitleCask', { name: value.displayName, summary });
  },
  description: () => {
    const value = pkg.value;
    if (!value) return undefined;
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
  <OnPackageOverview
    v-if="pkg"
    :screenshots="shots"
    :screenshots-label="t('package.screenshots')"
    :has-about="Boolean(description)"
    :about-machine-translated="Boolean(pkg.machineTranslated)"
    :about-original-href="originalHref"
    :caveats="pkg.caveats"
    :caveats-title="t('package.caveats')"
    :command="pkg.installCommand"
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
        :versions-href="localePath(packagePath(pkg.token, 'versions'))"
      />
    </template>
    <template #aside>
      <MonthlyInstalls :installs="pkg.installs" />
    </template>
  </OnPackageOverview>
</template>
