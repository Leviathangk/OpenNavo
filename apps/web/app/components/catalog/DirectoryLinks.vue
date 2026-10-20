<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// Directory entry points for the landing pages (C5): the home page and /security link the category tree,
// the collections, the full app list and the A–Z directory as real links. A page can put one more link
// first, such as /security's own category.
const props = withDefaults(
  defineProps<{ lead?: { key: string; label: string; href: string; icon: string } | null }>(),
  { lead: null }
);

const { t } = useI18n();
const localePath = useLocalePath();

const links = computed(() => [
  ...(props.lead ? [props.lead] : []),
  { key: 'categories', label: t('nav.categories'), href: localePath('/categories'), icon: 'layout-grid' },
  { key: 'collections', label: t('nav.collections'), href: localePath('/collections'), icon: 'library' },
  { key: 'apps', label: t('nav.apps'), href: localePath('/apps'), icon: 'layers' },
  { key: 'az', label: t('nav.az'), href: localePath('/browse'), icon: 'list-filter' }
]);
</script>

<template>
  <nav class="mx-auto box-border w-full max-w-1240px px-24px" :aria-label="t('browse.directory.title')">
    <div data-reveal>
      <LandingSectionHead
        align="center"
        :kicker="t('browse.directory.kicker')"
        :title="t('browse.directory.title')"
        :body="t('browse.directory.body')"
      />
    </div>
    <ul
      class="m-0 mt-40px grid list-none gap-12px p-0 sm:grid-cols-2"
      :class="links.length > 4 ? 'lg:grid-cols-5' : 'lg:grid-cols-4'"
    >
      <li v-for="link in links" :key="link.key" data-reveal>
        <NuxtLink
          :to="link.href"
          class="flex h-full items-center gap-12px rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px text-ink-primary no-underline outline-none transition-colors duration-fast ease-standard hover:border-line-strong focus-visible:shadow-focus-ring"
        >
          <span
            class="grid h-36px w-36px shrink-0 place-items-center rounded-default bg-accent-salmon-subtle text-brand-salmon"
            aria-hidden="true"
          >
            <OnIcon :name="link.icon" :size="18" />
          </span>
          <span class="min-w-0 flex-1 text-15px font-600">{{ link.label }}</span>
          <OnIcon name="chevron-right" :size="16" class="shrink-0 text-ink-tertiary" />
        </NuxtLink>
      </li>
    </ul>
  </nav>
</template>
