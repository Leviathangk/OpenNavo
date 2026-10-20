<script setup lang="ts">
import type { PackagePage } from '~/utils/packageFacts';

// Related articles on an app page (WP17): published articles that cover this app, newest first; casks and
// link apps alike (T3).
const props = defineProps<{ pkg: PackagePage }>();

const { t } = useI18n();
const items = computed(() => props.pkg.articles ?? []);
</script>

<template>
  <section
    v-if="items.length"
    class="flex flex-col gap-10px"
    aria-labelledby="package-articles-title"
    data-testid="package-articles"
  >
    <h2 id="package-articles-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.articles.title') }}
    </h2>
    <div class="grid gap-10px">
      <ArticleCard v-for="card in items" :key="card.slug" :card="card" :heading-level="3" />
    </div>
  </section>
</template>
