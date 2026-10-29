<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { unwrap } from '@opennavo/api';
import { withRecentReturn } from '@opennavo/shared';
import { catalogInstallCommand } from '@/utils/brew';
import { OnPackageOverview, OnPackageSpecCard } from '@opennavo/ui';
import type { OnRelatedPackage } from '@opennavo/ui';
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
import { packageSpec } from '@/utils/packageSpec';
import { useLibraryStore } from '@/stores';

// Overview (mockup 03): shared OnPackageOverview (ADR-017); left: screenshots → introduction → latest release → caveats.
// Right: install command → information → monthly installs → similar apps. Offline shows only the local summary and install command.
const route = useRoute();
const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const toasts = useToasts();
const library = useLibraryStore();
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
  return value === 'link'
    ? ''
    : (detail.value?.installCommand ?? catalogInstallCommand(value, token.value, local.value));
});

const installed = computed(() => library.find(kind.value, token.value));
const spec = computed(() =>
  detail.value
    ? packageSpec(detail.value, local.value, installed.value, t, appLocale.value)
    : { rows: [], checkedAt: null, repoSignals: null }
);

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
    :info="[]"
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
    <template #spec>
      <OnPackageSpecCard
        :rows="spec.rows"
        :title="t('package.spec.title')"
        :checked-at="spec.checkedAt"
        :checked-label="t('package.spec.checked')"
      />
    </template>
    <template #aside>
      <MonthlyInstallsCard v-if="detail && kind !== 'link'" :installs="detail.installs" />
      <MasAlsoPanel v-if="kind !== 'formula'" :bundle-ids="storeBundleIds" />
    </template>
  </OnPackageOverview>
</template>
