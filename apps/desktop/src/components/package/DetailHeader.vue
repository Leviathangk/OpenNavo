<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { PackageDetail } from '@opennavo/api';
import { formatDate, isThirdPartyTap } from '@opennavo/shared';
import {
  architectures,
  displayUrl,
  OnButton,
  OnIcon,
  OnMenu,
  OnPackageHeader,
  toPackageRepoSignals
} from '@opennavo/ui';
import type { OnPackageHeaderChip } from '@opennavo/ui';
import PackageGetButton from './PackageGetButton.vue';
import type { Kind } from '@/ipc/bindings';
import type { LocalItem } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageMenu } from '@/composables/usePackageMenu';
import { usePackageState } from '@/composables/usePackageState';
import { useBrewfileStore } from '@/stores/brewfile';
import { useLibraryStore, useUpdatesStore } from '@/stores';
import { useLinksStore } from '@/stores/links';
import { useToasts } from '@/composables/useToasts';

// Detail header (mockup 03): shared OnPackageHeader (ADR-017) plus local badges (installed version, pinned).
// Right side: update/get/open by local state, extra Open for updatable apps, More menu; sources and install date below.
const props = defineProps<{
  kind: Kind;
  token: string;
  detail?: PackageDetail;
  local?: LocalItem | null;
}>();

const { t } = useI18n();
const { pick, appLocale } = useAppLocale();
const brewfile = useBrewfileStore();
const library = useLibraryStore();
const updates = useUpdatesStore();
const { stateOf, act, errorText } = usePackageState();

const name = computed(
  () =>
    props.detail?.displayName ??
    (props.local ? pick(props.local.displayName, props.local.name, props.local.sourceLocale) : props.token)
);
const summary = computed(
  () => props.detail?.summary ?? (props.local ? pick(props.local.summary, '', props.local.sourceLocale) : '')
);
const subtitle = computed(() => [props.detail?.developer, summary.value].filter(Boolean).join(' · '));
const iconUrl = computed(() => props.detail?.iconUrl ?? props.local?.iconUrl ?? null);
const accent = computed(() => props.detail?.accentColor ?? props.local?.accentColor ?? null);
const arch = computed(() =>
  props.detail
    ? architectures(props.detail.supports)
        .map(item => t(`package.arch.${item}`))
        .join(' · ')
    : ''
);
const repoSignals = computed(() =>
  toPackageRepoSignals(props.detail, {
    repoUrl: props.detail?.openSource?.url ?? props.detail?.repoUrl,
    archived: props.detail?.openSource?.archived
  })
);

const installed = computed(() => library.find(props.kind, props.token));
const outdated = computed(() => updates.find(props.kind, props.token));
const state = computed(() => stateOf(props.kind, props.token, Boolean(props.detail?.disable ?? props.local?.disabled)));
const latest = computed(() => outdated.value?.currentVersion ?? props.detail?.version ?? props.local?.version ?? '');

const installedChip = computed(() => {
  const item = installed.value;
  if (!item) return null;
  return item.status === 'self_updated'
    ? t('package.installedSelfUpdated', { version: item.actualVersion ?? item.installedVersion })
    : t('package.installed', { version: item.installedVersion });
});

const chips = computed<OnPackageHeaderChip[]>(() => {
  const link = props.detail?.link ?? props.local?.link;
  const list: OnPackageHeaderChip[] = [
    props.kind === 'link'
      ? {
          key: 'kind',
          label: t(link?.downloadUrl || !link?.github ? 'package.link.chip' : 'package.link.chipGithub'),
          tone: 'success'
        }
      : { key: 'kind', label: props.kind === 'cask' ? 'Cask' : 'Formula' }
  ];
  if (link) list.push({ key: 'pricing', label: t(`package.link.pricing.${link.pricing}`) });
  const category = props.detail?.primaryCategory;
  if (category) list.push({ key: 'category', label: category.name });
  if (isThirdPartyTap(props.detail?.tap ?? props.local?.tap))
    list.push({ key: 'tap', label: t('package.thirdPartyTap'), tone: 'warning', outline: true });
  if (props.detail?.autoUpdates ?? props.local?.autoUpdates)
    list.push({ key: 'auto', label: t('package.autoUpdates'), tone: 'info' });
  if (arch.value) list.push({ key: 'arch', label: arch.value });
  if (props.detail?.openSource?.archived || repoSignals.value?.archived)
    list.push({ key: 'archived', label: t('package.repo.archived'), tone: 'danger', outline: true });
  if (installedChip.value) list.push({ key: 'installed', label: installedChip.value, tone: 'success', dot: true });
  if (installed.value?.pinned) list.push({ key: 'pinned', label: t('package.pinned') });
  if (props.detail?.disable ?? props.local?.disabled)
    list.push({ key: 'disabled', label: t('package.disabled'), tone: 'danger', outline: true });
  else if (props.detail?.deprecation ?? props.local?.deprecated)
    list.push({
      key: 'deprecated',
      label: t('package.deprecated'),
      tone: 'warning',
      outline: true
    });
  return list;
});

// Link apps say where the download comes from: the official domain or the GitHub project, never OpenNavo.
const source = computed(() => {
  const link = props.detail?.link ?? props.local?.link;
  if (!link) return null;
  if (link.downloadUrl) return displayUrl(link.downloadUrl).split('/')[0];
  return link.github
    ? `github.com/${link.github.repo}`
    : link.homepage
      ? displayUrl(link.homepage).split('/')[0]
      : null;
});

const meta = computed(() => {
  const parts: string[] = [];
  if (props.kind === 'link')
    return source.value ? t('package.link.meta', { source: source.value }) : t('package.link.notHosted');
  if (props.detail?.tap) parts.push(`${t('package.fromTap')} ${props.detail.tap}`);
  if (installed.value?.installedAt)
    parts.push(
      t('package.installedAt', {
        date: formatDate(installed.value.installedAt, { locale: appLocale.value })
      })
    );
  return parts.join(' · ');
});

const links = useLinksStore();
const toasts = useToasts();
async function openHomepage() {
  try {
    await links.open(props.token, 'homepage');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

const menu = usePackageMenu();
const menuItems = computed(() => menu.itemsFor(props.kind, props.token));
const onSelect = (key: string) => menu.select(props.kind, props.token, name.value, key, props.detail?.installCommand);
</script>

<template>
  <OnPackageHeader
    :kind="kind"
    :token="token"
    :name="name"
    :subtitle="subtitle"
    :icon-url="iconUrl"
    :accent="accent"
    :chips="chips"
    :meta="meta"
  >
    <template #actions>
      <OnButton
        v-if="kind === 'cask'"
        variant="secondary"
        :icon="brewfile.has(kind, token) ? 'check' : 'plus'"
        :disabled="brewfile.has(kind, token)"
        @click="brewfile.add(kind, token)"
        >{{ t(brewfile.has(kind, token) ? 'brewfile.inList' : 'brewfile.add') }}</OnButton
      >
      <PackageGetButton
        appearance="button"
        :kind="kind"
        :token="token"
        :disabled="Boolean(detail?.disable ?? local?.disabled)"
        :label="state.state === 'update' ? t('package.updateTo', { version: latest }) : undefined"
      />
      <OnButton
        v-if="state.state === 'update' && kind === 'cask'"
        variant="secondary"
        @click="act(kind, token, 'open')"
      >
        {{ t('package.open') }}
      </OnButton>
      <!-- The arrow follows the label, as on Download beside it: both leave OpenNavo for the developer's site. -->
      <OnButton
        v-if="kind === 'link' && (detail?.link ?? local?.link)?.homepage"
        variant="secondary"
        @click="openHomepage"
      >
        {{ t('package.link.homepage') }}
        <OnIcon name="arrow-up-right" :size="15" />
      </OnButton>
      <OnMenu
        v-if="kind !== 'link'"
        :items="menuItems"
        :label="t('package.actions.label')"
        variant="secondary"
        @select="onSelect"
      />
    </template>
  </OnPackageHeader>
</template>
