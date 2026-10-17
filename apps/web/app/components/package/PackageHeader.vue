<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { architectures, OnButton, OnIcon, OnPackageHeader, useUiMessages } from '@opennavo/ui';
import type { OnPackageHeaderChip } from '@opennavo/ui';
import { trackEvent } from '~/utils/analytics';
import { httpsUrl, linkDownloadUrl, linkSource } from '~/utils/linkApp';

// Detail header (08 §10.5, 03-detail): shared OnPackageHeader (ADR-017), web Get menu/homepage right.
// Link apps (store/security plan §4) swap the Get menu for Download, which opens the developer's own address.
const props = defineProps<{ pkg: PackageDetail }>();

const { t } = useI18n();
const messages = useUiMessages();

const isLink = computed(() => props.pkg.kind === 'link');
const link = computed(() => (isLink.value ? (props.pkg.link ?? null) : null));
const downloadUrl = computed(() => (link.value ? linkDownloadUrl(link.value) : undefined));
const homepage = computed(() =>
  isLink.value ? httpsUrl(link.value?.homepage ?? props.pkg.homepage) : props.pkg.homepage
);
const meta = computed(() => {
  if (!isLink.value) return `${t('package.fromTap')} ${props.pkg.tap}`;
  const source = link.value ? linkSource(link.value) : undefined;
  return source ? t('package.link.meta', { source }) : t('package.link.notHosted');
});

const subtitle = computed(() => [props.pkg.developer, props.pkg.summary].filter(Boolean).join(' · '));
const arch = computed(() =>
  architectures(props.pkg.supports)
    .map(item => t(`package.arch.${item}`))
    .join(' · ')
);
const unavailable = computed(() => Boolean(props.pkg.disable));

const chips = computed<OnPackageHeaderChip[]>(() => {
  const kind = isLink.value
    ? t(link.value?.downloadUrl || !link.value?.github ? 'package.link.chip' : 'package.link.chipGithub')
    : props.pkg.kind === 'cask'
      ? 'Cask'
      : 'Formula';
  const list: OnPackageHeaderChip[] = [{ key: 'kind', label: kind }];
  if (props.pkg.primaryCategory) list.push({ key: 'category', label: props.pkg.primaryCategory.name });
  if (link.value) list.push({ key: 'pricing', label: t(`package.link.pricing.${link.value.pricing}`) });
  if (props.pkg.autoUpdates) list.push({ key: 'auto', label: t('package.autoUpdates'), tone: 'info' });
  if (arch.value) list.push({ key: 'arch', label: arch.value });
  if (props.pkg.disable) list.push({ key: 'disabled', label: t('package.disabled'), tone: 'danger', outline: true });
  else if (props.pkg.deprecation)
    list.push({ key: 'deprecated', label: t('package.deprecated'), tone: 'warning', outline: true });
  return list;
});
</script>

<template>
  <OnPackageHeader
    :kind="pkg.kind"
    :token="pkg.token"
    :name="pkg.displayName"
    :subtitle="subtitle"
    :icon-url="pkg.iconUrl"
    :accent="pkg.accentColor"
    :chips="chips"
    :meta="meta"
    priority
  >
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
      <OnButton v-else-if="unavailable" variant="primary" shape="round" icon="download" disabled>
        {{ messages.getButton.get }}
      </OnButton>
      <GetMenu
        v-else
        variant="primary"
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
