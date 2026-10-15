<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnButton, OnModal } from '@opennavo/ui';
import type { Task, TaskTarget } from '@/ipc/bindings';
import RelaunchNotice from '@/components/permissions/RelaunchNotice.vue';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { usePermissionPromptStore, usePermissionsStore, useTasksStore } from '@/stores';
import { paneDone } from '@/stores/permissions';
import { recheckRoute, retryOptions } from '@/stores/permissionPrompt';

// Permission guidance (06 §12.3, §12.6) for E_PERMISSION (App Management) or E_FULL_DISK_ACCESS (zap data removal).
// List blocked apps; Enable opens System Settings/guidance; Retry re-enqueues with trigger=retry after authorization.
// Full Disk Access failures occur after moving the app; retry with --force to finish (06 §6.3).
const { t } = useI18n();
const store = usePermissionPromptStore();
const router = useRouter();
const retryable = computed(() => store.items.filter(task => !recheckRoute(task)));
async function recheck(task: Task) {
  const route = recheckRoute(task);
  if (route) {
    await router.push(route);
    store.remove(task.id);
  }
}
const permissions = usePermissionsStore();
const tasks = useTasksStore();
const toasts = useToasts();
const { displayName, icon } = usePackageIdentity();
const { errorText } = usePackageState();
const retrying = ref(false);

const open = computed({
  get: () => store.pane !== null,
  set: value => {
    if (!value) store.dismiss();
  }
});
const key = computed(() => (store.pane === 'full_disk_access' ? 'fullDiskAccess' : 'appManagement'));
const items = computed(() =>
  store.items.flatMap(task => (task.target ? [{ task, target: task.target as TaskTarget }] : []))
);
// On in System Settings (in effect or awaiting a reopen): Enable has nothing left to do; the notice reopens OpenNavo.
const done = computed(() => store.pane !== null && paneDone(store.pane, permissions.status));

// Read current state on opening: permission may already be enabled and only require relaunch.
watch(
  () => store.pane,
  pane => {
    if (pane) void permissions.refresh().catch(() => undefined);
  },
  { immediate: true }
);

async function enable() {
  if (!store.pane) return;
  try {
    await permissions.guide(store.pane);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function retry() {
  const list: Task[] = [...retryable.value];
  retrying.value = true;
  try {
    for (const task of list) {
      const queued = await tasks.enqueue(task.op, task.target, 'retry', retryOptions(task));
      if (!queued) continue;
      tasks.applyUpdate(queued);
      // Remove only failures successfully re-enqueued; retain new permission failures received while waiting.
      store.remove(task.id);
    }
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    retrying.value = false;
  }
}
</script>

<template>
  <OnModal
    v-model:open="open"
    size="sm"
    :title="t(`permission.prompt.${key}.title`)"
    :description="t(`permission.prompt.${key}.body`)"
  >
    <ul class="m-0 flex list-none flex-col gap-8px p-0">
      <li v-for="{ task, target } in items" :key="task.id" class="flex items-center gap-10px">
        <OnAppIcon v-bind="icon(target.kind, target.token)" :size="28" />
        <span class="min-w-0 flex-1 truncate text-13px text-ink-primary">{{
          displayName(target.kind, target.token)
        }}</span>
        <OnButton v-if="recheckRoute(task)" variant="secondary" size="sm" @click="recheck(task)">{{
          t('updates.history.recheck')
        }}</OnButton>
      </li>
    </ul>
    <RelaunchNotice v-if="permissions.relaunchRequired" class="mt-12px" />
    <template #footer>
      <OnButton variant="ghost" :disabled="retrying" @click="open = false">{{ t('permission.prompt.later') }}</OnButton>
      <OnButton v-if="!done" variant="secondary" :loading="permissions.guiding === store.pane" @click="enable">{{
        permissions.guiding === store.pane ? t('permission.prompt.waiting') : t('permission.prompt.enable')
      }}</OnButton>
      <OnButton v-if="retryable.length" variant="primary" :loading="retrying" @click="retry">{{
        retryable.length > 1 ? t('permission.prompt.retryAll') : t('permission.prompt.retry')
      }}</OnButton>
    </template>
  </OnModal>
</template>
