<script setup lang="ts">
export interface OnToggleProps {
  modelValue: boolean;
  disabled?: boolean;
  /** Required without a visible label. */
  ariaLabel?: string;
  /** ID of the settings-row label element. */
  ariaLabelledby?: string;
}

const props = defineProps<OnToggleProps>();

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>();

function toggle() {
  if (!props.disabled) emit('update:modelValue', !props.modelValue);
}
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="modelValue ? 'true' : 'false'"
    :aria-label="ariaLabel"
    :aria-labelledby="ariaLabelledby"
    :disabled="disabled"
    class="relative m-0 box-border inline-flex h-18px w-30px shrink-0 items-center rounded-full border-none p-0 outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring disabled:cursor-not-allowed disabled:opacity-50"
    :class="modelValue ? 'bg-brand-coral' : 'bg-component-toggle-track'"
    @click="toggle"
  >
    <span
      class="absolute left-2px top-2px h-14px w-14px rounded-full shadow-knob transition-transform duration-fast ease-standard"
      :class="modelValue ? 'translate-x-12px bg-component-toggle-knob-on' : 'bg-component-toggle-knob'"
      aria-hidden="true"
    ></span>
  </button>
</template>
