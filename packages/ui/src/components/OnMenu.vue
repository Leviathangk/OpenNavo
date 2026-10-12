<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useId } from 'vue';
import OnButton from './OnButton.vue';
import type { OnButtonSize, OnButtonVariant } from './OnButton.vue';
import OnIcon from './OnIcon.vue';
import { useUiMessages } from '../composables/locale';
import { moveFocus, usingKeyboard } from '../composables/overlay';

export interface OnMenuItem {
  key: string;
  label: string;
  /** lucide:*，16px */
  icon?: string;
  /** Shortcut hint, e.g. Command-C. */
  shortcut?: string;
  /** Dangerous action such as uninstall uses red text. */
  danger?: boolean;
  disabled?: boolean;
  /** Divider before this item. */
  separator?: boolean;
}

export interface OnMenuProps {
  items: readonly OnMenuItem[];
  /** Accessible trigger/menu name, default More actions. */
  label?: string;
  /** Trigger icon, default ellipsis. */
  icon?: string;
  variant?: OnButtonVariant;
  size?: OnButtonSize;
  /** Which trigger side the menu aligns with. */
  align?: 'start' | 'end';
}

const props = withDefaults(defineProps<OnMenuProps>(), {
  icon: 'ellipsis',
  variant: 'secondary',
  size: 'md',
  align: 'end'
});

const emit = defineEmits<{ select: [key: string] }>();

defineSlots<{
  /** Custom trigger: bind attrs onto the button, e.g. the web Get pill. */
  trigger?: (props: { open: boolean; attrs: Record<string, unknown> }) => unknown;
}>();

const messages = useUiMessages();
const menuId = useId();

const open = ref(false);
const root = ref<HTMLElement>();
const menu = ref<HTMLElement>();
const position = ref<Record<string, string>>({});

const label = computed(() => props.label ?? messages.value.menu.more);

const trigger = () => root.value?.querySelector<HTMLElement>('[aria-haspopup="menu"]') ?? null;
const enabledItems = () =>
  Array.from(menu.value?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? []);

// Teleport menu to body and position by trigger to avoid card overflow clipping; open upward if space below is insufficient.
function place() {
  const anchor = trigger()?.getBoundingClientRect();
  if (!anchor) return;
  const height = menu.value?.offsetHeight ?? 0;
  const style: Record<string, string> = {};
  if (anchor.bottom + 6 + height <= window.innerHeight - 8) style.top = `${anchor.bottom + 6}px`;
  else style.bottom = `${window.innerHeight - anchor.top + 6}px`;
  if (props.align === 'end') style.right = `${window.innerWidth - anchor.right}px`;
  else style.left = `${anchor.left}px`;
  position.value = style;
}

// Hover focuses the item to match keyboard selection.
function focusItem(event: MouseEvent) {
  (event.currentTarget as HTMLElement).focus();
}

function onOutside(event: Event) {
  const target = event.target;
  if (!(target instanceof Node)) return;
  if (root.value?.contains(target) || menu.value?.contains(target)) return;
  close(false);
}

function onViewportChange() {
  close(false);
}

function listen(active: boolean) {
  const method = active ? 'addEventListener' : 'removeEventListener';
  document[method]('pointerdown', onOutside, true);
  window[method]('resize', onViewportChange);
  window[method]('scroll', onViewportChange, true);
}

async function show(focus: 'first' | 'last') {
  // Hide until positioned to avoid flashing at top-left.
  position.value = { visibility: 'hidden' };
  open.value = true;
  listen(true);
  await nextTick();
  place();
  // Focus only after positioning/visibility takes effect; browsers cannot focus visibility:hidden elements.
  await nextTick();
  const items = enabledItems();
  // Like native menus, keyboard opens select the first/last item; pointer opens leave selection to hover and arrows.
  moveFocus(usingKeyboard() ? (focus === 'first' ? items[0] : items.at(-1)) : menu.value);
}

function close(restoreFocus = true) {
  if (!open.value) return;
  open.value = false;
  listen(false);
  if (restoreFocus) moveFocus(trigger());
}

function toggle() {
  if (open.value) close();
  else void show('first');
}

function onTriggerKeydown(event: KeyboardEvent) {
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault();
    void show(event.key === 'ArrowDown' ? 'first' : 'last');
  }
}

const triggerAttrs = computed(() => ({
  'aria-haspopup': 'menu',
  'aria-expanded': open.value ? 'true' : 'false',
  'aria-controls': open.value ? menuId : undefined,
  onClick: toggle,
  onKeydown: onTriggerKeydown
}));

function onMenuKeydown(event: KeyboardEvent) {
  const items = enabledItems();
  const index = items.indexOf(document.activeElement as HTMLElement);
  const focusAt = (next: number) => items[(next + items.length) % items.length]?.focus();
  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault();
      focusAt(index + 1);
      break;
    case 'ArrowUp':
      event.preventDefault();
      focusAt(index < 0 ? -1 : index - 1);
      break;
    case 'Home':
      event.preventDefault();
      focusAt(0);
      break;
    case 'End':
      event.preventDefault();
      focusAt(-1);
      break;
    case 'Escape':
      event.preventDefault();
      event.stopPropagation();
      close();
      break;
    case 'Tab':
      // Menu items are outside Tab order; close, restore trigger focus, then continue normal Tab traversal.
      event.preventDefault();
      close();
      break;
  }
}

function choose(item: OnMenuItem) {
  if (item.disabled) return;
  close();
  emit('select', item.key);
}

onBeforeUnmount(() => {
  if (open.value) listen(false);
});
</script>

<template>
  <span ref="root" class="relative inline-flex">
    <slot name="trigger" :open="open" :attrs="triggerAttrs">
      <OnButton :variant="variant" :size="size" :icon="icon" icon-only :aria-label="label" v-bind="triggerAttrs" />
    </slot>
    <Teleport v-if="open" to="body">
      <div
        :id="menuId"
        ref="menu"
        role="menu"
        :aria-label="label"
        tabindex="-1"
        class="on-menu fixed z-dropdown box-border min-w-200px rounded-panel border border-solid border-line-default p-6px font-sans shadow-popover outline-none"
        :style="position"
        @keydown="onMenuKeydown"
      >
        <template v-for="(item, index) in items" :key="item.key">
          <div v-if="item.separator && index > 0" role="separator" class="mx-4px my-4px h-1px bg-line-subtle"></div>
          <button
            type="button"
            role="menuitem"
            tabindex="-1"
            :disabled="item.disabled"
            class="m-0 box-border flex h-32px w-full items-center gap-8px rounded-small border-none bg-transparent px-10px font-sans text-13px outline-none transition-colors duration-fast ease-standard enabled:hover:bg-component-item-active focus:bg-component-item-active disabled:cursor-not-allowed disabled:text-ink-disabled"
            :class="item.danger ? 'text-status-danger' : 'text-ink-primary'"
            @click="choose(item)"
            @mousemove="focusItem"
          >
            <OnIcon v-if="item.icon" :name="item.icon" :size="16" :class="item.danger ? '' : 'text-ink-secondary'" />
            <span class="min-w-0 flex-1 truncate text-left">{{ item.label }}</span>
            <kbd v-if="item.shortcut" class="font-sans text-11px text-ink-tertiary">{{ item.shortcut }}</kbd>
          </button>
        </template>
      </div>
    </Teleport>
  </span>
</template>

<style>
.on-menu {
  background: var(--on-surface-overlay);
  backdrop-filter: blur(var(--on-effect-backdrop-blur));
  -webkit-backdrop-filter: blur(var(--on-effect-backdrop-blur));
}
</style>
