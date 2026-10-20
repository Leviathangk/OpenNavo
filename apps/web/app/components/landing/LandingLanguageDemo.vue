<script setup lang="ts">
import { LOCALES, LOCALE_METADATA, formatCount, formatDate } from '@opennavo/shared';

// Six-language demo (12 §1): flip language names, showing the same date/catalog count formatted with Intl.
// Representative frame uses current locale; fixed UTC date keeps SSR/browser output identical.
const props = defineProps<{ count: number | null }>();

const SAMPLE_DATE = Date.UTC(2026, 9, 8);
const DURATIONS = LOCALES.map(() => 1600);

const { locale } = useI18n();
const root = ref<HTMLElement>();
const step = useDemoLoop(
  root,
  DURATIONS,
  Math.max(0, (LOCALES as readonly string[]).indexOf(toAppLocale(locale.value)))
);

const items = computed(() =>
  LOCALES.map((code, index) => ({
    code,
    name: LOCALE_METADATA[code].name,
    offset: index - step.value,
    // Use the real catalog app count; show only date if API is unavailable.
    sample: [
      formatDate(SAMPLE_DATE, { locale: code, timeZone: 'UTC', now: new Date(SAMPLE_DATE) }),
      ...(props.count ? [formatCount(props.count, { locale: code })] : [])
    ].join(' · ')
  }))
);
const current = computed(() => items.value[step.value]);
</script>

<template>
  <div ref="root" class="mx-auto flex max-w-300px flex-col items-center gap-14px" inert aria-hidden="true">
    <div class="relative h-44px w-full overflow-hidden">
      <span
        v-for="item in items"
        :key="item.code"
        :lang="item.code"
        class="landing-language absolute inset-x-0 top-0 truncate text-center text-30px font-600 leading-[44px] tracking-[-0.02em] text-ink-primary"
        :class="item.offset === 0 ? 'is-current' : item.offset < 0 ? 'is-before' : 'is-after'"
        >{{ item.name }}</span
      >
    </div>
    <span v-if="current" :lang="current.code" class="text-12.5px text-ink-tertiary tabular-nums">{{
      current.sample
    }}</span>
    <span class="flex gap-6px">
      <span
        v-for="(item, index) in items"
        :key="item.code"
        class="h-6px rounded-full transition-all duration-slow ease-standard"
        :class="index === step ? 'w-18px bg-brand-coral' : 'w-6px bg-surface-control'"
      ></span>
    </span>
  </div>
</template>

<style>
.landing-language {
  transition:
    transform 0.5s var(--on-motion-easing-emphasized),
    opacity 0.5s var(--on-motion-easing-emphasized);
}

.landing-language.is-before {
  opacity: 0;
  transform: translateY(-100%);
}

.landing-language.is-after {
  opacity: 0;
  transform: translateY(100%);
}
</style>
