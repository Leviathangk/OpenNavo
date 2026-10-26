<script setup lang="ts">
import { OnAppIcon, OnIcon, useUiMessages } from '@opennavo/ui';

// Command-K demo (05 §7, S-03 alias): type vscode character by character, find Visual Studio Code. Step zero is fully typed.
const props = defineProps<{ app: LandingApp | null }>();

const QUERY = 'vscode';
const RESULT = { token: 'visual-studio-code', name: 'Visual Studio Code' };
const DURATIONS = [3000, 700, 160, 160, 160, 160, 160] as const;
const TYPED = [6, 0, 1, 2, 3, 4, 5] as const;

const { t } = useI18n();
const messages = useUiMessages();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);
const typed = computed(() => QUERY.slice(0, TYPED[step.value] ?? QUERY.length));
// Use VS Code's homepage icon when available.
const icon = computed(() => (props.app?.token === RESULT.token ? props.app : null));
</script>

<template>
  <div
    ref="root"
    class="mx-auto max-w-300px overflow-hidden rounded-panel border border-solid border-line-default bg-surface-overlay shadow-popover"
    inert
    aria-hidden="true"
  >
    <div class="flex items-center gap-8px border-b border-b-solid border-line-subtle px-12px py-10px">
      <OnIcon name="search" :size="15" class="text-ink-tertiary" />
      <span class="min-w-0 flex-1 truncate text-13px text-ink-primary">
        <span v-if="typed">{{ typed }}</span>
        <span v-else class="text-ink-tertiary">{{ messages.palette.placeholder }}</span>
        <span class="landing-caret ml-1px inline-block h-14px w-1.5px translate-y-2px bg-brand-coral"></span>
      </span>
      <kbd class="rounded-tiny bg-surface-chip px-5px py-1px font-sans text-10.5px text-ink-tertiary">⌘K</kbd>
    </div>
    <div class="landing-search-results px-6px pb-8px pt-6px" :class="typed.length >= 3 ? 'is-shown' : ''">
      <span class="block px-6px pb-4px text-10.5px text-ink-tertiary">{{ t('palette.packages') }}</span>
      <span class="flex items-center gap-8px rounded-small bg-component-item-active px-6px py-6px">
        <OnAppIcon
          kind="cask"
          :token="RESULT.token"
          :name="RESULT.name"
          :src="icon?.iconUrl"
          :variants="icon?.iconVariants"
          :accent="icon?.accentColor"
          :size="26"
        />
        <span class="min-w-0 flex-1 truncate text-12.5px font-600 text-ink-primary">{{ RESULT.name }}</span>
        <span class="text-10.5px text-ink-tertiary">{{ messages.palette.hintOpen }}</span>
      </span>
    </div>
  </div>
</template>

<style>
.landing-caret {
  animation: landing-blink 1s steps(1) infinite;
}

.landing-search-results {
  opacity: 0;
  transform: translateY(-4px);
  transition:
    opacity var(--on-motion-duration-base) var(--on-motion-easing-standard),
    transform var(--on-motion-duration-base) var(--on-motion-easing-standard);
}

.landing-search-results.is-shown {
  opacity: 1;
  transform: none;
}

@keyframes landing-blink {
  50% {
    opacity: 0;
  }
}
</style>
