<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue';

export interface OnTooltipProps {
  content: string;
  placement?: 'top' | 'bottom' | 'left' | 'right';
  /** Show delay in milliseconds (08 §8.24: 400). */
  delay?: number;
  disabled?: boolean;
}

const props = withDefaults(defineProps<OnTooltipProps>(), { placement: 'top', delay: 400 });
defineOptions({ inheritAttrs: false });

defineSlots<{ default?: () => unknown }>();

const id = useId();
const root = ref<HTMLElement>();
const tooltip = ref<HTMLElement>();
const open = ref(false);
const positioned = ref(false);
const position = ref({ left: '0px', top: '0px' });
let timer: ReturnType<typeof setTimeout> | undefined;

// Keep even hidden descriptions outside scrolling content. Visibility alone does
// not stop absolutely positioned tooltips from enlarging an ancestor's scroll area.
async function place() {
  await nextTick();
  if (!open.value || !root.value || !tooltip.value) return;
  const anchor = root.value.getBoundingClientRect();
  const { width, height } = tooltip.value.getBoundingClientRect();
  const inset = 8;
  const gap = 6;
  const space = {
    top: anchor.top - inset - gap,
    bottom: window.innerHeight - anchor.bottom - inset - gap,
    left: anchor.left - inset - gap,
    right: window.innerWidth - anchor.right - inset - gap
  };
  let side = props.placement;
  const opposite = { top: 'bottom', bottom: 'top', left: 'right', right: 'left' } as const;
  const required = side === 'top' || side === 'bottom' ? height : width;
  if (space[side] < required && space[opposite[side]] > space[side]) side = opposite[side];

  let left = anchor.left + (anchor.width - width) / 2;
  let top = anchor.top + (anchor.height - height) / 2;
  if (side === 'top') top = anchor.top - height - gap;
  else if (side === 'bottom') top = anchor.bottom + gap;
  else if (side === 'left') left = anchor.left - width - gap;
  else left = anchor.right + gap;
  position.value = {
    left: `${Math.max(inset, Math.min(left, window.innerWidth - width - inset))}px`,
    top: `${Math.max(inset, Math.min(top, window.innerHeight - height - inset))}px`
  };
  positioned.value = true;
}

function listen(active: boolean) {
  const method = active ? 'addEventListener' : 'removeEventListener';
  window[method]('scroll', hide, true);
  window[method]('resize', hide);
  if (active) window.addEventListener('keydown', onKeydown);
  else window.removeEventListener('keydown', onKeydown);
}

function show() {
  if (props.disabled || !props.content) return;
  clearTimeout(timer);
  timer = setTimeout(() => {
    positioned.value = false;
    open.value = true;
    listen(true);
    void place();
  }, props.delay);
}

function hide() {
  clearTimeout(timer);
  open.value = false;
  positioned.value = false;
  listen(false);
}

function onKeydown(event: KeyboardEvent) {
  // WCAG 1.4.13: Escape dismisses tooltips.
  if (event.key === 'Escape' && open.value) hide();
}

// Screen readers read tooltips through the trigger's aria-describedby.
function describeTrigger() {
  const trigger = root.value?.firstElementChild;
  if (!trigger || trigger.id === id) return;
  if (props.disabled || !props.content) trigger.removeAttribute('aria-describedby');
  else trigger.setAttribute('aria-describedby', id);
}

onMounted(describeTrigger);
watch(() => [props.disabled, props.content], describeTrigger);
watch(
  () => [props.content, props.placement],
  () => {
    if (!props.content) hide();
    else if (open.value) void place();
  }
);
watch(
  () => props.disabled,
  disabled => {
    if (disabled) hide();
  }
);
onBeforeUnmount(hide);
</script>

<template>
  <span
    ref="root"
    class="relative inline-flex"
    v-bind="$attrs"
    @mouseenter="show"
    @mouseleave="hide"
    @focusin="show"
    @focusout="hide"
  >
    <slot />
  </span>
  <Teleport to="body">
    <span
      v-show="open"
      :id="id"
      ref="tooltip"
      role="tooltip"
      class="pointer-events-none fixed z-tooltip box-border w-max max-w-[min(240px,calc(100vw-16px))] max-h-[calc(100vh-16px)] overflow-hidden rounded-small border border-solid border-line-default bg-surface-control px-9px py-6px font-sans text-11.5px font-400 leading-16px text-ink-primary shadow-popover [overflow-wrap:anywhere]"
      :class="positioned ? 'visible opacity-100' : 'invisible opacity-0'"
      :style="position"
    >
      {{ content }}
    </span>
  </Teleport>
</template>
