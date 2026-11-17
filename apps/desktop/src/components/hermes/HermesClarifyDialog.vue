<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { OnButton, OnModal, runOnEnter } from '@opennavo/ui';
import type { HermesClarifyAnswer, HermesClarifyQuestion, HermesClarifyRequest } from '@/stores/hermes';
import { useHermesContent } from '@/composables/useHermesContent';
import { useAskCountdown } from '@/composables/useAskCountdown';

const props = defineProps<{
  open: boolean;
  request: HermesClarifyRequest | null;
}>();

const emit = defineEmits<{
  submit: [answers: HermesClarifyAnswer[]];
  cancel: [];
}>();

const content = useHermesContent();
const { label: remaining } = useAskCountdown(() => props.request?.deadline);

/** Per-question draft: a string for free text, an array for a multi-select. */
const drafts = ref<Record<string, string | string[]>>({});

const questions = computed<HermesClarifyQuestion[]>(() => props.request?.questions ?? []);

// A new card starts blank; the runtime replays locked answers on reconnect only as `answers`, which
// the card shows as already-answered text rather than pretending the user typed it.
watch(
  () => props.request?.requestId,
  () => {
    drafts.value = {};
  },
  { immediate: true }
);

function draftOf(qid: string): string | string[] {
  return drafts.value[qid] ?? '';
}

function textOf(qid: string): string {
  const draft = draftOf(qid);
  return Array.isArray(draft) ? '' : draft;
}

function chosen(qid: string): string[] {
  const draft = draftOf(qid);
  return Array.isArray(draft) ? draft : [];
}

function onInput(qid: string, event: Event) {
  const target = event.target as { value?: unknown } | null;
  drafts.value = { ...drafts.value, [qid]: typeof target?.value === 'string' ? target.value : '' };
}

function toggleChoice(qid: string, choice: string, multi: boolean) {
  if (!multi) {
    drafts.value = { ...drafts.value, [qid]: chosen(qid)[0] === choice ? '' : choice };
    return;
  }
  const current = chosen(qid);
  const next = current.includes(choice) ? current.filter(item => item !== choice) : [...current, choice];
  drafts.value = { ...drafts.value, [qid]: next };
}

/** `null` (not `''`) is the runtime's "this question was skipped". */
function submit() {
  const answers = questions.value.map(question => {
    const draft = drafts.value[question.qid] ?? '';
    const answer = Array.isArray(draft) ? draft.join(', ') : draft.trim();
    return { qid: question.qid, answer: answer ? answer : null };
  });
  emit('submit', answers);
}
</script>

<template>
  <OnModal :open="open" :title="content.ask.clarifyTitle" size="md" role="alertdialog" @close="emit('cancel')">
    <div v-if="request" class="flex flex-col gap-14px">
      <div v-for="question in questions" :key="question.qid" class="flex flex-col gap-7px">
        <span class="text-12px leading-[1.5] text-ink-primary">{{ question.question }}</span>
        <div v-if="question.choices?.length" class="flex flex-wrap gap-6px">
          <OnButton
            v-for="choice in question.choices"
            :key="choice"
            size="sm"
            :variant="chosen(question.qid).includes(choice) ? 'accent' : 'secondary'"
            :aria-pressed="chosen(question.qid).includes(choice)"
            @click="toggleChoice(question.qid, choice, Boolean(question.multi_select))"
            >{{ choice }}</OnButton
          >
        </div>
        <input
          v-else
          :value="textOf(question.qid)"
          :aria-label="question.question"
          :placeholder="content.ask.clarifyPlaceholder"
          class="w-full rounded-small border border-solid border-line-default bg-surface-inset px-10px py-7px text-12px text-ink-primary outline-none focus-visible:shadow-focus-ring"
          @input="onInput(question.qid, $event)"
          @keydown.enter="runOnEnter($event, submit)"
        />
      </div>
      <span v-if="remaining" class="text-11px tabular-nums text-ink-tertiary">
        {{ content.ask.expires.replace('{time}', remaining) }}
      </span>
    </div>
    <template #footer>
      <OnButton variant="ghost" @click="emit('cancel')">{{ content.ask.cancel }}</OnButton>
      <OnButton variant="accent" @click="submit">{{ content.ask.clarifySubmit }}</OnButton>
    </template>
  </OnModal>
</template>
