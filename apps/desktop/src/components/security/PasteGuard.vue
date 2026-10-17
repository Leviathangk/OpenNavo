<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnModal } from '@opennavo/ui';
import type { PasteAction } from '@/ipc/bindings';
import { IpcError } from '@/ipc/client';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';

// A paused paste (store/security plan §6, S2 paste protection): Rust brought this window forward after holding back
// a paste that looked like download-and-run in a terminal. Discard is the default; Allow Once makes Rust re-check
// the same terminal and clipboard before pasting. Unanswered, it expires after 60 seconds and is discarded.
const { t } = useI18n();
const security = useSecurityStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const paste = computed(() => security.paste);
// Text over the check limit is paused unread: say so rather than describe a command it may not contain.
const unchecked = computed(() => paste.value?.patterns.includes('too_long') ?? false);
const now = ref(Date.now());
const timer = window.setInterval(() => {
  now.value = Date.now();
}, 1000);
onScopeDispose(() => window.clearInterval(timer));
const remaining = computed(() =>
  paste.value ? Math.max(0, Math.ceil((paste.value.expiresAt - now.value) / 1000)) : 0
);
// Rust discards it at expiry too; the dialog just goes away.
watch(remaining, seconds => {
  if (paste.value && seconds === 0) security.paste = null;
});

async function act(action: PasteAction) {
  try {
    await security.actOnPaste(action);
  } catch (error) {
    if (error instanceof IpcError && error.code === 'E_PASTE_EXPIRED') {
      toasts.push({ tone: 'info', title: t('errors.E_PASTE_EXPIRED') });
      return;
    }
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <OnModal
    :open="paste !== null"
    :title="t('security.paste.title', { app: paste?.appName ?? '' })"
    :description="unchecked ? t('security.paste.bodyUnchecked') : t('security.paste.body')"
    size="md"
    role="alertdialog"
    initial-focus="[data-paste-discard]"
    @close="act('discard')"
  >
    <template v-if="paste">
      <p v-if="!unchecked" class="m-0 text-12.5px text-status-warning">
        {{ paste.patterns.map(pattern => t(`security.paste.pattern.${pattern}`)).join(' · ') }}
      </p>
      <pre
        class="m-0 mt-10px max-h-140px overflow-auto whitespace-pre-wrap break-all rounded-small bg-surface-raised p-10px font-mono text-12px text-ink-primary"
        >{{ paste.preview }}</pre
      >
      <p class="mb-0 mt-10px text-12px text-ink-tertiary">{{ t('security.paste.expires', { seconds: remaining }) }}</p>
    </template>
    <template #footer>
      <OnButton variant="ghost" data-paste-allow @click="act('allow_once')">{{ t('security.paste.allow') }}</OnButton>
      <OnButton variant="primary" data-paste-discard @click="act('discard')">{{
        t('security.paste.discard')
      }}</OnButton>
    </template>
  </OnModal>
</template>
