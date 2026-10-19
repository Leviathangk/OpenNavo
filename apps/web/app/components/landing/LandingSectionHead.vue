<script setup lang="ts">
import { OnHighlightText } from '@opennavo/ui';

// Section heading (mockup .sec-head): salmon eyebrow, large heading, muted body with **highlighted** text.
withDefaults(defineProps<{ kicker: string; title: string; body?: string; align?: 'start' | 'center' }>(), {
  align: 'start'
});
</script>

<template>
  <div
    class="flex flex-col gap-14px"
    :class="align === 'center' ? 'mx-auto max-w-760px items-center text-center' : 'items-start'"
  >
    <span class="text-13px font-600 tracking-[0.08em] text-brand-salmon">{{ kicker }}</span>
    <h2 class="landing-section-title m-0 text-title1 text-ink-primary md:text-section">{{ title }}</h2>
    <OnHighlightText
      v-if="body"
      as="p"
      :text="body"
      class="m-0 max-w-620px text-15.5px leading-[1.7] text-ink-secondary"
    />
  </div>
</template>

<style>
/* Even line lengths keep long headings (and CJK ones in particular) from leaving a lone final character. */
.landing-section-title {
  text-wrap: balance;
}

/* Chinese headings break at punctuation and spaces rather than inside a phrase ("一处" stays together); Japanese
   ones between phrases where supported. Both still break anywhere if a phrase cannot fit. */
:lang(zh) .landing-section-title {
  word-break: keep-all;
  overflow-wrap: anywhere;
}

:lang(ja) .landing-section-title {
  word-break: auto-phrase;
  overflow-wrap: anywhere;
}
</style>
