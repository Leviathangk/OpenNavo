<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { OnCodeText, parseInline } from '@opennavo/ui';
import { faqItems } from '~/utils/packageFaq';
import type { FaqTranslate } from '~/utils/packageFaq';

// Questions people search for about a Mac app, answered from recorded facts (utils/packageFaq).
// Each question is a heading and every answer is in the server-rendered HTML even while collapsed.
// Google no longer shows FAQ rich results, so there is no FAQPage markup.
const props = defineProps<{ pkg: PackageDetail }>();

const { t, locale } = useI18n();
const translate: FaqTranslate = (key, named = {}, plural) =>
  plural === undefined ? t(key, named) : t(key, named, { plural });
const items = computed(() =>
  faqItems(props.pkg, translate, toAppLocale(locale.value)).map(item => ({ ...item, parts: parseInline(item.answer) }))
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
        <h3 class="m-0 inline text-13px font-500">{{ item.question }}</h3>
      </summary>
      <p class="m-0 mt-6px text-13px leading-[1.6] text-ink-secondary">
        <template v-for="(part, index) in item.parts" :key="index">
          <code
            v-if="part.type === 'code'"
            class="rounded-tiny bg-surface-inset px-4px font-mono text-12px text-component-mono-text"
            ><OnCodeText :text="part.text"
          /></code>
          <template v-else>{{ part.text }}</template>
        </template>
      </p>
    </details>
  </section>
</template>
