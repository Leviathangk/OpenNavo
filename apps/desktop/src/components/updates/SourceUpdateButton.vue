<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnGetButton } from '@opennavo/ui';
import type { GetState } from '@opennavo/ui';
import type { OutdatedItem } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useTasksStore, useUpdatesStore } from '@/stores';
import { updateRequest } from '@/stores/updates';

// Update button for rows another source delivers (store/security plan §11.4): App Store rows queue an App Store
// task, the rest a local-app update with its source. Clicking while it runs stops it, as on Homebrew rows.
const props = defineProps<{ item: OutdatedItem }>();

const { t } = useI18n();
const tasks = useTasksStore();
const updates = useUpdatesStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const task = computed(() => {
  const { target } = updateRequest(props.item);
  return tasks.forPackage(target.kind, target.token);
});
const state = computed<GetState>(() =>
  task.value?.state === 'running' ? 'running' : task.value?.state === 'queued' ? 'queued' : 'update'
);

async function update() {
  try {
    await updates.upgrade([props.item]);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

function cancel() {
  if (task.value) void tasks.cancel(task.value.id);
}
</script>

<template>
  <OnGetButton :state="state" :progress="task?.percent ?? undefined" @click="update" @cancel="cancel" />
</template>
