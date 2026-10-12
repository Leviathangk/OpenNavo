<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { installCommand } from '@opennavo/shared';
import { architectures, displayUrl, OnPackageOverview, useUiMessages } from '@opennavo/ui';
import type { OnKeyValueItem, OnRelatedPackage } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import LatestRelease from '@/components/package/LatestRelease.vue';
import MonthlyInstallsCard from '@/components/package/MonthlyInstallsCard.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useRemoteLoader } from '@/composables/useRemoteLoader';
import { usePackage } from '@/composables/usePackageDetail';
import { useToasts } from '@/composables/useToasts';

// Overview (mockup 03): shared OnPackageOverview (ADR-017); left: screenshots → introduction → latest release → caveats.
// Right: install command → information → monthly installs → similar apps. Offline shows only the local summary and install command.
const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const toasts = useToasts();
const { kind, token, detail, local } = usePackage();

const { data: related } = useRemoteLoader(
  'related',
  () =>
    unwrap(
      api.GET('/packages/{kind}/{token}/related', {
        params: { path: { kind: kind.value, token: token.value }, query: { limit: 5 } }
      })
    ),
  [kind, token, appLocale]
);

const shots = computed(() =>
  (detail.value?.screenshots ?? []).map(shot => ({
    src: shot.url,
    thumb: shot.thumbUrl,
    caption: shot.caption ?? undefined
  }))
);
const description = computed(
  () =>
    detail.value?.description ??
    detail.value?.summary ??
    (local.value ? pick(local.value.summary, '', local.value.sourceLocale) : '')
);
const command = computed(() => detail.value?.installCommand ?? installCommand(kind.value, token.value));

const messages = useUiMessages();

const info = computed<OnKeyValueItem[]>(() => {
  const value = detail.value;
  const items: OnKeyValueItem[] = [{ key: 'token', label: 'Token', value: token.value, mono: true }];
  const homepage = value?.homepage ?? local.value?.homepage;
  if (homepage)
    items.push({
      key: 'homepage',
      label: t('package.info.homepage'),
      value: displayUrl(homepage),
      href: homepage
    });
  const binaries = value?.artifacts.binaries ?? local.value?.binaries ?? [];
  if (binaries.length)
    items.push({
      key: 'binaries',
      label: t('package.info.binaries'),
      value: binaries.join(' '),
      mono: true
    });
  if (kind.value === 'cask')
    items.push({
      key: 'auto',
      label: t('package.info.autoUpdates'),
      value: t((value?.autoUpdates ?? local.value?.autoUpdates) ? 'package.info.autoYes' : 'package.info.autoNo')
    });
  if (!value) return items;
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
    items.push({
      key: 'rosetta',
      label: t('package.arch.arm64'),
      value: messages.value.cask.rosetta
    });
  if (value.platforms?.some(platform => platform.version !== value.version))
    items.push({
      key: 'platformVersions',
      label: messages.value.cask.version,
      value: messages.value.cask.legacy
    });
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
    items.push({
      key: 'macos',
      label: messages.value.cask.requirements,
      value: requirements.join(' · ')
    });
  } else if (value.minMacos)
    items.push({
      key: 'macos',
      label: t('package.info.minMacos'),
      value: `macOS ${value.minMacos}`
    });
  if (value.license) items.push({ key: 'license', label: t('package.info.license'), value: value.license });
  return items;
});

const relatedItems = computed<OnRelatedPackage[]>(() =>
  (related.value ?? []).map(pkg => ({
    key: `${pkg.kind}/${pkg.token}`,
    kind: pkg.kind,
    token: pkg.token,
    name: pkg.displayName,
    src: pkg.iconUrl,
    accent: pkg.accentColor,
    description: pkg.summary,
    href: `/package/${pkg.kind}/${pkg.token}`
  }))
);

function onCopied(text: string) {
  toasts.push({ tone: 'success', title: t('common.copied'), description: text });
}
</script>

<template>
  <OnPackageOverview
    :screenshots="shots"
    :screenshots-label="t('package.screenshots')"
    :has-about="Boolean(description)"
    :about-machine-translated="Boolean(detail?.machineTranslated)"
    :caveats="detail?.caveats"
    :caveats-title="t('package.caveats')"
    :command="command"
    :info="info"
    :related="relatedItems"
    :related-title="t('package.related')"
    :link-as="RouterLink"
    @copied="onCopied"
  >
    <template #about>
      <OnMarkdown size="md" :heading-level="3" :source="description" />
    </template>
    <template #main>
      <p v-if="!detail" class="m-0 text-13px text-ink-tertiary">{{ t('package.offline') }}</p>
      <LatestRelease
        v-if="detail?.latestRelease"
        :release="detail.latestRelease"
        :total="detail.releaseCount"
        :versions-to="`/package/${kind}/${token}/versions`"
      />
    </template>
    <template #aside>
      <MonthlyInstallsCard v-if="detail" :installs="detail.installs" />
    </template>
  </OnPackageOverview>
</template>
