<script setup lang="ts">
import { useId } from 'vue';
import OnButton from './OnButton.vue';
import OnDialogShell from './OnDialogShell.vue';
import { useUiMessages } from '../composables/locale';

export type OnModalSize = 'sm' | 'md' | 'lg';

export interface OnModalProps {
  open: boolean;
  title: string;
  description?: string;
  /** Widths 480 / 560 / 720. */
  size?: OnModalSize;
  /** False prevents Escape/backdrop dismissal for destructive confirmation. */
  dismissible?: boolean;
  /** Upper-right Close button. */
  closable?: boolean;
  role?: 'dialog' | 'alertdialog';
  /** Initial focus selector, default first focusable element. */
  initialFocus?: string;
}

const props = withDefaults(defineProps<OnModalProps>(), {
  size: 'md',
  dismissible: true,
  closable: true,
  role: 'dialog'
});

const emit = defineEmits<{
  'update:open': [open: boolean];
  /** Any dismissal: Escape, backdrop, Close. */
  close: [];
}>();

const slots = defineSlots<{
  default?: () => unknown;
  /** Right-aligned footer actions. */
  footer?: () => unknown;
  /** Before the title, e.g. the app icon in the uninstall dialog. */
  leading?: () => unknown;
  /** Rich line under the title (chips, code); description still renders below it. */
  subtitle?: () => unknown;
}>();

const messages = useUiMessages();
const titleId = useId();
const descriptionId = useId();

const WIDTH: Record<OnModalSize, string> = { sm: 'max-w-480px', md: 'max-w-560px', lg: 'max-w-720px' };

function close() {
  emit('update:open', false);
  emit('close');
}

defineExpose({ close });
</script>

<template>
  <OnDialogShell
    :open="open"
    :dismissible="dismissible"
    :role="role"
    :labelledby="titleId"
    :describedby="description ? descriptionId : undefined"
    :initial-focus="initialFocus"
    :panel-class="`box-border flex max-h-[calc(100vh-48px)] w-full flex-col overflow-hidden rounded-huge border border-solid border-line-default bg-component-modal-bg shadow-modal ${WIDTH[props.size]}`"
    @close="close"
  >
    <header class="flex shrink-0 items-start gap-12px px-24px pt-22px" :class="slots.default ? '' : 'pb-4px'">
      <slot name="leading" />
      <div class="min-w-0 flex-1">
        <h2 :id="titleId" class="m-0 text-title2 text-ink-primary">{{ title }}</h2>
        <div v-if="slots.subtitle" class="mt-4px text-12.5px leading-[1.55] text-ink-tertiary">
          <slot name="subtitle" />
        </div>
        <p v-if="description" :id="descriptionId" class="m-0 mt-6px text-13px leading-[1.55] text-ink-secondary">
          {{ description }}
        </p>
      </div>
      <OnButton
        v-if="closable"
        class="-mr-6px"
        variant="ghost"
        size="sm"
        icon="x"
        icon-only
        :aria-label="messages.dialog.close"
        @click="close"
      />
    </header>
    <div
      v-if="slots.default"
      class="min-h-0 min-w-0 flex-1 overflow-x-hidden overflow-y-auto overscroll-contain px-24px pt-16px text-13px text-ink-secondary"
    >
      <slot />
    </div>
    <footer v-if="slots.footer" class="flex shrink-0 flex-wrap justify-end gap-8px px-24px pb-20px pt-20px">
      <slot name="footer" />
    </footer>
    <div v-else class="h-20px shrink-0"></div>
  </OnDialogShell>
</template>
