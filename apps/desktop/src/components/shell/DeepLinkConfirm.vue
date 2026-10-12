<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { installCommand } from '@opennavo/shared';
import { OnAppIcon, OnConfirm } from '@opennavo/ui';
import { useDeepLinkStore } from '@/composables/useDeepLink';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useTasksStore } from '@/stores';

// Deep-link install confirmation (06 §10): explain the web origin and show complete brew commands; enqueue only after consent (trigger=deeplink).
const { t } = useI18n();
const deeplink = useDeepLinkStore();
const tasks = useTasksStore();
const toasts = useToasts();
const { displayName, icon } = usePackageIdentity();
const { errorText } = usePackageState();
const queueing = ref(false);

const open = computed({
  get: () => Boolean(deeplink.install),
  set: value => {
    if (!value) deeplink.install = null;
  }
});
const target = computed(() => deeplink.install);
const name = computed(() => (target.value ? displayName(target.value.kind, target.value.token) : ''));
// Deep links only ever request Homebrew packages, never local `app` targets.
const command = computed(() =>
  target.value && target.value.kind !== 'app' ? installCommand(target.value.kind, target.value.token) : ''
);

async function confirm() {
  if (!target.value) return;
  const requested = target.value;
  // Show web-install consent and adoption consent separately to avoid stacked dialogs.
  deeplink.install = null;
  queueing.value = true;
  try {
    await tasks.enqueue('install', requested, 'deeplink');
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  } finally {
    queueing.value = false;
  }
}
</script>

<template>
  <OnConfirm
    v-model:open="open"
    :title="t('deeplink.installTitle', { name })"
    :description="t('deeplink.installDescription')"
    :confirm-label="t('deeplink.installConfirm')"
    :loading="queueing"
    @confirm="confirm"
  >
    <div v-if="target" class="mt-14px flex items-center gap-12px">
      <OnAppIcon v-bind="icon(target.kind, target.token)" :size="40" />
      <code
        class="min-w-0 flex-1 break-all rounded-default bg-surface-inset px-12px py-10px font-mono text-12.5px text-ink-primary"
        >{{ command }}</code
      >
    </div>
  </OnConfirm>
</template>
