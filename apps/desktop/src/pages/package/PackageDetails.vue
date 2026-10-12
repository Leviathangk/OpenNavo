<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { installCommand } from '@opennavo/shared';
import {
  displayUrl,
  OnButton,
  OnCaskArtifacts,
  OnCaskPlatformSelect,
  OnInstallCommand,
  OnKeyValueList,
  selectedCaskPlatform,
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
const { kind, token, detail, error } = usePackage();

const messages = useUiMessages();
const platformTag = ref('');
const platform = computed(() => (detail.value ? selectedCaskPlatform(detail.value, platformTag.value) : undefined));
const artifacts = computed(() => platform.value?.artifacts ?? detail.value?.artifacts);
const downloadUrl = computed(() => (platform.value ? platform.value.downloadUrl : detail.value?.downloadUrl));
const downloadSha = computed(() => (platform.value ? platform.value.downloadSha256 : detail.value?.downloadSha256));

const items = computed<OnKeyValueItem[]>(() => {
  const value = detail.value;
  if (!value) return [];
  const effectiveArtifacts = platform.value?.artifacts ?? value.artifacts;
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
  else if (!artifacts.value?.entries?.length && artifacts.value?.apps.length)
    list.push({
      key: 'apps',
      label: messages.value.cask.source,
      value: artifacts.value.apps.join(' · '),
      mono: true
    });
  if (effectiveArtifacts.pkgs.length)
    list.push({
      key: 'pkgs',
      label: t('package.details.pkgs'),
      value: effectiveArtifacts.pkgs.join('  '),
      mono: true
    });
  if (effectiveArtifacts.binaries.length)
    list.push({
      key: 'binaries',
      label: t('package.info.binaries'),
      value: effectiveArtifacts.binaries.join(' '),
      mono: true
    });
  if (effectiveArtifacts.uninstallNotes?.length)
    list.push({
      key: 'uninstall',
      label: t('package.info.uninstall'),
      value: effectiveArtifacts.uninstallNotes.join('；')
    });
  if (value.kegOnly)
    list.push({
      key: 'keg',
      label: t('package.details.kegOnly'),
      value: t('package.details.kegOnlyValue')
    });
  if (downloadUrl.value)
    list.push({
      key: 'download',
      label: t('package.details.download'),
      value: displayUrl(downloadUrl.value),
      href: downloadUrl.value
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

const command = computed(() => detail.value?.installCommand ?? installCommand(kind.value, token.value));

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
      <OnCaskPlatformSelect
        v-if="detail.platforms?.length"
        :model-value="platform?.tag"
        :platforms="detail.platforms"
        @update:model-value="platformTag = $event"
      />
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px">
        <OnKeyValueList :items="items" />
        <div v-if="downloadSha" class="flex justify-end pb-10px">
          <OnButton size="xs" icon="copy" @click="copySha">{{ t('common.copy') }} SHA-256</OnButton>
        </div>
      </section>
      <OnCaskArtifacts v-if="artifacts?.entries?.length" :entries="artifacts.entries" />
      <section
        v-if="detail.caveats"
        class="rounded-big border border-solid border-status-warning bg-surface-card px-18px py-14px"
      >
        <h2 class="m-0 text-13.5px font-600 text-status-warning">{{ t('package.caveats') }}</h2>
        <pre class="m-0 mt-8px whitespace-pre-wrap font-mono text-12px leading-[1.6] text-ink-secondary">{{
          detail.caveats
        }}</pre>
      </section>
    </div>
    <aside class="min-w-0">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px">
        <OnInstallCommand :command="command" @copied="onCopied" />
      </section>
    </aside>
  </div>
</template>
