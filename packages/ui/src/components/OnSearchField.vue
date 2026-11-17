<script setup lang="ts">
import { ref } from 'vue';
import OnIcon from './OnIcon.vue';
import { useUiMessages } from '../composables/locale';
import { runOnEnter } from '../utils/ime';

export interface OnSearchFieldProps {
  modelValue: string;
  placeholder?: string;
  /** Input accessible name, defaulting to placeholder. */
  ariaLabel?: string;
  /** Right shortcut hint, e.g. Command-K; replaced by Clear when input has text. */
  shortcut?: string;
  clearable?: boolean;
  disabled?: boolean;
}

const props = withDefaults(defineProps<OnSearchFieldProps>(), { clearable: true });

const emit = defineEmits<{
  'update:modelValue': [value: string];
  /** Enter key. */
  submit: [value: string];
  clear: [];
}>();

const messages = useUiMessages();
const input = ref<HTMLInputElement>();

function onInput(event: Event) {
  emit('update:modelValue', (event.target as HTMLInputElement).value);
}

function clear() {
  emit('update:modelValue', '');
  emit('clear');
  input.value?.focus();
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter') {
    runOnEnter(event, () => emit('submit', props.modelValue));
  } else if (event.key === 'Escape' && props.modelValue) {
    // Escape clears nonempty input first; otherwise bubble outward, e.g. to close palette.
    event.stopPropagation();
    clear();
  }
}

defineExpose({
  focus: () => input.value?.focus(),
  blur: () => input.value?.blur()
});
</script>

<template>
  <div
    class="box-border flex h-32px min-w-0 items-center rounded-default border border-solid border-line-subtle bg-component-search-bg transition-colors duration-fast ease-standard focus-within:border-line-strong focus-within:shadow-focus-ring"
    :class="disabled ? 'opacity-50' : ''"
  >
    <OnIcon name="search" :size="16" class="ml-11px text-ink-tertiary" />
    <input
      ref="input"
      type="search"
      class="on-search-input m-0 box-border h-full min-w-0 flex-1 border-none bg-transparent px-9px py-0 font-sans text-12.5px text-ink-primary outline-none placeholder:text-ink-tertiary"
      :value="modelValue"
      :placeholder="placeholder"
      :aria-label="ariaLabel ?? placeholder"
      :disabled="disabled"
      autocomplete="off"
      spellcheck="false"
      @input="onInput"
      @keydown="onKeydown"
    />
    <button
      v-if="clearable && modelValue && !disabled"
      type="button"
      class="m-0 mr-6px box-border inline-flex h-20px w-20px items-center justify-center rounded-tiny border-none bg-transparent p-0 text-ink-tertiary outline-none transition-colors duration-fast hover:text-ink-primary focus-visible:shadow-focus-ring"
      :aria-label="messages.action.clear"
      @click="clear"
    >
      <OnIcon name="x" :size="14" />
    </button>
    <kbd
      v-else-if="shortcut"
      class="mr-8px inline-flex h-18px items-center rounded-tiny bg-surface-raised px-5px font-sans text-11px text-ink-tertiary"
    >
      {{ shortcut }}
    </kbd>
  </div>
</template>

<style>
/* Hide WebKit's native clear button in favor of our own. */
.on-search-input::-webkit-search-cancel-button,
.on-search-input::-webkit-search-decoration {
  appearance: none;
}
</style>
