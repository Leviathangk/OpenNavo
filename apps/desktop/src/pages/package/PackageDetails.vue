<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { isThirdPartyTap, lifecycleReasonOf, packageInstallState } from '@opennavo/shared';
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
const installState = computed(() => (detail.value ? packageInstallState(detail.value) : 'active'));
const installCommand = computed(() => detail.value?.installCommand ?? '');
const installNotice = computed(() => {
  const value = detail.value;
  const state = installState.value;
  if (!value || state === 'active') return undefined;
  const reason = lifecycleReasonOf(value, state) ?? t('package.noReason');
  return {
    text: t(
      state === 'disabled'
        ? 'package.disabledNotice'
        : state === 'deprecated'
          ? 'package.deprecatedNotice'
          : 'package.staleNotice',
      { reason }
    ),
    disabled: state === 'disabled'
  };
});

const items = computed<OnKeyValueItem[]>(() => {
  const value = detail.value;
  if (!value) {
    if (!local.value) return [];
    const paths = library.find(kind.value, token.value)?.appPaths ?? [];
    return [
      { key: 'names', label: t('package.details.names'), value: local.value.names.join(' · ') },
      ...(paths.length
        ? [{ key: 'apps', label: t('package.details.location'), value: paths.join('  '), mono: true }]
        : [])
    ];
  }
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

async function copySha() {
  const sha = downloadSha.value;
  if (!sha) return;
  try {
    await navigator.clipboard.writeText(sha);
    toasts.push({ tone: 'success', title: t('common.copied'), description: 'SHA-256' });
  } catch {
    toasts.push({ tone: 'warning', title: t('common.copyFailed'), description: 'SHA-256', duration: 0 });
  }
}

function onCopied(command: string) {
  toasts.push({ tone: 'success', title: t('common.copied'), description: command });
}

function onCopyFailed() {
  toasts.push({ tone: 'warning', title: t('common.copyFailed'), description: installCommand.value, duration: 0 });
}
</script>

<template>
  <OfflineNotice v-if="error && !detail" />
  <div v-else-if="detail || local" class="pt-4px">
    <div class="flex min-w-0 flex-col gap-16px">
      <section
        v-if="installCommand || installNotice"
        class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
        data-testid="package-install-command"
      >
        <p
          v-if="installNotice"
          role="note"
          class="m-0 rounded-default border border-solid px-12px py-10px text-12.5px leading-[1.6]"
          :class="[
            installNotice.disabled
              ? 'border-status-danger text-status-danger'
              : 'border-status-warning text-status-warning',
            installCommand && !installNotice.disabled ? 'mb-12px' : ''
          ]"
        >
          {{ installNotice.text }}
        </p>
        <OnInstallCommand
          v-if="installCommand && !installNotice?.disabled"
          :command="installCommand"
          @copied="onCopied"
          @copy-failed="onCopyFailed"
        />
      </section>
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px">
        <OnKeyValueList :items="items" />
        <div v-if="downloadSha" class="flex justify-end pb-10px">
          <OnButton size="xs" icon="copy" @click="copySha">{{ t('common.copy') }} SHA-256</OnButton>
        </div>
      </section>
      <OnCaskArtifacts v-if="detail?.artifacts.entries?.length" :entries="detail.artifacts.entries" />
      <section
        v-if="detail?.caveats"
        class="rounded-big border border-solid border-status-warning bg-surface-card px-18px py-14px"
      >
        <h2 class="m-0 text-13.5px font-600 text-status-warning">{{ t('package.caveats') }}</h2>
        <pre class="m-0 mt-8px whitespace-pre-wrap font-mono text-12px leading-[1.6] text-ink-secondary"><OnCodeText
          :text="detail.caveats"
        /></pre>
      </section>
    </div>
  </div>
</template>
