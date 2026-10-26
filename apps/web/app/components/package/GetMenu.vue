<script setup lang="ts">
import { OnButton, OnGetButton, OnMenu, useUiMessages } from '@opennavo/ui';
import type { OnMenuItem } from '@opennavo/ui';
import type { PackageKind } from '@opennavo/shared';
import { trackEvent } from '~/utils/analytics';
import { linkDownloadUrl } from '~/utils/linkApp';
import type { LinkInfo } from '~/utils/linkApp';
import { packagePath } from '~/utils/packageView';

// Web Get menu (05 §7): open in OpenNavo / copy install command / add to Brewfile. Link apps (store/security
// plan §4) have none of these: Download opens the developer's address, or the app's page when the list lacks it.
const props = withDefaults(
  defineProps<{
    kind: PackageKind;
    token: string;
    name: string;
    /** API install command; derive from kind/token when omitted. */
    command?: string;
    /** Download addresses of a link app, when the list item carries them. */
    link?: LinkInfo | null;
    /** chip is the small card pill; primary is the hero/detail main button. */
    variant?: 'chip' | 'primary';
  }>(),
  { variant: 'chip', link: null }
);

const { t } = useI18n();
const messages = useUiMessages();
const localePath = useLocalePath();
const toasts = useToasts();
const { open } = useOpenInApp();
const { copy } = useCopyCommand();
const brewfile = useBrewfileList();

const inList = computed(() => brewfile.has(props.kind, props.token));

const items = computed<OnMenuItem[]>(() => [
  { key: 'open', label: messages.value.action.openInApp, icon: 'arrow-up-right' },
  { key: 'copy', label: messages.value.action.copyInstallCommand, icon: 'copy' },
  {
    key: 'brewfile',
    label: inList.value ? messages.value.action.inBrewfile : messages.value.action.addToBrewfile,
    icon: inList.value ? 'check' : 'plus',
    disabled: inList.value
  }
]);

function download() {
  trackEvent('get_app');
  const url = props.link ? linkDownloadUrl(props.link) : undefined;
  if (url) window.open(url, '_blank', 'noopener,noreferrer');
  else void navigateTo(localePath(packagePath(props.token)));
}

function onSelect(key: string) {
  if (key === 'open') open({ kind: props.kind, token: props.token, name: props.name });
  else if (key === 'copy') void copy(props.kind, props.token, props.command);
  else if (key === 'brewfile') {
    const result = brewfile.add(props.kind, props.token);
    if (result === 'added') toasts.push({ tone: 'success', title: t('toast.addedToBrewfile', { name: props.name }) });
    else if (result === 'full') toasts.push({ tone: 'warning', title: t('toast.brewfileFull') });
  }
}
</script>

<template>
  <OnGetButton
    v-if="kind === 'link'"
    state="download"
    :appearance="variant === 'primary' ? 'button' : 'compact'"
    :aria-label="t('get.downloadLabel', { name })"
    @click="download"
  />
  <OnMenu v-else :items="items" :label="t('get.menuLabel', { name })" @select="onSelect">
    <template #trigger="{ attrs }">
      <OnGetButton
        v-if="variant === 'chip'"
        state="get"
        v-bind="attrs"
        :aria-label="t('get.label', { name })"
        @click="trackEvent('get_app')"
      />
      <OnButton
        v-else
        :aria-label="t('get.label', { name })"
        variant="primary"
        shape="round"
        icon="download"
        v-bind="attrs"
        @click="trackEvent('get_app')"
      >
        {{ messages.getButton.get }}
      </OnButton>
    </template>
  </OnMenu>
</template>
