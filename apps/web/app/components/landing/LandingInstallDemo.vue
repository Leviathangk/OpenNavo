<script setup lang="ts">
import { installCommand } from '@opennavo/shared';
import { OnAppIcon, OnGetButton, useUiMessages } from '@opennavo/ui';
import type { GetState } from '@opennavo/ui';

// Install demo (08 §10.14): Get → progress ring → Open; Homebrew terminal output appears below line by line.
// Step zero is installed with complete logs; SSR/reduced motion stay there.
const props = defineProps<{ app: LandingApp }>();

const DURATIONS = [3000, 1200, 650, 650, 650, 450] as const;
const LINES = [4, 0, 1, 2, 3, 3] as const;
const PROGRESS = [100, 0, 6, 38, 72, 100] as const;

const messages = useUiMessages();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);

const log = computed(() => installLog(props.app.token));
const visible = computed(() => log.value.slice(0, LINES[step.value] ?? log.value.length));
const state = computed<GetState>(() => (step.value === 0 ? 'open' : step.value === 1 ? 'get' : 'running'));

function lineClass(line: string): string {
  if (line.startsWith('$')) return 'text-ink-primary';
  if (line.startsWith('==>')) return 'text-component-log-heading';
  return 'text-component-log-success';
}
</script>

<template>
  <div ref="root" class="mx-auto flex max-w-460px flex-col gap-12px" inert aria-hidden="true">
    <div class="rounded-big border border-solid border-line-default bg-surface-base p-18px shadow-popover">
      <div class="flex items-center gap-14px">
        <OnAppIcon
          kind="cask"
          :token="app.token"
          :name="app.name"
          :src="app.iconUrl"
          :variants="app.iconVariants"
          :accent="app.accentColor"
          :size="64"
        />
        <div class="flex min-w-0 flex-1 flex-col gap-4px">
          <span class="truncate text-17px font-600 text-ink-primary">{{ app.name }}</span>
          <span v-if="app.summary" class="line-clamp-2 text-12.5px leading-[1.5] text-ink-tertiary">{{
            app.summary
          }}</span>
        </div>
        <OnGetButton :state="state" :progress="state === 'running' ? PROGRESS[step] : undefined" />
      </div>
      <div class="mt-16px flex flex-col gap-6px border-t border-t-solid border-line-subtle pt-14px">
        <span class="text-11.5px text-ink-tertiary">{{ messages.installCommand.title }}</span>
        <code class="truncate font-mono text-12px text-component-mono-text">{{
          installCommand('cask', app.token)
        }}</code>
      </div>
    </div>
    <div
      class="box-border flex h-128px flex-col gap-4px overflow-hidden rounded-default border border-solid border-line-subtle bg-surface-inset px-14px py-12px font-mono text-11.5px leading-[1.7]"
    >
      <span
        v-for="(line, index) in visible"
        :key="`${index}-${line}`"
        class="landing-log-line block truncate"
        :class="lineClass(line)"
        >{{ line }}</span
      >
    </div>
  </div>
</template>

<style>
.landing-log-line {
  animation: landing-line 0.36s var(--on-motion-easing-standard) both;
}

@keyframes landing-line {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
}
</style>
