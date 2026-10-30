<script setup lang="ts">
import { computed } from 'vue';
import type { PackageInstallState } from '@opennavo/shared';
import OnChip from './OnChip.vue';
import OnTooltip from './OnTooltip.vue';

export interface OnPackageSourceBadgeProps {
  label: string;
  description: string;
  state: PackageInstallState;
  verified: boolean;
}
const props = defineProps<OnPackageSourceBadgeProps>();

// Verified labels already carry a visible check, so only older labels without one keep the icon.
const icon = computed(() => {
  if (props.state !== 'active') return 'triangle-alert';
  if (!props.verified) return 'info';
  return props.label.includes('✓') ? undefined : 'check';
});
</script>

<template>
  <OnTooltip v-slot="{ describedBy }" :content="description" toggle-on-click multiline>
    <button
      type="button"
      :aria-label="label"
      :aria-describedby="describedBy"
      :data-state="state"
      data-testid="package-source-badge"
      class="inline-flex min-h-24px min-w-24px cursor-pointer items-center rounded-small border-none bg-transparent p-0 font-sans outline-none focus-visible:shadow-focus-ring"
    >
      <OnChip
        size="md"
        :tone="state === 'disabled' ? 'danger' : state !== 'active' ? 'warning' : verified ? 'success' : 'neutral'"
        outline
        :icon="icon"
        >{{ label }}</OnChip
      >
    </button>
  </OnTooltip>
</template>
