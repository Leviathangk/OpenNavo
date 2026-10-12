<script setup lang="ts">
import { useId } from 'vue';
import OnButton from './OnButton.vue';
import OnDialogShell from './OnDialogShell.vue';
import { useUiMessages } from '../composables/locale';

export interface OnSheetProps {
  open: boolean;
  title: string;
  /** Initial focus selector, defaulting to first focusable element. */
  initialFocus?: string;
}

defineProps<OnSheetProps>();

const emit = defineEmits<{
  'update:open': [open: boolean];
  close: [];
}>();

const slots = defineSlots<{
  default?: () => unknown;
  /** Right of title, before Close, e.g. Copy all. */
  actions?: () => unknown;
  footer?: () => unknown;
}>();

const messages = useUiMessages();
const titleId = useId();

function close() {
  emit('update:open', false);
  emit('close');
}

defineExpose({ close });
</script>

<template>
  <OnDialogShell
    :open="open"
    placement="right"
    :labelledby="titleId"
    :initial-focus="initialFocus"
    panel-class="box-border flex h-full min-w-0 w-420px max-w-full flex-col overflow-hidden border-l border-l-solid border-line-default bg-component-modal-bg shadow-modal"
    @close="close"
  >
    <header class="flex h-56px shrink-0 items-center gap-8px border-b border-b-solid border-line-subtle px-20px">
      <h2 :id="titleId" class="m-0 min-w-0 flex-1 truncate text-headline text-ink-primary">{{ title }}</h2>
      <slot name="actions" />
      <OnButton variant="ghost" size="sm" icon="x" icon-only :aria-label="messages.dialog.close" @click="close" />
    </header>
    <div
      class="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain p-20px text-13px text-ink-secondary"
    >
      <slot />
    </div>
    <footer
      v-if="slots.footer"
      class="flex shrink-0 justify-end gap-8px border-t border-t-solid border-line-subtle px-20px py-14px"
    >
      <slot name="footer" />
    </footer>
  </OnDialogShell>
</template>
