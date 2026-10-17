<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnIcon } from '@opennavo/ui';
import type { DownloadAction } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useVirusTotal } from '@/composables/useVirusTotal';
import { useDownloadsStore } from '@/stores/downloads';
import { useSecurityStore } from '@/stores/security';
import { describeVerdict, fileName } from './downloadVerdict';
import type { VerdictTone } from './downloadVerdict';

// Download check on a link app's page (store/security plan §4, mockup A): after Download, OpenNavo watches the
// Downloads folder and tells whether the file is signed by the registered developer, notarized and clean.
const props = defineProps<{ token: string }>();

const { t } = useI18n();
const downloads = useDownloadsStore();
const security = useSecurityStore();
const virusTotal = useVirusTotal();
const toasts = useToasts();
const { errorText } = usePackageState();

const entry = computed(() => downloads.forToken(props.token));
const summary = computed(() => (entry.value?.verdict ? describeVerdict(entry.value.verdict, t) : null));
const text = computed(() => {
  const value = entry.value;
  if (!value) return '';
  const file = fileName(value.path);
  return summary.value
    ? t('package.link.verdict.checked', { file, details: summary.value.parts.join(' · ') })
    : t('package.link.verdict.verifying', { file });
});

const LOOK: Record<VerdictTone | 'pending', { icon: string; tone: string }> = {
  pending: { icon: 'shield-check', tone: 'bg-surface-raised text-ink-secondary' },
  success: { icon: 'shield-check', tone: 'bg-status-success-subtle text-status-success' },
  warning: { icon: 'triangle-alert', tone: 'bg-status-warning-subtle text-status-warning' },
  danger: { icon: 'circle-alert', tone: 'bg-status-danger-subtle text-status-danger' }
};
const look = computed(() => LOOK[summary.value?.tone ?? 'pending']);

// Rust opens only files it reported and checked, and never one with a threat; Show in Finder always works.
async function act(action: DownloadAction) {
  const value = entry.value;
  if (!value) return;
  try {
    await unwrap(commands.downloadAct(value.path, action));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div
    v-if="entry"
    role="status"
    aria-live="polite"
    class="flex items-start gap-10px rounded-default px-12px py-10px"
    :class="look.tone"
  >
    <OnIcon :name="look.icon" :size="16" class="mt-2px shrink-0" />
    <p class="m-0 min-w-0 flex-1 text-13px leading-[1.55]">{{ text }}</p>
    <div v-if="summary" class="flex shrink-0 items-center gap-6px">
      <OnButton v-if="summary.tone !== 'danger'" size="xs" @click="act('open')">
        {{ t('package.link.verdict.open') }}
      </OnButton>
      <OnButton size="xs" variant="ghost" @click="act('reveal')">{{ t('package.link.verdict.reveal') }}</OnButton>
      <!-- Only the file's SHA-256 is sent, and only once the user set up VirusTotal (D8). -->
      <OnButton
        v-if="security.virusTotalReady && entry.verdict"
        size="xs"
        variant="ghost"
        :loading="virusTotal.busy.value === entry.verdict.sha256"
        @click="virusTotal.lookup(entry.verdict.sha256)"
      >
        {{ t('security.virusTotal.lookup') }}
      </OnButton>
    </div>
  </div>
</template>
