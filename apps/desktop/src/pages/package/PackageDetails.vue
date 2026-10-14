<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { installCommand, isThirdPartyTap } from '@opennavo/shared';
import {
  displayUrl,
  OnButton,
  OnCaskArtifacts,
  OnCodeText,
  OnInstallCommand,
  OnKeyValueList,
  useUiMessages
} from '@opennavo/ui';
import type { OnKeyValueItem } from '@opennavo/ui';
import OfflineNotice from '@/components/common/OfflineNotice.vue';
import { usePackage } from '@/composables/usePackageDetail';
import { useToasts } from '@/composables/useToasts';
import { useLibraryStore } from '@/stores';

// Installation details (08 §10.8): repository, install location (actual path when installed), bundled commands, uninstall behavior, download URLs/checksums, definition file.
const { t } = useI18n();
const toasts = useToasts();
const library = useLibraryStore();
const { kind, token, detail, local, error } = usePackage();

const messages = useUiMessages();
const downloadSha = computed(() => detail.value?.downloadSha256);

const items = computed<OnKeyValueItem[]>(() => {
  const value = detail.value;
  if (!value) return [];
  const artifacts = value.artifacts;
  const installed = library.find(kind.value, token.value);
  const list: OnKeyValueItem[] = [{ key: 'tap', label: t('package.details.tap'), value: value.tap, mono: true }];
  if (value.names.length > 1)
    list.push({ key: 'names', label: t('package.details.names'), value: value.names.join(' · ') });
  const appPaths = installed?.appPaths ?? [];
  if (appPaths.length)
    list.push({
      key: 'apps',
      label: t('package.details.location'),
      value: appPaths.join('  '),
      mono: true
    });
  else if (!artifacts.entries?.length && artifacts.apps.length)
    list.push({
      key: 'apps',
      label: messages.value.cask.source,
      value: artifacts.apps.join(' · '),
      mono: true
    });
  if (artifacts.pkgs.length)
    list.push({
      key: 'pkgs',
      label: t('package.details.pkgs'),
      value: artifacts.pkgs.join('  '),
      mono: true
    });
  if (artifacts.binaries.length)
    list.push({
      key: 'binaries',
      label: t('package.info.binaries'),
      value: artifacts.binaries.join(' '),
      mono: true
    });
  if (artifacts.uninstallNotes?.length)
    list.push({
      key: 'uninstall',
      label: t('package.info.uninstall'),
      value: artifacts.uninstallNotes.join('；')
    });
  if (value.kegOnly)
    list.push({
      key: 'keg',
      label: t('package.details.kegOnly'),
      value: t('package.details.kegOnlyValue')
    });
  if (value.downloadUrl)
    list.push({
      key: 'download',
      label: t('package.details.download'),
      value: displayUrl(value.downloadUrl),
      href: value.downloadUrl
    });
  // 08 §10.8: truncate displayed SHA-256; the button below copies the full value.
  if (downloadSha.value)
    list.push({
      key: 'sha256',
      label: 'SHA-256',
      value: `${downloadSha.value.slice(0, 16)}…`,
      mono: true
    });
  list.push({
    key: 'source',
    label: t('package.details.source'),
    value: displayUrl(value.sourceUrl),
    href: value.sourceUrl
  });
  // formulae.brew.sh only lists Homebrew's own taps.
  if (!isThirdPartyTap(value.tap))
    list.push({
      key: 'formulae',
      label: 'formulae.brew.sh',
      value: displayUrl(value.formulaeUrl),
      href: value.formulaeUrl
    });
  if (value.repoUrl)
    list.push({
      key: 'repo',
      label: t('package.details.repo'),
      value: displayUrl(value.repoUrl),
      href: value.repoUrl
    });
  return list;
});

const command = computed(
  () => detail.value?.installCommand ?? installCommand(kind.value, token.value, local.value?.tap)
);

function onCopied(text: string) {
  toasts.push({ tone: 'success', title: t('common.copied'), description: text });
}

async function copySha() {
  const sha = downloadSha.value;
  if (!sha) return;
  await navigator.clipboard.writeText(sha);
  toasts.push({ tone: 'success', title: t('common.copied'), description: 'SHA-256' });
}
</script>

<template>
  <OfflineNotice v-if="error && !detail" />
  <div v-else-if="detail" class="grid grid-cols-[minmax(0,1fr)_320px] gap-20px pt-4px">
    <div class="flex min-w-0 flex-col gap-16px">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px">
        <OnKeyValueList :items="items" />
        <div v-if="downloadSha" class="flex justify-end pb-10px">
          <OnButton size="xs" icon="copy" @click="copySha">{{ t('common.copy') }} SHA-256</OnButton>
        </div>
      </section>
      <OnCaskArtifacts v-if="detail.artifacts.entries?.length" :entries="detail.artifacts.entries" />
      <section
        v-if="detail.caveats"
        class="rounded-big border border-solid border-status-warning bg-surface-card px-18px py-14px"
      >
        <h2 class="m-0 text-13.5px font-600 text-status-warning">{{ t('package.caveats') }}</h2>
        <pre class="m-0 mt-8px whitespace-pre-wrap font-mono text-12px leading-[1.6] text-ink-secondary"><OnCodeText
          :text="detail.caveats"
        /></pre>
      </section>
    </div>
    <aside class="min-w-0">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px">
        <OnInstallCommand :command="command" @copied="onCopied" />
      </section>
    </aside>
  </div>
</template>
