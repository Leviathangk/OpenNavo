<script setup lang="ts">
import { isThirdPartyTap } from '@opennavo/shared';
import type { PackageKind } from '@opennavo/shared';
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

// Installation details (08 §10.8): location, bundled commands, uninstall behavior, caveats, download URLs, definition/formulae.brew.sh links.
const props = defineProps<{ kind: PackageKind }>();

const { t } = useI18n();
const toasts = useToasts();
const { data: pkg, token } = await usePackage(props.kind);

const messages = useUiMessages();
const downloadSha = computed(() => pkg.value?.downloadSha256);

const items = computed<OnKeyValueItem[]>(() => {
  const value = pkg.value;
  if (!value) return [];
  const artifacts = value.artifacts;
  const list: OnKeyValueItem[] = [{ key: 'tap', label: t('package.details.tap'), value: value.tap, mono: true }];
  if (value.names.length > 1)
    list.push({ key: 'names', label: t('package.details.names'), value: value.names.join(' · ') });
  if (!artifacts.entries?.length && artifacts.apps.length)
    list.push({ key: 'apps', label: messages.value.cask.source, value: artifacts.apps.join(' · '), mono: true });
  if (artifacts.pkgs.length)
    list.push({ key: 'pkgs', label: t('package.details.pkgs'), value: artifacts.pkgs.join('  '), mono: true });
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
    list.push({ key: 'keg', label: t('package.details.kegOnly'), value: t('package.details.kegOnlyValue') });
  if (value.downloadUrl)
    list.push({
      key: 'download',
      label: t('package.details.download'),
      value: displayUrl(value.downloadUrl),
      href: value.downloadUrl
    });
  // 08 §10.8: truncate displayed SHA-256; copy full value with the button below.
  if (downloadSha.value)
    list.push({ key: 'sha256', label: 'SHA-256', value: `${downloadSha.value.slice(0, 16)}…`, mono: true });
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
    list.push({ key: 'repo', label: t('package.details.repo'), value: displayUrl(value.repoUrl), href: value.repoUrl });
  return list;
});

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('toast.copied'), description: command });
}

async function copySha() {
  const sha = downloadSha.value;
  if (!sha) return;
  try {
    await navigator.clipboard.writeText(sha);
    toasts.push({ tone: 'success', title: t('download.shaCopied') });
  } catch {
    toasts.push({ tone: 'warning', title: t('toast.copyFailed'), description: sha, duration: 0 });
  }
}

usePageSeo({
  title: () => t('package.details.metaTitle', { name: pkg.value?.displayName ?? token.value }),
  description: () => pkg.value?.summary ?? undefined
});
</script>

<template>
  <div v-if="pkg" class="grid grid-cols-1 gap-20px pt-4px lg:grid-cols-[minmax(0,1fr)_320px]">
    <div class="flex min-w-0 flex-col gap-16px">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px">
        <OnKeyValueList :items="items" />
        <div v-if="downloadSha" class="flex justify-end pb-10px">
          <OnButton size="xs" icon="copy" @click="copySha">{{ t('download.copySha') }}</OnButton>
        </div>
      </section>
      <OnCaskArtifacts v-if="pkg.artifacts.entries?.length" :entries="pkg.artifacts.entries" />
      <section
        v-if="pkg.caveats"
        class="rounded-big border border-solid border-status-warning bg-surface-card px-18px py-14px"
      >
        <h2 class="m-0 text-13.5px font-600 text-status-warning">{{ t('package.caveats') }}</h2>
        <pre class="m-0 mt-8px whitespace-pre-wrap font-mono text-12px leading-[1.6] text-ink-secondary"><OnCodeText
          :text="pkg.caveats"
        /></pre>
      </section>
    </div>
    <aside class="min-w-0">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px">
        <OnInstallCommand :command="pkg.installCommand" @copied="onCopied" />
      </section>
    </aside>
  </div>
</template>
