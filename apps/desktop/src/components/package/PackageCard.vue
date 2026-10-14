<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import type { PackageSummary } from '@opennavo/api';
import { OnAppCard } from '@opennavo/ui';
import type { OnAppCardStat } from '@opennavo/ui';
import { usePackageState } from '@/composables/usePackageState';
import { useTasksStore } from '@/stores';

// App card: shared OnAppCard plus local state (get / update / open / progress).
const props = defineProps<{ pkg: PackageSummary; href?: string; stats?: readonly OnAppCardStat[] }>();

const { stateOf, act } = usePackageState();
const tasks = useTasksStore();
const current = computed(() => stateOf(props.pkg.kind, props.pkg.token, props.pkg.disabled));

function cancel() {
  const task = tasks.forPackage(props.pkg.kind, props.pkg.token);
  if (task) void tasks.cancel(task.id);
}
</script>

<template>
  <OnAppCard
    :pkg="pkg"
    :state="current.state"
    :progress="current.progress"
    :action-title="current.label"
    :stats="stats"
    :href="href ?? `/package/${pkg.kind}/${pkg.token}`"
    :link-as="RouterLink"
    @action="act(pkg.kind, pkg.token, current.state)"
    @cancel="cancel"
  />
</template>
