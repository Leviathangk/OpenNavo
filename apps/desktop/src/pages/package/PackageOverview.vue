<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { installCommand, withRecentReturn } from '@opennavo/shared';
import { architectures, displayUrl, OnPackageOverview, useUiMessages } from '@opennavo/ui';
import type { OnKeyValueItem, OnRelatedPackage } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import ArticleCard from '@/components/article/ArticleCard.vue';
import LatestRelease from '@/components/package/LatestRelease.vue';
import LinkDownloadPanel from '@/components/package/LinkDownloadPanel.vue';
import MasAlsoPanel from '@/components/appstore/MasAlsoPanel.vue';
import DownloadVerdictBanner from '@/components/package/DownloadVerdictBanner.vue';
import type { LinkInfo, LocalizedText } from '@/ipc/bindings';
import MonthlyInstallsCard from '@/components/package/MonthlyInstallsCard.vue';
import { api } from '@/api';
import { useAppLocale } from '@/composables/useAppLocale';
import { useRemoteLoader } from '@/composables/useRemoteLoader';
import { usePackage } from '@/composables/usePackageDetail';
import { useToasts } from '@/composables/useToasts';

// Overview (mockup 03): shared OnPackageOverview (ADR-017); left: screenshots → introduction → latest release → caveats.
// Right: install command → information → monthly installs → similar apps. Offline shows only the local summary and install command.
const route = useRoute();
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
// Link apps have no install command; their page shows download links instead (store/security plan §4).
const command = computed(() => {
  const value = kind.value;
  return value === 'link' ? '' : (detail.value?.installCommand ?? installCommand(value, token.value, local.value?.tap));
});

const messages = useUiMessages();

// Bundle IDs that can match an App Store listing: link apps register theirs, and for casks the server names the
// listing it found. The panel asks Apple itself, so price and availability follow this Mac's storefront.
const storeBundleIds = computed(() => [
  ...(detail.value?.appStore?.bundleId ? [detail.value.appStore.bundleId] : []),
  ...(detail.value?.bundleIds ?? local.value?.bundleIds ?? [])
]);

// Link apps (store/security plan §4): where the download comes from and how to tell a genuine copy.
// The API allows null translations where the local catalog drops them; normalize to the catalog's shape.
const link = computed<LinkInfo | null>(() => {
  const remote = detail.value?.link;
  if (!remote) return local.value?.link ?? null;
  const notes = remote.notes
    ? (Object.fromEntries(Object.entries(remote.notes).filter(([, text]) => Boolean(text))) as LocalizedText)
    : null;
  return {
    ...remote,
    notes,
    github: remote.github ? { repo: remote.github.repo, latest: remote.github.latest ?? null } : null
  };
});
const linkInfo = computed<OnKeyValueItem[]>(() => {
  const value = link.value;
  if (!value) return [];
  const items: OnKeyValueItem[] = [];
  if (value.homepage)
    items.push({
      key: 'homepage',
      label: t('package.info.homepage'),
      value: displayUrl(value.homepage),
      href: value.homepage
    });
  if (value.github)
    items.push({
      key: 'github',
      label: 'GitHub',
      value: value.github.repo,
      href: `https://github.com/${value.github.repo}`
    });
  if (value.teamId) items.push({ key: 'team', label: t('package.link.teamId'), value: value.teamId, mono: true });
  items.push({
    key: 'pricing',
    label: t('package.link.pricingLabel'),
    value: t(`package.link.pricing.${value.pricing}`)
  });
  const bundleIds = detail.value?.bundleIds ?? local.value?.bundleIds ?? [];
  if (bundleIds.length) items.push({ key: 'bundle', label: 'Bundle ID', value: bundleIds.join(' '), mono: true });
  return items;
});

const info = computed<OnKeyValueItem[]>(() => {
  if (kind.value === 'link') return linkInfo.value;
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
  <!-- After a link app's Download: whether the file is the developer's, notarized and clean (plan §4, mockup A). -->
  <DownloadVerdictBanner v-if="kind === 'link'" class="mb-14px" :token="token" />
  <OnPackageOverview
    :screenshots="shots"
    :screenshots-label="t('package.screenshots')"
    :has-about="Boolean(description)"
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
        :versions-to="withRecentReturn(`/package/${kind}/${token}/versions`, route.query.returnTo)"
      />
      <section v-if="detail?.articles?.length" class="mt-24px" aria-labelledby="package-articles">
        <h2 id="package-articles" class="m-0 text-15px font-600 text-ink-primary">{{ t('articles.related') }}</h2>
        <ArticleCard v-for="card in detail.articles" :key="card.slug" :card="card" compact :heading-level="3" />
      </section>
    </template>
    <template v-if="kind === 'link' && link" #download>
      <LinkDownloadPanel :token="token" :link="link" />
    </template>
    <template #aside>
      <MonthlyInstallsCard v-if="detail && kind !== 'link'" :installs="detail.installs" />
      <MasAlsoPanel v-if="kind !== 'formula'" :bundle-ids="storeBundleIds" />
    </template>
  </OnPackageOverview>
</template>
