<script setup lang="ts">
import { computed } from 'vue';
import OnIcon from './OnIcon.vue';

export type OnChipTone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent' | 'coral' | 'outline';

export interface OnChipProps {
  /** accent salmon for updates, coral for progress, outline neutral for machine translation/patch badges. */
  tone?: OnChipTone;
  /** Outline combinable with status colors, e.g. disabled = danger + outline. */
  outline?: boolean;
  /** Leading 6 px currentColor dot. */
  dot?: boolean;
  icon?: string;
  /** Heights 20 / 24. */
  size?: 'sm' | 'md';
}

const props = withDefaults(defineProps<OnChipProps>(), { tone: 'neutral', size: 'sm' });

defineSlots<{ default?: () => unknown }>();

const FILLED: Record<Exclude<OnChipTone, 'outline'>, string> = {
  neutral: 'bg-surface-chip text-ink-secondary',
  success: 'bg-status-success-subtle text-status-success',
  warning: 'bg-status-warning-subtle text-status-warning',
  danger: 'bg-status-danger-subtle text-status-danger',
  info: 'bg-status-info-subtle text-status-info',
  accent: 'bg-accent-salmon-subtle text-brand-salmon',
  coral: 'bg-accent-coral-subtle text-ink-accent'
};

const OUTLINED: Record<Exclude<OnChipTone, 'outline'>, string> = {
  neutral: 'border-line-default text-ink-secondary',
  success: 'on-chip-tinted-border text-status-success',
  warning: 'on-chip-tinted-border text-status-warning',
  danger: 'on-chip-tinted-border text-status-danger',
  info: 'on-chip-tinted-border text-status-info',
  accent: 'on-chip-tinted-border text-brand-salmon',
  coral: 'on-chip-tinted-border text-ink-accent'
};

const classes = computed(() => {
  const outlined = props.outline || props.tone === 'outline';
  const tone = props.tone === 'outline' ? 'neutral' : props.tone;
  return [
    'box-border inline-flex items-center whitespace-nowrap rounded-small font-500 leading-none',
    props.size === 'md' ? 'h-24px gap-6px px-8px text-12px' : 'h-20px gap-5px px-7px text-11px',
    outlined ? `border border-solid bg-transparent ${OUTLINED[tone]}` : FILLED[tone]
  ];
});
</script>

<template>
  <span :class="classes">
    <span v-if="dot" class="h-6px w-6px shrink-0 rounded-full bg-current" aria-hidden="true"></span>
    <OnIcon v-if="icon" :name="icon" :size="12" />
    <slot />
  </span>
</template>

<style>
.on-chip-tinted-border {
  border-color: color-mix(in srgb, currentColor 40%, transparent);
}
</style>
