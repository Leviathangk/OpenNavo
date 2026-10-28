<script setup lang="ts">
// Shared dialog/sheet/palette shell: body teleport, backdrop, transitions, Escape, focus transfer/trap/restore, scroll lock.
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { focusableIn, lockScroll, moveFocus, trapTab, unlockScroll } from '../composables/overlay';

export type OnDialogPlacement = 'center' | 'right' | 'top';

export interface OnDialogShellProps {
  open: boolean;
  /** center dialog; right sheet; top at 15vh for command palette. */
  placement?: OnDialogPlacement;
  /** False prevents Escape/backdrop dismissal for destructive confirmations. */
  dismissible?: boolean;
  role?: 'dialog' | 'alertdialog';
  labelledby?: string;
  describedby?: string;
  ariaLabel?: string;
  /** Initial focus selector inside panel; defaults to first focusable element. */
  initialFocus?: string;
  /** Backdrop: dim translucent black, clear transparent click-catcher for palette. */
  backdrop?: 'dim' | 'clear';
  panelClass?: string;
}

const props = withDefaults(defineProps<OnDialogShellProps>(), {
  placement: 'center',
  dismissible: true,
  role: 'dialog',
  backdrop: 'dim'
});

const emit = defineEmits<{
  /** Escape or backdrop click. */
  close: [];
}>();

defineSlots<{ default?: () => unknown }>();

// Unmount Teleport only after exit transition; closed SSR renders nothing.
const rendered = ref(props.open);
const panel = ref<HTMLElement>();
let opener: HTMLElement | null = null;
let active = false;
let pressedOnBackdrop = false;

async function activate() {
  if (active) return;
  active = true;
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  lockScroll();
  await nextTick();
  const container = panel.value;
  if (!container) return;
  const target = props.initialFocus ? container.querySelector<HTMLElement>(props.initialFocus) : null;
  moveFocus(target ?? focusableIn(container)[0] ?? container);
}

function deactivate() {
  if (!active) return;
  active = false;
  unlockScroll();
  // Restore focus to the opener, which may have been removed.
  if (opener?.isConnected) moveFocus(opener);
  opener = null;
}

watch(
  () => props.open,
  open => {
    if (open) {
      rendered.value = true;
      void activate();
    } else {
      deactivate();
    }
  }
);

onMounted(() => {
  if (props.open) void activate();
});

onBeforeUnmount(deactivate);

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    if (!props.dismissible) return;
    event.preventDefault();
    event.stopPropagation();
    emit('close');
  } else if (event.key === 'Tab' && panel.value) {
    trapTab(event, panel.value);
  }
}

// Dismiss only when press/release both occur on backdrop, avoiding accidental closure after text selection.
function onMousedown(event: MouseEvent) {
  pressedOnBackdrop = event.target === event.currentTarget;
}

function onClick(event: MouseEvent) {
  const onBackdrop = pressedOnBackdrop && event.target === event.currentTarget;
  pressedOnBackdrop = false;
  if (onBackdrop && props.dismissible) emit('close');
}

const LAYOUT: Record<OnDialogPlacement, string> = {
  center: 'items-center justify-center p-24px',
  right: 'items-stretch justify-end',
  top: 'items-start justify-center px-16px pt-[15vh]'
};
</script>

<template>
  <Teleport v-if="rendered" to="body">
    <!-- Reopening while the exit transition runs makes Vue finish that leave through after-leave: keep the Teleport. -->
    <Transition :name="`on-dialog-${placement}`" appear @after-leave="rendered = open">
      <div
        v-if="open"
        class="on-dialog fixed inset-0 z-modal box-border flex font-sans"
        :class="[LAYOUT[placement], backdrop === 'dim' ? 'bg-component-scrim' : 'bg-transparent']"
        @keydown="onKeydown"
        @mousedown="onMousedown"
        @click="onClick"
      >
        <div
          ref="panel"
          class="on-dialog-panel outline-none"
          :class="panelClass"
          :role="role"
          aria-modal="true"
          :aria-labelledby="labelledby"
          :aria-describedby="describedby"
          :aria-label="ariaLabel"
          tabindex="-1"
        >
          <slot />
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style>
/* Enter 200 ms emphasized, exit 120 ms exit (08 §5); panel/backdrop durations match, with Vue tracking backdrop completion. */
.on-dialog-center-enter-active,
.on-dialog-right-enter-active,
.on-dialog-top-enter-active,
.on-dialog-center-enter-active .on-dialog-panel,
.on-dialog-right-enter-active .on-dialog-panel,
.on-dialog-top-enter-active .on-dialog-panel {
  transition:
    opacity var(--on-motion-duration-base) var(--on-motion-easing-emphasized),
    transform var(--on-motion-duration-base) var(--on-motion-easing-emphasized);
}

.on-dialog-center-leave-active,
.on-dialog-right-leave-active,
.on-dialog-top-leave-active,
.on-dialog-center-leave-active .on-dialog-panel,
.on-dialog-right-leave-active .on-dialog-panel,
.on-dialog-top-leave-active .on-dialog-panel {
  transition:
    opacity var(--on-motion-duration-fast) var(--on-motion-easing-exit),
    transform var(--on-motion-duration-fast) var(--on-motion-easing-exit);
}

.on-dialog-center-enter-from,
.on-dialog-center-leave-to,
.on-dialog-right-enter-from,
.on-dialog-right-leave-to,
.on-dialog-top-enter-from,
.on-dialog-top-leave-to {
  opacity: 0;
}

.on-dialog-center-enter-from .on-dialog-panel,
.on-dialog-center-leave-to .on-dialog-panel {
  transform: scale(0.96);
}

.on-dialog-right-enter-from .on-dialog-panel,
.on-dialog-right-leave-to .on-dialog-panel {
  transform: translateX(100%);
}

.on-dialog-top-enter-from .on-dialog-panel,
.on-dialog-top-leave-to .on-dialog-panel {
  transform: translateY(-8px);
}
</style>
