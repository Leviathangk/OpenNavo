<script setup lang="ts">
import { ref } from 'vue';

// Shared app shell (08 §9.1, ADR-017): 224 sidebar, content column with toolbar/main/288 rail; layout only.
// Host supplies all regions through slots. Two scroll modes:
// - container (desktop): fixed window height, independently scrolling main area.
// - document (web): document scrolling, sticky sidebar/toolbar; below md hide both and use compactBar mobile menu.
// Rail sticks right at xl+, moves below main content otherwise (05 §11.2), followed by footer.
export interface OnAppShellProps {
  /** Current page has a rail. */
  hasRail?: boolean;
  /** Rail remains visible according to host width checks (08 §9.1: ≥1240). */
  railDocked?: boolean;
  /** When not persistent, whether the toolbar-controlled rail overlay is open. */
  railOpen?: boolean;
  scroll?: 'container' | 'document';
}

const props = withDefaults(defineProps<OnAppShellProps>(), {
  hasRail: false,
  railDocked: true,
  railOpen: false,
  scroll: 'container'
});

defineSlots<{
  sidebar?: () => unknown;
  toolbar?: () => unknown;
  compactBar?: () => unknown;
  default?: () => unknown;
  rail?: () => unknown;
  /** Document mode only: footer after main content/rail. */
  footer?: () => unknown;
  overlay?: () => unknown;
}>();

const main = ref<HTMLElement>();

/** Reset scroll on navigation; container mode scrolls main content, not window. */
function scrollToTop() {
  if (props.scroll === 'container') main.value?.scrollTo({ top: 0 });
  else window.scrollTo({ top: 0 });
}

defineExpose({ scrollToTop });
</script>

<template>
  <div
    v-if="scroll === 'container'"
    class="relative grid h-screen grid-cols-[224px_minmax(0,1fr)] overflow-hidden bg-surface-base text-ink-primary"
  >
    <slot name="sidebar" />
    <div class="flex min-h-0 min-w-0 flex-col">
      <slot name="toolbar" />
      <div class="relative flex min-h-0 flex-1">
        <main ref="main" class="min-w-0 flex-1 overflow-y-auto px-28px pb-28px pt-6px">
          <slot />
        </main>
        <div v-if="hasRail && railDocked" class="w-288px shrink-0">
          <slot name="rail" />
        </div>
        <div v-else-if="hasRail && railOpen" class="absolute bottom-0 right-0 top-0 z-dropdown w-288px shadow-popover">
          <slot name="rail" />
        </div>
      </div>
    </div>
    <slot name="overlay" />
  </div>
  <div v-else class="relative min-h-screen bg-surface-base text-ink-primary md:grid md:grid-cols-[224px_minmax(0,1fr)]">
    <div v-if="$slots.compactBar" class="sticky top-0 z-sticky md:hidden">
      <slot name="compactBar" />
    </div>
    <div class="sticky top-0 hidden h-screen md:block">
      <slot name="sidebar" />
    </div>
    <div class="flex min-w-0 flex-col">
      <div class="sticky top-0 z-sticky hidden bg-surface-base md:block">
        <slot name="toolbar" />
      </div>
      <div class="relative flex flex-1 flex-col xl:flex-row xl:items-start">
        <main
          id="main-content"
          ref="main"
          tabindex="-1"
          class="box-border min-w-0 flex-1 px-16px pb-40px pt-6px md:px-28px"
        >
          <slot />
        </main>
        <!-- Render once: sticky under toolbar with independent scrolling at xl+, otherwise normal flow below main. -->
        <div v-if="hasRail" class="xl:sticky xl:top-52px xl:h-[calc(100vh-52px)] xl:w-288px xl:shrink-0">
          <slot name="rail" />
        </div>
      </div>
      <slot name="footer" />
    </div>
    <slot name="overlay" />
  </div>
</template>
