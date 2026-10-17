<script setup lang="ts">
import { computed } from 'vue';
import OnAppIcon from './OnAppIcon.vue';
import type { OnAppIconKind } from './OnAppIcon.vue';
import OnButton from './OnButton.vue';
import OnLogViewer from './OnLogViewer.vue';
import OnProgressMeter from './OnProgressMeter.vue';
import { useUiMessages } from '../composables/locale';

export interface OnTaskPackage {
  kind: OnAppIconKind;
  token: string;
  name: string;
  src?: string | null;
  accent?: string | null;
}

export interface OnTaskCardProps {
  /** Updating Docker Desktop. */
  title: string;
  /** 4.92.1 → 4.93.0 · Step 2 of 4: Downloading. */
  subtitle?: string;
  pkg?: OnTaskPackage;
  /** 0–100; omitted means indeterminate. */
  progress?: number | null;
  /** Downloaded bytes in bold, total size, speed. */
  transferred?: string;
  total?: string;
  speed?: string;
  /** About 13 seconds remaining. */
  remaining?: string;
  /** Logs, last five lines. */
  log?: readonly string[];
  /** Log expansion v-model:logExpanded, expanded by default. */
  logExpanded?: boolean;
  queue?: readonly OnTaskPackage[];
  /** Queue-row caption, e.g. brew runs one task at a time. */
  queueNote?: string;
  /** Show Cancel. */
  cancellable?: boolean;
}

const props = withDefaults(defineProps<OnTaskCardProps>(), {
  log: () => [],
  logExpanded: true,
  queue: () => [],
  cancellable: true
});

const emit = defineEmits<{
  cancel: [];
  'update:logExpanded': [expanded: boolean];
}>();

const messages = useUiMessages();

// 372 MB / 600 MB · 18.4 MB/s: downloaded bytes bold, remainder tertiary text.
const rest = computed(() => {
  const parts = [props.total ? `/ ${props.total}` : '', props.speed ? `· ${props.speed}` : ''].filter(Boolean);
  return parts.join(' ');
});
const hasStats = computed(() => Boolean(props.transferred || props.total || props.speed || props.remaining));
</script>

<template>
  <section
    class="box-border rounded-big border border-solid border-component-running-border bg-surface-card px-18px py-16px font-sans"
    :aria-label="title"
  >
    <div class="flex items-center gap-12px">
      <OnAppIcon
        v-if="pkg"
        :kind="pkg.kind"
        :token="pkg.token"
        :name="pkg.name"
        :src="pkg.src"
        :accent="pkg.accent"
        :size="40"
      />
      <div class="min-w-0 flex-1">
        <p class="m-0 truncate text-14px font-600 text-ink-primary">{{ title }}</p>
        <p v-if="subtitle" class="m-0 mt-2px truncate text-12px text-ink-tertiary">{{ subtitle }}</p>
      </div>
      <OnButton v-if="cancellable" variant="ghost" size="sm" @click="emit('cancel')">
        {{ messages.getButton.cancel }}
      </OnButton>
    </div>

    <OnProgressMeter class="mt-14px" :value="progress" :label="title" />

    <div v-if="hasStats || log.length" class="mt-8px flex items-center gap-8px text-11.5px text-ink-tertiary">
      <span class="min-w-0 flex-1 truncate">
        <b v-if="transferred" class="font-500 text-ink-primary">{{ transferred }}</b>
        <template v-if="rest">{{ transferred ? ' ' : '' }}{{ rest }}</template>
      </span>
      <span v-if="remaining" class="shrink-0">{{ remaining }}</span>
      <OnButton
        v-if="log.length"
        variant="ghost"
        size="xs"
        icon-only
        :icon="logExpanded ? 'chevron-up' : 'chevron-down'"
        :aria-label="logExpanded ? messages.task.collapseLog : messages.task.expandLog"
        :aria-expanded="logExpanded ? 'true' : 'false'"
        @click="emit('update:logExpanded', !logExpanded)"
      />
    </div>

    <OnLogViewer v-if="log.length && logExpanded" class="mt-12px" :lines="log" :tail="5" />

    <div v-if="queue.length" class="mt-10px flex items-center gap-8px text-12px text-ink-tertiary">
      <span class="shrink-0">{{ messages.task.queued }}</span>
      <span
        v-for="item in queue"
        :key="item.token"
        class="inline-flex shrink-0 items-center gap-6px text-ink-secondary"
      >
        <OnAppIcon
          :kind="item.kind"
          :token="item.token"
          :name="item.name"
          :src="item.src"
          :accent="item.accent"
          :size="20"
        />
        {{ item.name }}
      </span>
      <span v-if="queueNote" class="ml-auto truncate pl-12px">{{ queueNote }}</span>
    </div>
  </section>
</template>
