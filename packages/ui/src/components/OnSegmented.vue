<script setup lang="ts" generic="T extends string">
import { nextTick, ref } from 'vue';
import { isRovingKey, nextRovingIndex } from '../composables/roving';
import type { OnSegmentedOption } from '../types';

export interface OnSegmentedProps<V extends string> {
  modelValue: V;
  options: readonly OnSegmentedOption<V>[];
  /** Option heights 24 / 28. */
  size?: 'sm' | 'md';
  ariaLabel?: string;
}

const props = withDefaults(defineProps<OnSegmentedProps<T>>(), { size: 'md' });

const emit = defineEmits<{ 'update:modelValue': [value: T] }>();

const buttons = ref<HTMLButtonElement[]>([]);

function select(value: T) {
  if (value !== props.modelValue) emit('update:modelValue', value);
}

async function onKeydown(event: KeyboardEvent) {
  if (!isRovingKey(event.key)) return;
  event.preventDefault();
  const current = props.options.findIndex(option => option.value === props.modelValue);
  const next = nextRovingIndex(
    event.key,
    current,
    props.options.map(() => true)
  );
  const option = props.options[next];
  if (!option) return;
  select(option.value);
  await nextTick();
  buttons.value[next]?.focus();
}
</script>

<template>
  <div
    role="radiogroup"
    :aria-label="ariaLabel"
    class="box-border inline-flex max-w-full flex-wrap items-center gap-2px rounded-default border border-solid border-line-subtle bg-surface-inset p-3px"
    @keydown="onKeydown"
  >
    <button
      v-for="option in options"
      :key="option.value"
      ref="buttons"
      type="button"
      role="radio"
      :aria-checked="option.value === modelValue ? 'true' : 'false'"
      :tabindex="option.value === modelValue ? 0 : -1"
      class="m-0 box-border inline-flex items-center gap-6px whitespace-nowrap rounded-small border-none font-sans font-500 outline-none transition-colors duration-fast ease-standard focus-visible:shadow-focus-ring"
      :class="[
        size === 'sm' ? 'h-24px px-10px text-12px' : 'h-28px px-13px text-12.5px',
        option.value === modelValue
          ? 'bg-component-segment-active text-ink-primary shadow-segment'
          : 'bg-transparent text-ink-secondary hover:text-ink-primary'
      ]"
      @click="select(option.value)"
    >
      <span v-if="option.dots?.length" class="inline-flex items-center gap-2px" aria-hidden="true">
        <span
          v-for="dot in option.dots"
          :key="dot"
          class="h-5px w-5px rounded-full"
          :style="{ backgroundColor: `var(--on-${dot})` }"
        ></span>
      </span>
      {{ option.label }}
      <span v-if="option.count !== undefined" class="text-11.5px text-ink-tertiary tabular-nums">
        {{ option.count }}
      </span>
    </button>
  </div>
</template>
