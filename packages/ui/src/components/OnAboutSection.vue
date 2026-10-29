<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useUiMessages } from '../composables/locale';

// Overview introduction (05 §11.3, 12 D3, ADR-017): heading plus host-rendered Markdown body via default slot; renderer lives in
// @opennavo/ui/markdown. Collapse beyond twelve lines with expansion.
// Informational only, without review or reading restrictions.
export interface OnAboutSectionProps {
  /** Collapsed line count at 14 px / 1.75 line height. */
  lines?: number;
  headingLevel?: 2 | 3;
}

const props = withDefaults(defineProps<OnAboutSectionProps>(), {
  lines: 12,
  headingLevel: 2
});

defineSlots<{ default?: () => unknown }>();

const messages = useUiMessages();
const heading = computed(() => `h${props.headingLevel}`);

const body = ref<HTMLElement>();
const inner = ref<HTMLElement>();
const expanded = ref(false);
// SSR/first paint starts collapsed; measure after mounting and hide expansion if unnecessary; remeasure resized/replaced bodies.
const overflowing = ref(false);
let observer: ResizeObserver | undefined;

function measure() {
  const el = body.value;
  const content = inner.value;
  if (!el || !content) return;
  overflowing.value = content.scrollHeight > el.clientHeight + 1 || (expanded.value && content.scrollHeight > 0);
}

onMounted(() => {
  measure();
  if (typeof ResizeObserver !== 'undefined' && inner.value) {
    observer = new ResizeObserver(measure);
    observer.observe(inner.value);
  }
});
onBeforeUnmount(() => observer?.disconnect());
</script>

<template>
  <section class="flex flex-col gap-10px">
    <header class="flex flex-wrap items-center gap-8px">
      <component :is="heading" class="m-0 text-headline text-ink-primary">{{ messages.about.title }}</component>
    </header>
    <div
      ref="body"
      class="relative text-14px"
      :class="expanded ? '' : overflowing ? 'on-about-collapsed on-about-fade' : 'on-about-collapsed'"
      :style="{ '--on-about-lines': lines }"
    >
      <div ref="inner"><slot /></div>
    </div>
    <button
      v-if="overflowing"
      type="button"
      class="m-0 self-start rounded-tiny border-none bg-transparent p-0 font-sans text-13px text-ink-secondary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
      :aria-expanded="expanded ? 'true' : 'false'"
      @click="expanded = !expanded"
    >
      {{ expanded ? messages.about.collapse : messages.about.expand }}
    </button>
  </section>
</template>

<style>
/* Collapse at twelve 14×1.75 lines; actual overflow fades to page background to indicate more content. */
.on-about-collapsed {
  max-height: calc(var(--on-about-lines) * 1.75em);
  overflow: hidden;
}

.on-about-fade::after {
  content: '';
  position: absolute;
  right: 0;
  bottom: 0;
  left: 0;
  height: 3em;
  background: linear-gradient(to bottom, transparent, var(--on-surface-base));
  pointer-events: none;
}
</style>
