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
  /**
   * The label already shows a check mark of its own (every `package.sourceBadge.verified` string carries
   * one), so a verified active source needs no icon. The host sets this because it picks the label.
   */
  labelHasCheck?: boolean;
}

const props = withDefaults(defineProps<OnPackageSourceBadgeProps>(), { labelHasCheck: false });

// The icon follows the typed props. It used to be found by scanning the translated label for "✓", which
// made it depend on copy: a language that moved or dropped the mark lost the icon, and one that mentioned
// the mark elsewhere drew the icon under a second check.
const icon = computed(() => {
  if (props.state !== 'active') return 'triangle-alert';
  if (!props.verified) return 'info';
  return props.labelHasCheck ? undefined : 'check';
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
