<script setup lang="ts">
import { ref, watch } from 'vue';
import { OnButton, OnIcon, OnModal, runOnEnter } from '@opennavo/ui';
import type { HermesSecretRequest } from '@/stores/hermes';
import { useHermesContent } from '@/composables/useHermesContent';
import { useAskCountdown } from '@/composables/useAskCountdown';

const props = defineProps<{
  open: boolean;
  request: HermesSecretRequest | null;
}>();

const emit = defineEmits<{
  submit: [value: string];
  skip: [];
}>();

const content = useHermesContent();
const { label: remaining } = useAskCountdown(() => props.request?.deadline);
const value = ref('');

function submit() {
  emit('submit', value.value);
}

// Never carry a value across cards: a password typed for one prompt must not prefill the next.
watch(
  () => props.request?.requestId,
  () => {
    value.value = '';
  },
  { immediate: true }
);
</script>

<template>
  <OnModal
    :open="open"
    :title="content.ask.secretTitle"
    :description="request?.prompt || content.ask.secretBody"
    size="sm"
    role="alertdialog"
    @close="emit('skip')"
  >
    <div v-if="request" class="flex flex-col gap-10px">
      <span
        v-if="request.label"
        class="inline-flex w-fit items-center gap-5px rounded-full border border-solid border-line-subtle bg-surface-inset px-8px py-2px font-mono text-11px text-ink-tertiary"
      >
        <OnIcon name="lucide:key-round" :size="11" />
        {{ request.label }}
      </span>
      <input
        v-model="value"
        type="password"
        autocomplete="off"
        spellcheck="false"
        :aria-label="content.ask.secretTitle"
        :placeholder="content.ask.secretPlaceholder"
        class="w-full rounded-small border border-solid border-line-default bg-surface-inset px-10px py-8px font-mono text-12px text-ink-primary outline-none focus-visible:shadow-focus-ring"
        @keydown.enter="runOnEnter($event, submit)"
      />
      <span v-if="remaining" class="text-11px tabular-nums text-ink-tertiary">
        {{ content.ask.expires.replace('{time}', remaining) }}
      </span>
    </div>
    <template #footer>
      <OnButton variant="ghost" @click="emit('skip')">{{ content.ask.skip }}</OnButton>
      <OnButton variant="accent" @click="emit('submit', value)">{{ content.ask.secretSubmit }}</OnButton>
    </template>
  </OnModal>
</template>
