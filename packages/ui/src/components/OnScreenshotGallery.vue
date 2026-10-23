<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref } from 'vue';
import { interpolate } from '@opennavo/shared';
import OnIcon from './OnIcon.vue';
import { useUiMessages } from '../composables/locale';
import { lockScroll, moveFocus, trapTab, unlockScroll } from '../composables/overlay';

export interface OnScreenshot {
  /** Original image for lightbox. */
  src: string;
  /** Gallery thumbnail, defaulting to original. */
  thumb?: string;
  /** Width-described candidates for the 318×200 thumbnail box (X3), e.g. from imageSrcset(). */
  thumbSrcset?: string;
  /** Lower-left caption, also image alt text. */
  caption?: string;
}

export interface OnScreenshotGalleryProps {
  items: readonly OnScreenshot[];
  /** Accessible section name, default Screenshots. */
  label?: string;
}

const props = defineProps<OnScreenshotGalleryProps>();

const messages = useUiMessages();

/** Lightbox index; null means closed. */
const current = ref<number | null>(null);
const dialog = ref<HTMLElement>();
const closeButton = ref<HTMLButtonElement>();
let opener: HTMLElement | null = null;

const active = computed(() => (current.value === null ? undefined : props.items[current.value]));
const position = computed(() =>
  interpolate(messages.value.gallery.position, { index: (current.value ?? 0) + 1, total: props.items.length })
);

function altFor(item: OnScreenshot, index: number): string {
  return item.caption || interpolate(messages.value.gallery.item, { index: index + 1 });
}

async function open(index: number, event: MouseEvent) {
  opener = event.currentTarget as HTMLElement;
  if (current.value === null) lockScroll();
  current.value = index;
  await nextTick();
  moveFocus(closeButton.value);
}

function close() {
  if (current.value === null) return;
  current.value = null;
  unlockScroll();
  // Restore focus to the thumbnail that opened the lightbox.
  moveFocus(opener);
  opener = null;
}

function step(delta: number) {
  if (current.value === null) return;
  const total = props.items.length;
  current.value = (current.value + delta + total) % total;
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    close();
  } else if (event.key === 'ArrowLeft') {
    event.preventDefault();
    step(-1);
  } else if (event.key === 'ArrowRight') {
    event.preventDefault();
    step(1);
  } else if (event.key === 'Tab' && dialog.value) {
    // Trap focus inside the lightbox.
    trapTab(event, dialog.value);
  }
}

onBeforeUnmount(() => {
  if (current.value !== null) unlockScroll();
});

const navButton =
  'm-0 box-border inline-flex h-40px w-40px items-center justify-center rounded-full border-none bg-button-secondary-bg p-0 text-button-secondary-text outline-none transition-colors duration-fast ease-standard hover:bg-button-secondary-hover focus-visible:shadow-focus-ring';
</script>

<template>
  <section v-if="items.length" :aria-label="label ?? messages.gallery.label" class="min-w-0 font-sans">
    <ul class="-m-4px flex list-none gap-12px overflow-x-auto p-4px">
      <li v-for="(item, index) in items" :key="`${index}-${item.src}`" class="shrink-0">
        <button
          type="button"
          class="relative m-0 box-border block h-200px w-318px cursor-zoom-in overflow-hidden rounded-panel border border-solid border-line-default bg-surface-card-alt p-0 outline-none focus-visible:shadow-focus-ring"
          aria-haspopup="dialog"
          @click="open(index, $event)"
        >
          <!-- The first shot is often the largest image in view (X3): load it first, and every shot reserves its box. -->
          <img
            :src="item.thumb ?? item.src"
            :srcset="item.thumbSrcset"
            :sizes="item.thumbSrcset ? '318px' : undefined"
            :alt="altFor(item, index)"
            :loading="index === 0 ? 'eager' : 'lazy'"
            :fetchpriority="index === 0 ? 'high' : undefined"
            decoding="async"
            width="318"
            height="200"
            class="block h-full w-full object-cover"
          />
          <!-- The caption chip and the lightbox stay dark in both themes, so they render in the dark theme. -->
          <span
            v-if="item.caption"
            data-theme="dark"
            class="absolute bottom-9px left-10px rounded-tiny bg-component-caption-bg px-7px py-2px text-10.5px text-ink-primary"
            aria-hidden="true"
          >
            {{ item.caption }}
          </span>
        </button>
      </li>
    </ul>

    <Teleport v-if="active && current !== null" to="body">
      <div
        ref="dialog"
        data-theme="dark"
        class="fixed inset-0 z-modal box-border flex items-center justify-center bg-component-lightbox-scrim font-sans"
        role="dialog"
        aria-modal="true"
        :aria-label="messages.gallery.dialog"
        @click.self="close"
        @keydown="onKeydown"
      >
        <!-- Only images receive clicks; all other areas fall through to backdrop dismissal. -->
        <figure
          class="pointer-events-none m-0 box-border flex max-h-full max-w-full flex-col items-center gap-12px px-72px py-56px"
        >
          <img
            :src="active.src"
            :alt="altFor(active, current)"
            class="pointer-events-auto block max-h-[calc(100vh-160px)] max-w-full rounded-big object-contain"
          />
          <figcaption class="flex items-center gap-10px text-13px text-ink-secondary">
            <span v-if="active.caption">{{ active.caption }}</span>
            <span class="text-ink-tertiary">{{ position }}</span>
          </figcaption>
        </figure>
        <button
          ref="closeButton"
          type="button"
          :class="navButton"
          class="absolute right-20px top-20px"
          :aria-label="messages.gallery.close"
          @click="close"
        >
          <OnIcon name="x" :size="18" />
        </button>
        <template v-if="items.length > 1">
          <button
            type="button"
            :class="navButton"
            class="absolute left-20px top-1/2 -translate-y-1/2"
            :aria-label="messages.gallery.previous"
            @click="step(-1)"
          >
            <OnIcon name="chevron-left" :size="18" />
          </button>
          <button
            type="button"
            :class="navButton"
            class="absolute right-20px top-1/2 -translate-y-1/2"
            :aria-label="messages.gallery.next"
            @click="step(1)"
          >
            <OnIcon name="chevron-right" :size="18" />
          </button>
        </template>
      </div>
    </Teleport>
  </section>
</template>
