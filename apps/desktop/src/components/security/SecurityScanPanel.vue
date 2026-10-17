<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnChip, OnEmpty, OnIcon, OnMenu, OnProgressMeter } from '@opennavo/ui';
import type { OnMenuItem } from '@opennavo/ui';
import { IpcError } from '@/ipc/client';
import type { AvFinding, AvScanKind } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useVirusTotal } from '@/composables/useVirusTotal';
import { useSecurityStore } from '@/stores/security';
import { pickFolder } from '@/utils/files';
import { displayPath } from '@/utils/paths';
import { openExternal } from '@/utils/external';
import { shortPath } from './meta';
import { requireDone } from './outcome';

// Virus scan (plan §6.2, mockup E): the first download needs consent; then quick, full or custom scans with live
// progress. Threats default to Quarantine (restorable); Delete and Trust are in the row menu.
const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useSecurityStore();
const virusTotal = useVirusTotal();

const engine = computed(() => store.overview?.engine ?? null);
const ready = computed(() => Boolean(engine.value?.db));
const consented = computed(() => store.securitySettings.avConsent);
const downloading = ref(false);
const scan = computed(() => store.scan);
const kinds: AvScanKind[] = ['quick', 'full', 'custom'];

async function download() {
  downloading.value = true;
  try {
    if (consented.value) await store.updateDatabase();
    else await store.consentAndDownload();
  } catch (error) {
    const limited = error instanceof IpcError && error.code === 'E_AV_DB_RATE_LIMITED';
    toasts.push({
      tone: 'danger',
      title: limited ? t('security.engine.rateLimited') : t('security.engine.downloadFailed'),
      description: limited ? undefined : errorText(error)
    });
  } finally {
    downloading.value = false;
  }
}

async function start(kind: AvScanKind) {
  try {
    let paths: string[] | null = null;
    if (kind === 'custom') {
      const folder = await pickFolder();
      if (!folder) return;
      paths = [folder];
    }
    await store.startScan(kind, paths);
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('security.scan.failed'),
      description: errorText(error)
    });
  }
}

function choose(finding: AvFinding, key: MenuKey) {
  if (key === 'virusTotal') void virusTotal.lookup(finding.sha256);
  else void act(finding, key);
}

async function act(finding: AvFinding, action: 'quarantine' | 'delete' | 'trust' | 'reveal') {
  try {
    const outcome = await store.actOnFindings([finding.id], action);
    requireDone(outcome, finding.id);
    if (action === 'quarantine')
      toasts.push({
        tone: 'success',
        title: t('security.findings.quarantined', {
          name: finding.path.split('/').pop() ?? finding.path
        })
      });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

type MenuKey = 'reveal' | 'trust' | 'delete' | 'virusTotal';
// Scan roots macOS refused: the home folder's well-known folders by name, others by path.
const FOLDERS: Record<string, string> = {
  '~/Downloads': 'downloads',
  '~/Desktop': 'desktop',
  '~/Documents': 'documents'
};
const deniedText = computed(() => {
  const roots = scan.value?.deniedRoots ?? [];
  if (!roots.length) return '';
  const names = roots.map(root => {
    const shown = displayPath(root);
    const key = FOLDERS[shown];
    return key ? t(`security.scan.folders.${key}`) : shown;
  });
  return t('security.scan.denied', {
    folders: new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(names)
  });
});
function openPrivacy() {
  void openExternal('x-apple.systempreferences:com.apple.preference.security?Privacy_FilesAndFolders');
}

const menu = (finding: AvFinding): OnMenuItem[] => [
  { key: 'reveal', label: t('security.findings.reveal'), icon: 'folder' },
  // A second opinion by hash, offered only once the user set up VirusTotal (D8).
  ...(store.virusTotalReady
    ? [
        {
          key: 'virusTotal',
          label: t('security.virusTotal.lookup'),
          icon: 'scan-search',
          disabled: virusTotal.busy.value !== null
        }
      ]
    : []),
  { key: 'trust', label: t('security.findings.trust'), icon: 'shield-check' },
  {
    key: 'delete',
    label: t('security.findings.delete'),
    icon: 'trash',
    danger: true,
    disabled: finding.status !== 'open'
  }
];

const skippedText = computed(() => {
  const value = scan.value;
  if (!value || value.state === 'running') return null;
  const { dataless, tooLarge, unreadable, excluded } = value.skipped;
  const parts = [
    dataless ? t('security.scan.skipped.dataless', { count: dataless }, { plural: dataless }) : null,
    tooLarge ? t('security.scan.skipped.tooLarge', { count: tooLarge }, { plural: tooLarge }) : null,
    unreadable ? t('security.scan.skipped.unreadable', { count: unreadable }, { plural: unreadable }) : null,
    excluded ? t('security.scan.skipped.excluded', { count: excluded }, { plural: excluded }) : null
  ].filter(Boolean);
  return parts.length ? parts.join(' · ') : null;
});
</script>

<template>
  <div class="flex flex-col gap-16px">
    <OnCard v-if="!ready" padding="lg" as="section" class="flex flex-col gap-10px" aria-labelledby="av-consent">
      <h2 id="av-consent" class="m-0 flex items-center gap-8px text-15px font-600 text-ink-primary">
        <OnIcon name="download" :size="17" />{{ t('security.engine.consentTitle') }}
      </h2>
      <p class="m-0 text-13px leading-[1.6] text-ink-secondary">
        {{ t('security.engine.consentBody') }}
      </p>
      <ul class="m-0 flex flex-col gap-4px pl-18px text-12.5px leading-[1.6] text-ink-tertiary">
        <li>{{ t('security.engine.consentSize') }}</li>
        <li>{{ t('security.engine.consentMemory') }}</li>
        <li>{{ t('security.engine.consentSource') }}</li>
        <li>{{ t('security.engine.consentCoverage') }}</li>
      </ul>
      <div class="flex items-center gap-10px">
        <OnButton variant="primary" icon="download" :loading="downloading || engine?.updating" @click="download">
          {{ engine?.updating ? t('security.engine.downloading') : t('security.engine.download') }}
        </OnButton>
        <span v-if="engine?.lastError" class="text-12px text-status-danger">{{
          t('security.engine.downloadFailed')
        }}</span>
      </div>
    </OnCard>

    <div class="grid grid-cols-1 gap-12px md:grid-cols-3">
      <OnCard
        v-for="kind in kinds"
        :key="kind"
        padding="md"
        as="section"
        class="flex flex-col gap-6px"
        :aria-labelledby="`scan-${kind}`"
      >
        <span class="text-11.5px text-ink-tertiary">{{ t(`security.scan.${kind}.duration`) }}</span>
        <h2 :id="`scan-${kind}`" class="m-0 text-16px font-600 text-ink-primary">
          {{ t(`security.scan.${kind}.title`) }}
        </h2>
        <p class="m-0 flex-1 text-12.5px leading-[1.55] text-ink-secondary">
          {{ t(`security.scan.${kind}.body`) }}
        </p>
        <OnButton
          class="mt-6px self-start"
          :variant="kind === 'quick' ? 'primary' : 'secondary'"
          size="sm"
          :disabled="!ready || store.scanning"
          @click="start(kind)"
        >
          {{ t(`security.scan.${kind}.action`) }}
        </OnButton>
      </OnCard>
    </div>

    <OnCard v-if="scan" padding="md" as="section" aria-labelledby="scan-status" aria-live="polite">
      <div class="flex items-center gap-10px">
        <h2 id="scan-status" class="m-0 flex-1 text-14px font-600 text-ink-primary">
          {{
            scan.state === 'running'
              ? t('security.scan.running', {
                  kind: t(`security.scan.${scan.kind}.title`),
                  count: (store.progress?.filesScanned ?? scan.filesScanned).toLocaleString(appLocale),
                  size: formatBytes(store.progress?.bytesScanned ?? scan.bytesScanned, {
                    locale: appLocale
                  })
                })
              : t(`security.scan.state.${scan.state}`, {
                  count: scan.filesScanned.toLocaleString(appLocale),
                  time: scan.finishedAt ? formatRelativeTime(scan.finishedAt, { locale: appLocale }) : ''
                })
          }}
        </h2>
        <OnButton v-if="scan.state === 'running'" variant="ghost" size="sm" @click="store.cancelScan()">
          {{ t('common.cancel') }}
        </OnButton>
      </div>
      <!-- Why a scan stopped, e.g. the engine did not start. -->
      <p
        v-if="scan.state === 'failed' && scan.errorCode"
        class="m-0 mt-6px text-12.5px text-status-danger"
        role="alert"
      >
        {{ te(`errors.${scan.errorCode}`) ? t(`errors.${scan.errorCode}`) : scan.errorCode }}
      </p>
      <!-- Folders macOS would not let OpenNavo read (Files and Folders privacy): name them and offer the setting. -->
      <p v-if="deniedText" class="m-0 mt-6px flex items-center gap-8px text-12.5px text-status-warning" role="status">
        <span class="min-w-0 flex-1">{{ deniedText }}</span>
        <OnButton size="xs" variant="secondary" @click="openPrivacy">{{
          t('security.overview.openSettings')
        }}</OnButton>
      </p>
      <!-- Rust reports counts, not a total: the walk is open-ended, so the meter stays indeterminate. -->
      <OnProgressMeter
        v-if="scan.state === 'running'"
        class="mt-10px"
        :value="null"
        size="sm"
        :label="t('security.scan.progressLabel')"
      />
      <p
        v-if="scan.state === 'running' && store.progress"
        class="m-0 mt-6px truncate font-mono text-11.5px text-ink-tertiary"
      >
        {{ shortPath(store.progress.currentPath, 90) }}
      </p>
      <p v-if="skippedText" class="m-0 mt-6px text-12px text-ink-tertiary">{{ skippedText }}</p>
    </OnCard>

    <section v-if="scan" aria-labelledby="findings-title">
      <h2 id="findings-title" class="m-0 mb-8px text-14px font-600 text-ink-primary">
        {{ t('security.findings.title', { count: scan.findings.length }, { plural: scan.findings.length }) }}
      </h2>
      <ul v-if="scan.findings.length" class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
        <li
          v-for="finding in scan.findings"
          :key="finding.id"
          class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-14px py-10px first:border-t-0"
        >
          <span
            class="grid h-32px w-32px shrink-0 place-items-center rounded-default bg-status-danger-subtle text-status-danger"
            aria-hidden="true"
          >
            <OnIcon name="shield" :size="16" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="m-0 flex items-center gap-6px text-13px font-600 text-ink-primary">
              <span class="truncate">{{ finding.path.split('/').pop() }}</span>
              <OnChip tone="danger" size="sm">{{ finding.signature }}</OnChip>
            </p>
            <p class="m-0 truncate font-mono text-11.5px text-ink-tertiary">
              {{ shortPath(finding.path, 80) }}
            </p>
          </div>
          <OnChip
            v-if="finding.status !== 'open'"
            :tone="finding.status === 'trusted' ? 'neutral' : 'success'"
            size="sm"
          >
            {{ t(`security.findings.status.${finding.status}`) }}
          </OnChip>
          <template v-else>
            <OnButton
              variant="accent"
              size="sm"
              :disabled="store.busy.has(finding.id)"
              @click="act(finding, 'quarantine')"
            >
              {{ t('security.findings.quarantine') }}
            </OnButton>
          </template>
          <OnMenu
            :items="menu(finding)"
            :label="t('security.findings.more', { name: finding.path.split('/').pop() ?? '' })"
            variant="ghost"
            size="sm"
            align="end"
            @select="(key: string) => choose(finding, key as MenuKey)"
          />
        </li>
      </ul>
      <OnEmpty
        v-else-if="scan.state !== 'running'"
        :title="t('security.findings.none')"
        :description="t('security.findings.noneHint')"
        icon="lucide:shield-check"
      />
    </section>
  </div>
</template>
