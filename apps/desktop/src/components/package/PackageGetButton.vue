<script setup lang="ts">
import { computed } from 'vue';
import { OnGetButton } from '@opennavo/ui';
import type { OnGetButtonProps } from '@opennavo/ui';
import type { Kind } from '@/ipc/bindings';
import { usePackageState } from '@/composables/usePackageState';
import { useTasksStore } from '@/stores';

// Get button (06 §13): derive from installed packages, updates, and queue; clicking while running can stop it.
const props = defineProps<{
  kind: Kind;
  token: string;
  disabled?: boolean;
  label?: string;
  appearance?: OnGetButtonProps['appearance'];
}>();

const { stateOf, taskOf, act } = usePackageState();
const tasks = useTasksStore();
const current = computed(() => stateOf(props.kind, props.token, props.disabled));

function cancel() {
  const task = taskOf(props.kind, props.token);
  if (task) void tasks.cancel(task.id);
}
</script>

<template>
  <OnGetButton
    :state="current.state"
    :progress="current.progress"
    :label="label ?? current.label"
    :appearance="appearance"
    @click="act(kind, token, current.state)"
    @cancel="cancel"
  />
</template>
