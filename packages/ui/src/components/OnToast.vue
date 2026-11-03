<script setup lang="ts">
import OnButton from './OnButton.vue';
import OnIcon from './OnIcon.vue';
import { useUiMessages } from '../composables/locale';

export type OnToastTone = 'info' | 'success' | 'warning' | 'danger';

export interface OnToastProps {
  tone?: OnToastTone;
  title: string;
  description?: string;
  /** Action button, e.g. View for failures. */
  actionLabel?: string;
  /** Neutral acknowledgement beside the action, e.g. Got it. */
  dismissLabel?: string;
  /** Upper-right time, e.g. now or two minutes ago. */
  time?: string;
}

withDefaults(defineProps<OnToastProps>(), { tone: 'info' });

const emit = defineEmits<{ action: []; close: [] }>();

const messages = useUiMessages();

// 08 §6 icons: success check, failure x, warning triangle-alert.
const ICON: Record<OnToastTone, string> = {
  info: 'info',
  success: 'check',
  warning: 'triangle-alert',
  danger: 'x'
};

const TONE: Record<OnToastTone, string> = {
  info: 'text-status-info',
  success: 'text-status-success',
  warning: 'text-status-warning',
  danger: 'text-status-danger'
};
</script>

<template>
  <!-- Failures announce immediately as alerts; others use the outer polite live region. -->
  <div
    class="on-toast box-border flex w-330px max-w-full items-start gap-10px rounded-big border border-solid border-line-default py-11px pl-12px pr-8px font-sans shadow-toast"
    :role="tone === 'danger' ? 'alert' : undefined"
  >
    <OnIcon :name="ICON[tone]" :size="16" class="mt-1px shrink-0" :class="TONE[tone]" />
    <div class="min-w-0 flex-1">
      <p class="m-0 text-12.5px font-600 leading-[1.4] text-ink-primary">{{ title }}</p>
      <p v-if="description" class="m-0 mt-2px text-11.5px leading-[1.45] text-ink-secondary">{{ description }}</p>
      <div v-if="actionLabel || dismissLabel" class="mt-8px flex flex-wrap items-center gap-6px">
        <OnButton v-if="actionLabel" variant="secondary" size="xs" @click="emit('action')">
          {{ actionLabel }}
        </OnButton>
        <OnButton v-if="dismissLabel" variant="ghost" size="xs" @click="emit('close')">
          {{ dismissLabel }}
        </OnButton>
      </div>
    </div>
    <span v-if="time" class="mt-1px whitespace-nowrap text-10.5px text-ink-tertiary">{{ time }}</span>
    <button
      type="button"
      class="m-0 box-border inline-flex h-20px w-20px shrink-0 items-center justify-center rounded-tiny border-none bg-transparent p-0 text-ink-tertiary outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
      :aria-label="messages.toast.close"
      @click="emit('close')"
    >
      <OnIcon name="x" :size="14" />
    </button>
  </div>
</template>

<style>
.on-toast {
  background: var(--on-component-toast-bg);
  backdrop-filter: blur(var(--on-effect-backdrop-blur));
  -webkit-backdrop-filter: blur(var(--on-effect-backdrop-blur));
}
</style>
