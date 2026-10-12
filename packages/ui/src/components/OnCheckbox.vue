<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import OnIcon from './OnIcon.vue';

export interface OnCheckboxProps {
  modelValue: boolean;
  /** Some children selected (table header, uninstall group); clicking selects all. */
  indeterminate?: boolean;
  disabled?: boolean;
  /** Always included and unchangeable, e.g. the app bundle in an uninstall plan: checked and greyed. */
  locked?: boolean;
  /** Accessible name when no visible label is passed in the default slot. */
  ariaLabel?: string;
  /** Hover hint, e.g. why the box is disabled. */
  title?: string;
}

const props = defineProps<OnCheckboxProps>();

const emit = defineEmits<{ 'update:modelValue': [value: boolean] }>();

defineSlots<{ default?: () => unknown }>();

const input = ref<HTMLInputElement>();

// indeterminate exists only as a DOM property, never as an attribute.
function syncIndeterminate() {
  if (input.value) input.value.indeterminate = Boolean(props.indeterminate) && !props.modelValue && !props.locked;
}
onMounted(syncIndeterminate);
watch(() => [props.indeterminate, props.modelValue, props.locked], syncIndeterminate);

const checked = computed(() => props.modelValue || props.locked);
const mixed = computed(() => Boolean(props.indeterminate) && !checked.value);

const box = computed(() => {
  if (props.locked) return 'border-ink-disabled bg-ink-disabled';
  if (checked.value || mixed.value) return 'border-brand-coral bg-brand-coral';
  return 'border-line-strong bg-transparent hover:border-ink-tertiary';
});

function onChange(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).checked);
}
</script>

<template>
  <!-- Stop the click here so table rows and group headers do not also react. -->
  <label
    class="inline-flex min-w-0 items-center gap-8px align-middle"
    :class="[disabled || locked ? 'cursor-not-allowed' : 'cursor-pointer', disabled && !locked ? 'opacity-40' : '']"
    :title="title"
    @click.stop
  >
    <span class="relative inline-grid h-16px w-16px shrink-0 place-items-center">
      <input
        ref="input"
        type="checkbox"
        class="m-0 box-border h-16px w-16px cursor-[inherit] appearance-none rounded-[4.5px] border-[1.5px] border-solid outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring"
        :class="box"
        :checked="checked"
        :disabled="disabled || locked"
        :aria-label="ariaLabel"
        :aria-checked="mixed ? 'mixed' : undefined"
        @change="onChange"
      />
      <OnIcon
        v-if="checked || mixed"
        :name="mixed ? 'minus' : 'check'"
        :size="11"
        class="pointer-events-none absolute"
        :class="locked ? 'text-ink-inverse' : 'text-ink-on-accent'"
      />
    </span>
    <slot />
  </label>
</template>
