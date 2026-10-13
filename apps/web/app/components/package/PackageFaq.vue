<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { parseInline } from '@opennavo/ui';
import { faqItems } from '~/utils/appAnswer';

// WP9 questions the page does not answer elsewhere, each from a recorded fact. Answers are in the
// server-rendered HTML even while collapsed; no FAQ rich result is expected for a catalog page.
const props = defineProps<{ pkg: PackageDetail }>();

const { t, locale } = useI18n();
const items = computed(() =>
  faqItems(props.pkg, t, toAppLocale(locale.value)).map(item => ({ ...item, parts: parseInline(item.answer) }))
);
</script>

<template>
  <section
    v-if="items.length"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-12px"
    aria-labelledby="package-faq-title"
    data-testid="package-faq"
  >
    <h2 id="package-faq-title" class="m-0 py-4px text-13.5px font-600 text-ink-primary">
      {{ t('package.faq.title') }}
    </h2>
    <details
      v-for="item in items"
      :key="item.key"
      class="border-t border-t-solid border-line-subtle py-8px first-of-type:border-t-0"
    >
      <summary class="cursor-pointer text-13px font-500 text-ink-primary outline-none focus-visible:shadow-focus-ring">
        {{ item.question }}
      </summary>
      <p class="m-0 mt-6px text-13px leading-[1.6] text-ink-secondary">
        <template v-for="(part, index) in item.parts" :key="index">
          <code
            v-if="part.type === 'code'"
            class="rounded-tiny bg-surface-inset px-4px font-mono text-12px text-component-mono-text"
            >{{ part.text }}</code
          >
          <template v-else>{{ part.text }}</template>
        </template>
      </p>
    </details>
  </section>
</template>
