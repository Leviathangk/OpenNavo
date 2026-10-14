<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import OnIcon from './OnIcon.vue';
import { useUiMessages } from '../composables/locale';
import { commandBreak } from '../utils/command';

export interface OnInstallCommandProps {
  command: string;
  /** Title defaults to Install command; desktop clarifies this is the command OpenNavo executes. */
  title?: string;
  /** Beyond this length, wrap after --cask/--formula when the command does not fit its row. */
  wrapAt?: number;
  /** Copy implementation, browser clipboard by default; desktop may supply a plugin. */
  copy?: (text: string) => Promise<void>;
}

const props = withDefaults(defineProps<OnInstallCommandProps>(), { wrapAt: 30 });

const emit = defineEmits<{ copied: [command: string]; copyFailed: [error: unknown] }>();

const messages = useUiMessages();
const copied = ref(false);
let timer: ReturnType<typeof setTimeout> | undefined;

const split = computed(() => commandBreak(props.command, props.wrapAt));
// Column counts for the clamps below: the head with its "$ " prompt, and the whole command on one line.
const columns = computed(() => {
  const value = split.value;
  if (!value) return undefined;
  return {
    '--on-command-head': `${value.head.length + 2}ch`,
    '--on-command-line': `${value.head.length + value.tail.length + 3}ch`
  };
});

async function copyCommand() {
  const write = props.copy ?? ((text: string) => navigator.clipboard.writeText(text));
  try {
    await write(props.command);
    copied.value = true;
    emit('copied', props.command);
    clearTimeout(timer);
    // After copying, show Copied for 1.5 seconds (08 §8.19).
    timer = setTimeout(() => {
      copied.value = false;
    }, 1500);
  } catch (error) {
    emit('copyFailed', error);
  }
}

onBeforeUnmount(() => clearTimeout(timer));
</script>

<template>
  <div class="flex flex-col gap-10px">
    <div class="flex items-center justify-between gap-12px">
      <span class="text-11.5px text-ink-tertiary">{{ title ?? messages.installCommand.title }}</span>
      <button
        type="button"
        class="m-0 inline-flex items-center gap-4px rounded-tiny border-none bg-transparent p-0 font-sans text-12px text-ink-secondary outline-none transition-colors duration-fast hover:text-ink-primary focus-visible:shadow-focus-ring"
        @click="copyCommand"
      >
        <OnIcon :name="copied ? 'lucide:check' : 'lucide:copy'" :size="13" />
        <span aria-live="polite">{{ copied ? messages.action.copied : messages.action.copy }}</span>
      </button>
    </div>
    <pre
      class="m-0 overflow-x-auto rounded-default border border-solid border-line-subtle bg-surface-inset px-14px py-12px font-mono text-11.5px leading-[1.6] text-ink-primary"
    ><code v-if="split" class="flex flex-wrap" :style="columns"><span class="on-command-head shrink-0 whitespace-pre"><span class="select-none text-brand-salmon">$ </span>{{ split.head }} \</span><span class="on-command-tail shrink-0 whitespace-pre">{{ split.tail }}</span></code><code v-else><span class="block whitespace-pre"><span class="select-none text-brand-salmon">$ </span>{{ command.trim() }}</span></code></pre>
  </div>
</template>

<style scoped>
/*
 * A long command stays on one line while it fits its row and otherwise wraps after --cask/--formula.
 * Each clamp acts as a switch: (one-line width - row width) * 999 lands far below the minimum when the
 * line fits and far above the maximum when it does not. Fitting rows clip the head's trailing " \"
 * and leave one column before the tail; narrow rows show " \" and indent the wrapped tail six
 * columns, under the command after its "$ " prompt.
 */
.on-command-head {
  overflow: hidden;
  width: clamp(var(--on-command-head), (var(--on-command-line) - 100%) * 999, var(--on-command-head) + 2ch);
}

.on-command-tail {
  padding-left: clamp(1ch, (var(--on-command-line) - 100%) * 999, 6ch);
}
</style>
