<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue';
import { OnIcon } from '@opennavo/ui';
import { useHermesContent } from '@/composables/useHermesContent';
import { useToasts } from '@/composables/useToasts';

const props = defineProps<{ text: string }>();

const content = useHermesContent();
const toasts = useToasts();
const copied = ref(false);
let copiedTimer: ReturnType<typeof setTimeout> | undefined;

async function copy() {
  try {
    await navigator.clipboard.writeText(props.text);
    copied.value = true;
    clearTimeout(copiedTimer);
    copiedTimer = setTimeout(() => {
      copied.value = false;
    }, 1500);
  } catch (error) {
    toasts.push({
      tone: 'warning',
      title: content.value.common.copyFailed,
      description: error instanceof Error ? error.message : String(error)
    });
  }
}

onBeforeUnmount(() => clearTimeout(copiedTimer));
</script>

<template>
  <button
    type="button"
    class="m-0 inline-flex h-24px items-center gap-5px rounded-tiny border-none bg-transparent px-4px font-sans text-11px outline-none transition-colors duration-fast hover:bg-accent-coral-subtle focus-visible:shadow-focus-ring"
    :class="copied ? 'bg-accent-coral-subtle text-ink-accent' : 'text-ink-accent'"
    :aria-label="copied ? content.common.copied : content.common.copy"
    @click="copy"
  >
    <OnIcon :name="copied ? 'lucide:check' : 'lucide:copy'" :size="13" />
    <span aria-live="polite">{{ copied ? content.common.copied : content.common.copy }}</span>
  </button>
</template>
