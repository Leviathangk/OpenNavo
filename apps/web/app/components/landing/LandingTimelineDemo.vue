<script setup lang="ts">
import { formatVersion } from '@opennavo/shared';
import { OnAppIcon, OnChip, OnIcon, useUiMessages } from '@opennavo/ui';

// Release demo (08 §10.14): recent apps with real API versions enter from above like a feed; below, OpenNavo-authored
// notes use placeholder bars without invented content. Step zero is the representative frame.
const props = defineProps<{ apps: readonly LandingApp[] }>();

const VISIBLE = 4;
const DURATIONS = Array.from({ length: Math.max(1, props.apps.length) }, () => 2400);

const messages = useUiMessages();
const root = ref<HTMLElement>();
const step = useDemoLoop(root, DURATIONS);

// At step k, shift the list by k positions so the new item appears first.
const entries = computed(() => {
  const total = props.apps.length;
  return Array.from(
    { length: Math.min(VISIBLE, total) },
    (_, index) => props.apps[(total - step.value + index) % total]
  ).filter((app): app is LandingApp => Boolean(app));
});
const latest = computed(() => entries.value[0]);
</script>

<template>
  <div ref="root" class="mx-auto flex max-w-460px flex-col gap-12px" inert aria-hidden="true">
    <div
      class="rounded-big border border-solid border-line-default bg-surface-base px-16px pb-8px pt-14px shadow-popover"
    >
      <div class="flex items-center gap-8px pb-10px text-13px font-600 text-ink-primary">
        <OnIcon name="rotate-ccw-clock" :size="16" class="text-ink-tertiary" />
        {{ messages.version.timeline }}
      </div>
      <TransitionGroup tag="ol" name="landing-feed" class="relative m-0 list-none p-0">
        <li
          v-for="(app, index) in entries"
          :key="app.token"
          class="landing-feed-item flex items-center gap-12px border-t border-t-solid border-line-subtle py-10px"
        >
          <span
            class="h-8px w-8px shrink-0 rounded-full"
            :class="index === 0 ? 'landing-feed-dot bg-brand-coral' : 'bg-component-timeline-dot'"
          ></span>
          <OnAppIcon
            kind="cask"
            :token="app.token"
            :name="app.name"
            :src="app.iconUrl"
            :variants="app.iconVariants"
            :accent="app.accentColor"
            :size="32"
          />
          <span class="flex min-w-0 flex-1 flex-col">
            <span class="truncate text-13.5px font-600 text-ink-primary">{{ app.name }}</span>
            <span v-if="app.version" class="truncate font-mono text-11.5px text-component-mono-text">{{
              formatVersion(app.version)
            }}</span>
          </span>
          <OnChip tone="outline">{{ messages.version.sources.homebrew }}</OnChip>
        </li>
      </TransitionGroup>
    </div>
    <div v-if="latest" class="rounded-big border border-solid border-line-subtle bg-surface-base px-16px py-14px">
      <div class="flex flex-wrap items-center gap-8px">
        <Transition name="landing-fade" mode="out-in">
          <span :key="latest.token" class="truncate text-13px font-600 text-ink-primary">{{ latest.name }}</span>
        </Transition>
        <OnChip tone="accent">{{ messages.version.sources.editorial }}</OnChip>
      </div>
      <div class="mt-12px flex flex-col gap-8px">
        <span class="landing-skeleton h-8px w-11/12 rounded-full"></span>
        <span class="landing-skeleton h-8px w-9/12 rounded-full"></span>
        <span class="landing-skeleton h-8px w-10/12 rounded-full"></span>
      </div>
    </div>
  </div>
</template>

<style>
.landing-feed-move,
.landing-feed-enter-active,
.landing-feed-leave-active {
  transition:
    transform 0.6s var(--on-motion-easing-emphasized),
    opacity 0.6s var(--on-motion-easing-emphasized);
}

.landing-feed-enter-from {
  opacity: 0;
  transform: translateY(-14px);
}

.landing-feed-leave-to {
  opacity: 0;
  transform: translateY(14px);
}

/* Remove exiting items from flow so remaining items move smoothly. */
.landing-feed-leave-active {
  position: absolute;
  left: 0;
  right: 0;
}

.landing-feed-dot {
  box-shadow: 0 0 0 4px var(--on-accent-coral-subtle);
}

.landing-fade-enter-active,
.landing-fade-leave-active {
  transition: opacity var(--on-motion-duration-base) var(--on-motion-easing-standard);
}

.landing-fade-enter-from,
.landing-fade-leave-to {
  opacity: 0;
}

/* Release-note placeholders with a slow sweeping highlight. */
.landing-skeleton {
  position: relative;
  overflow: hidden;
  background: var(--on-surface-raised);
}

.landing-skeleton::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(
    90deg,
    transparent,
    color-mix(in srgb, var(--on-text-primary) 8%, transparent),
    transparent
  );
  animation: landing-shimmer 2.4s ease-in-out infinite;
}

@keyframes landing-shimmer {
  from {
    transform: translateX(-100%);
  }
  to {
    transform: translateX(100%);
  }
}
</style>
