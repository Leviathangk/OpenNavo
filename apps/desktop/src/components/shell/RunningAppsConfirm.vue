<script setup lang="ts">
import { ref, watch, onBeforeUnmount, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnButton, OnModal } from '@opennavo/ui';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { events } from '@/ipc/client';
import { useTasksStore } from '@/stores/tasks';
import { useToasts } from '@/composables/useToasts';
import { usePackageState } from '@/composables/usePackageState';
import { isDeferredUpdate, useRunningAppsStore } from '@/stores/runningApps';

const { t } = useI18n();
const store = useRunningAppsStore();
const { icon } = usePackageIdentity();
const reopen = ref(true);
const tasks = useTasksStore();
const toasts = useToasts();
const { errorText } = usePackageState();
watch(
  () => tasks.finished[0],
  task => {
    if (task && isDeferredUpdate(task) && task.trigger !== 'schedule') {
      toasts.push({
        tone: 'info',
        title: t('runningApps.deferred'),
        description: t(`errors.${task.error?.code}`)
      });
    }
  }
);
let unlisten: (() => void) | undefined;
let disposed = false;
onMounted(async () => {
  const stop = await events.updatesRequested.listen(event => {
    void tasks.enqueueMany('upgrade', event.payload, 'manual', { greedy: true }).catch(error => {
      toasts.push({
        tone: 'danger',
        title: t('errors.actionFailed'),
        description: errorText(error)
      });
    });
  });
  if (disposed) stop();
  else unlisten = stop;
});
onBeforeUnmount(() => {
  disposed = true;
  unlisten?.();
});
watch(
  () => store.pending,
  () => {
    reopen.value = true;
  }
);
onBeforeUnmount(() => store.choose('cancel'));
</script>

<template>
  <OnModal
    :open="store.pending !== null"
    :title="t('runningApps.title')"
    :description="t(store.pending?.batch ? 'runningApps.batchDescription' : 'runningApps.description')"
    size="md"
    role="alertdialog"
    initial-focus="[data-update-later]"
    @close="store.choose('cancel')"
  >
    <ul class="m-0 flex list-none flex-col gap-10px p-0">
      <li v-for="app in store.pending?.apps" :key="app.target.token" class="flex items-center gap-10px">
        <OnAppIcon v-bind="icon(app.target.kind, app.target.token)" :size="28" />
        <span class="min-w-0 break-words text-13px font-600 text-ink-primary">{{ app.name }}</span>
      </li>
    </ul>
    <label class="mt-18px flex items-center gap-8px text-13px text-ink-secondary">
      <input v-model="reopen" type="checkbox" class="m-0 h-14px w-14px accent-brand-coral" />
      {{ t('runningApps.reopen') }}
    </label>
    <p class="mb-0 mt-8px text-12px leading-[1.5] text-ink-tertiary">
      {{ t('runningApps.quitHint') }}
    </p>
    <template #footer>
      <OnButton variant="ghost" data-update-later @click="store.choose('cancel')">{{
        t('runningApps.later')
      }}</OnButton>
      <OnButton v-if="store.pending?.batch" variant="secondary" @click="store.choose('skip')">{{
        t('runningApps.skip')
      }}</OnButton>
      <OnButton variant="primary" @click="store.choose('quit', reopen)">{{
        t(store.pending?.batch ? 'runningApps.quitAll' : 'runningApps.quit')
      }}</OnButton>
    </template>
  </OnModal>
</template>
