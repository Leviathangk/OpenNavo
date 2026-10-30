<script setup lang="ts">
import { packageInstallState } from '@opennavo/shared';
import { OnButton, OnIcon, OnPackageHeader, toPackageRepoSignals, useUiMessages } from '@opennavo/ui';
import type { OnPackageHeaderChip } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { httpsUrl, linkDownloadUrl, linkSource } from '~/utils/linkApp';
import { officialSource } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';
import PackageSourceBadge from './PackageSourceBadge.vue';

// Detail header (08 §10.5, 03-detail): shared OnPackageHeader (ADR-017), web Get menu/homepage right.
// Link apps (store/security plan §4) swap the Get menu for Download, which opens the developer's own address.
// T1: the categories are real links, and an app OpenNavo paused (no new version for 90 days, still offered by
// Homebrew) reads "paused", never "disabled", which only Homebrew's own disable earns.
const props = defineProps<{ pkg: PackagePage; headingSuffix?: string }>();

const { t } = useI18n();
const messages = useUiMessages();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const isLink = computed(() => props.pkg.kind === 'link');
const link = computed(() => (isLink.value ? (props.pkg.link ?? null) : null));
const downloadUrl = computed(() => (link.value ? linkDownloadUrl(link.value) : undefined));
const homepage = computed(() =>
  isLink.value ? httpsUrl(link.value?.homepage ?? props.pkg.homepage) : props.pkg.homepage
);
const meta = computed(() => {
  if (!isLink.value) return '';
  const source = link.value ? linkSource(link.value) : undefined;
  return source ? t('package.link.meta', { source }) : t('package.link.notHosted');
});

const subtitle = computed(() => [props.pkg.developer, props.pkg.summary].filter(Boolean).join(' · '));
const repoSignals = computed(() => toPackageRepoSignals(props.pkg));
const installState = computed(() => packageInstallState(props.pkg));
const unavailable = computed(() => installState.value === 'disabled');
const source = computed(() => officialSource(props.pkg));
// Pair actions in equal columns only when the header actually has Get/Download and Homepage.
const actionCount = computed(() => {
  if (isLink.value) return Number(Boolean(downloadUrl.value)) + Number(Boolean(homepage.value));
  return 1 + Number(Boolean(homepage.value));
});
const stretchActions = computed(() => actionCount.value === 2);

const chips = computed<OnPackageHeaderChip[]>(() => {
  const kind = isLink.value
    ? t(link.value?.downloadUrl || !link.value?.github ? 'package.link.chip' : 'package.link.chipGithub')
    : props.pkg.kind === 'cask'
      ? 'Cask'
      : 'Formula';
  const list: OnPackageHeaderChip[] = [{ key: 'kind', label: kind }];
  // The primary category first, then the others, each linking to its category page.
  const primary = props.pkg.primaryCategory;
  const categories = [
    ...(primary ? [primary] : []),
    ...(props.pkg.categories ?? []).filter(item => item.slug !== primary?.slug)
  ];
  for (const category of categories)
    list.push({
      key: `category:${category.slug}`,
      label: category.name,
      href: localePath(`/categories/${category.slug}`)
    });
  if (link.value && link.value.pricing !== 'unknown')
    list.push({ key: 'pricing', label: t(`package.link.pricing.${link.value.pricing}`) });
  if (props.pkg.autoUpdates) list.push({ key: 'auto', label: t('package.autoUpdates'), tone: 'info' });
  if (repoSignals.value?.archived)
    list.push({ key: 'archived', label: t('package.repo.archived'), tone: 'danger', outline: true });
  if (installState.value !== 'active' && !source.value)
    list.push({
      key: installState.value,
      label: t(`package.${installState.value}`),
      tone: unavailable.value ? 'danger' : 'warning',
      outline: true
    });
  return list;
});
</script>

<template>
  <OnPackageHeader
    :kind="pkg.kind"
    :token="pkg.token"
    :name="pkg.displayName"
    :heading-suffix="headingSuffix"
    :subtitle="subtitle"
    :icon-url="pkg.iconUrl"
    :icon-variants="pkg.iconVariants"
    :accent="pkg.accentColor"
    :chips="chips"
    :meta="meta"
    :link-as="NuxtLink"
    :stretch-actions="stretchActions"
    priority
  >
    <template #tags>
      <PackageSourceBadge v-if="source" :pkg="pkg" />
    </template>
    <template #actions>
      <template v-if="isLink">
        <OnButton
          v-if="downloadUrl"
          variant="primary"
          shape="round"
          :href="downloadUrl"
          target="_blank"
          rel="noopener noreferrer"
          @click="trackEvent('get_app')"
        >
          {{ t('package.link.goDownload') }}
          <OnIcon name="arrow-up-right" :size="15" />
        </OnButton>
      </template>
      <OnButton
        v-else-if="unavailable"
        :aria-label="t('get.label', { name: pkg.displayName })"
        variant="primary"
        shape="round"
        icon="download"
        disabled
      >
        {{ messages.getButton.get }}
      </OnButton>
      <GetMenu
        v-else
        variant="primary"
        block
        :kind="pkg.kind"
        :token="pkg.token"
        :name="pkg.displayName"
        :command="pkg.installCommand"
      />
      <OnButton v-if="homepage" shape="round" :href="homepage" target="_blank" rel="noopener noreferrer">
        {{ t('package.homepage') }}
        <OnIcon name="arrow-up-right" :size="15" />
      </OnButton>
    </template>
  </OnPackageHeader>
</template>
