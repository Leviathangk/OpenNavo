<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';
import { PACKAGE_GUIDE_LINKS } from '~/utils/packageLinks';
import { packagePath } from '~/utils/packageView';

// Server-rendered package links (#214): a compact, accessible path into representative app pages,
// independent of the decorative icon marquee above it. Existing copy avoids new translation keys.
const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');
</script>

<template>
  <section class="mx-auto box-border w-full max-w-1240px px-24px" data-reveal>
    <h2 class="m-0 text-headline text-ink-primary">{{ t('home.popularApps') }}</h2>
    <ul class="m-0 mt-14px flex list-none flex-wrap gap-8px p-0">
      <li v-for="app in PACKAGE_GUIDE_LINKS" :key="app.token">
        <NuxtLink
          :to="localePath(packagePath(app.token))"
          class="box-border inline-flex items-center gap-6px rounded-full border border-solid border-line-subtle bg-surface-card px-14px py-7px text-13px font-500 text-ink-secondary no-underline transition-colors duration-fast ease-standard hover:border-line-strong hover:text-ink-primary focus-visible:shadow-focus-ring"
        >
          {{ app.name }}
          <OnIcon name="arrow-right" :size="13" class="text-ink-tertiary" />
        </NuxtLink>
      </li>
    </ul>
  </section>
</template>
