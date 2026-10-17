<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnGetButton } from '@opennavo/ui';
import type { GetState } from '@opennavo/ui';
import { commands, unwrap } from '@/ipc/client';
import type { MasApp } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useLocalAppsStore, useTasksStore, useUpdatesStore } from '@/stores';
import { useMasStore } from '@/stores/mas';

// App Store Get button (store/security plan §5, §11.3). With the bundled helper (engine `ok`), free apps install
// and installed ones update through the task queue, signed in as the App Store's Apple Account; without it, Get opens
// the product page in the App Store app. Paid apps always show their price and open the App Store: buying is out of
// scope. Installed apps without an update open.
const props = withDefaults(defineProps<{ app: MasApp; appearance?: 'compact' | 'button' }>(), {
  appearance: 'compact'
});

const { t } = useI18n();
const mas = useMasStore();
const tasks = useTasksStore();
const updates = useUpdatesStore();
const localApps = useLocalAppsStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const target = computed(() => ({ kind: 'mas' as const, token: String(props.app.adamId) }));
const task = computed(() => tasks.forPackage(target.value.kind, target.value.token));
const installed = computed(() => mas.installedFor(props.app.adamId));
const engineReady = computed(() => mas.status?.engine === 'ok');
const update = computed(() => {
  const row = updates.findMas(props.app.adamId);
  return row && !row.ignored && !row.informationalUrl ? row : undefined;
});

const state = computed<GetState>(() => {
  if (task.value?.state === 'running') return 'running';
  if (task.value?.state === 'queued') return 'queued';
  if (installed.value) return update.value && engineReady.value ? 'update' : 'open';
  return 'get';
});
const label = computed(() =>
  state.value === 'get' ? (props.app.price > 0 ? props.app.formattedPrice : t('appStore.get')) : undefined
);

const acting = ref(false);
async function act() {
  if (acting.value || task.value?.state === 'running' || task.value?.state === 'queued') return;
  const intent = state.value;
  const acceptedApp = props.app;
  const acceptedTarget = target.value;
  const acceptedUpdate = update.value;
  acting.value = true;
  try {
    if (intent === 'update' && acceptedUpdate) {
      await updates.upgrade([acceptedUpdate]);
      return;
    }
    if (intent === 'open') {
      // Refresh a future Update label without changing or blocking this Open.
      if (acceptedUpdate && !mas.status) void mas.ensureStatus().catch(() => undefined);
      if (!localApps.loaded) await localApps.load();
      const local = localApps.items.find(app => app.bundleId === acceptedApp.bundleId);
      // An app the Installed list has not picked up yet still opens from its App Store page.
      if (local) await unwrap(commands.localAppOpen(local.id));
      else await mas.openStore(acceptedApp.adamId);
      return;
    }
    if (acceptedApp.price > 0) {
      await mas.openStore(acceptedApp.adamId);
      return;
    }
    const status = await mas.ensureStatus();
    if (status.engine === 'ok') await tasks.enqueue('install', acceptedTarget);
    else await mas.openStore(acceptedApp.adamId);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    acting.value = false;
  }
}

function cancel() {
  if (task.value) void tasks.cancel(task.value.id);
}
</script>

<template>
  <OnGetButton
    :state="state"
    :disabled="acting"
    :label="label"
    :progress="task?.percent ?? undefined"
    :appearance="appearance"
    :aria-label="state === 'get' ? t('appStore.getIn', { name: app.name }) : undefined"
    @click="act"
    @cancel="cancel"
  />
</template>
