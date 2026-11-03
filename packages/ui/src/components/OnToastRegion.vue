<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue';
import OnToast from './OnToast.vue';
import type { OnToastTone } from './OnToast.vue';
import { useUiMessages } from '../composables/locale';

export interface OnToastItem {
  id: string;
  tone?: OnToastTone;
  title: string;
  description?: string;
  actionLabel?: string;
  dismissLabel?: string;
  time?: string;
  /** Auto-dismiss milliseconds, default 4000; zero disables auto-dismiss. */
  duration?: number;
}

export interface OnToastRegionProps {
  items: readonly OnToastItem[];
  /** Maximum visible toasts, default three; hide oldest first. */
  max?: number;
}

const props = withDefaults(defineProps<OnToastRegionProps>(), { max: 3 });

const emit = defineEmits<{
  /** Host removes entries after timeout, Close, or action clicks. */
  dismiss: [id: string];
  action: [id: string];
}>();

const messages = useUiMessages();

const DEFAULT_DURATION = 4000;

interface Timer {
  remaining: number;
  startedAt: number;
  handle?: ReturnType<typeof setTimeout>;
}

const timers = new Map<string, Timer>();
let paused = false;

function start(id: string, timer: Timer) {
  timer.startedAt = Date.now();
  timer.handle = setTimeout(() => {
    timers.delete(id);
    emit('dismiss', id);
  }, timer.remaining);
}

watch(
  () => props.items.map(item => item.id),
  ids => {
    for (const [id, timer] of timers) {
      if (!ids.includes(id)) {
        clearTimeout(timer.handle);
        timers.delete(id);
      }
    }
    for (const item of props.items) {
      const duration = item.duration ?? DEFAULT_DURATION;
      if (timers.has(item.id) || duration <= 0) continue;
      const timer: Timer = { remaining: duration, startedAt: 0 };
      timers.set(item.id, timer);
      if (!paused) start(item.id, timer);
    }
  },
  { immediate: true }
);

// Pause all timers while hovered/focused; resume remaining durations afterward.
function pause() {
  if (paused) return;
  paused = true;
  for (const timer of timers.values()) {
    clearTimeout(timer.handle);
    timer.remaining = Math.max(0, timer.remaining - (Date.now() - timer.startedAt));
  }
}

function resume() {
  if (!paused) return;
  paused = false;
  for (const [id, timer] of timers) start(id, timer);
}

function onFocusout(event: FocusEvent) {
  const next = event.relatedTarget;
  if (!(next instanceof Node) || !(event.currentTarget as HTMLElement).contains(next)) resume();
}

onBeforeUnmount(() => {
  for (const timer of timers.values()) clearTimeout(timer.handle);
  timers.clear();
});

const visible = computed(() => props.items.slice(-props.max));

function act(id: string) {
  emit('action', id);
  emit('dismiss', id);
}
</script>

<template>
  <section
    :aria-label="messages.toast.region"
    class="pointer-events-none fixed bottom-20px right-20px z-toast box-border flex max-w-[calc(100vw-40px)] flex-col items-end"
  >
    <TransitionGroup
      name="on-toast"
      tag="ol"
      class="m-0 flex list-none flex-col gap-10px p-0"
      aria-live="polite"
      @mouseenter="pause"
      @mouseleave="resume"
      @focusin="pause"
      @focusout="onFocusout"
    >
      <li v-for="item in visible" :key="item.id" class="pointer-events-auto">
        <OnToast
          :tone="item.tone"
          :title="item.title"
          :description="item.description"
          :action-label="item.actionLabel"
          :dismiss-label="item.dismissLabel"
          :time="item.time"
          @action="act(item.id)"
          @close="emit('dismiss', item.id)"
        />
      </li>
    </TransitionGroup>
  </section>
</template>

<style>
.on-toast-enter-active {
  transition:
    opacity var(--on-motion-duration-base) var(--on-motion-easing-standard),
    transform var(--on-motion-duration-base) var(--on-motion-easing-standard);
}

.on-toast-leave-active {
  transition: opacity var(--on-motion-duration-fast) var(--on-motion-easing-exit);
}

.on-toast-enter-from {
  opacity: 0;
  transform: translateY(8px);
}

.on-toast-leave-to {
  opacity: 0;
}
</style>
