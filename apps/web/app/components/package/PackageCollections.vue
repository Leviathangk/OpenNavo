<script setup lang="ts">
import type { PackagePage } from '~/utils/packageFacts';

// Collections with this app (T1): the public collections that list it, as real links, so every app links to
// the hubs it belongs to and each hub gains links from its members.
const props = defineProps<{ pkg: PackagePage }>();

const { t } = useI18n();
const localePath = useLocalePath();
const items = computed(() => props.pkg.collections ?? []);
</script>

<template>
  <section
    v-if="items.length"
    class="mt-4px"
    aria-labelledby="package-collections-title"
    data-testid="package-collections"
  >
    <h2 id="package-collections-title" class="m-0 text-headline text-ink-primary">
      {{ t('package.collections.title') }}
    </h2>
    <ul class="m-0 mt-8px list-none p-0">
      <li
        v-for="item in items"
        :key="item.slug"
        class="border-t border-t-solid border-line-subtle py-8px text-12.5px first:border-t-0"
      >
        <NuxtLink
          :to="localePath(`/collections/${item.slug}`)"
          class="font-600 text-ink-primary no-underline outline-none hover:underline focus-visible:underline"
          >{{ item.title }}</NuxtLink
        >
      </li>
    </ul>
  </section>
</template>
