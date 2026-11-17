<script setup lang="ts">
import { OnIcon, OnLogo } from '@opennavo/ui';

// Footer sits below main content/rail in the shell column, with matching horizontal padding. It
// repeats the landing footer's link groups so every page links the core entry points (WP7), All Apps
// and the A–Z directory included (T1). The sidebar already labels these entries, so the groups flatten
// into one wrapped row: brand and links share the first row, language, copyright and analytics the second.
const { t } = useI18n();
const localePath = useLocalePath();
const year = useCopyrightYear();
const columns = useFooterLinks();
const consent = useAnalyticsConsent();
const links = computed(() => columns.value.flatMap(column => column.links));
</script>

<template>
  <footer class="flex flex-col gap-12px border-t border-t-solid border-line-subtle px-16px py-16px md:px-28px">
    <div class="flex flex-col gap-10px xl:flex-row xl:items-center xl:justify-between xl:gap-32px">
      <div class="flex min-w-0 items-start gap-12px xl:items-center">
        <NuxtLink
          :to="localePath('/')"
          class="inline-flex shrink-0 rounded-tiny no-underline outline-none focus-visible:shadow-focus-ring"
        >
          <OnLogo wordmark />
        </NuxtLink>
        <p class="m-0 line-clamp-2 max-w-520px text-12px leading-[1.55] text-ink-tertiary">
          {{ t('footer.tagline') }}
        </p>
      </div>
      <nav class="flex flex-wrap gap-x-16px gap-y-6px xl:justify-end" :aria-label="t('landing.footerNav')">
        <NuxtLink
          v-for="link in links"
          :key="link.key"
          :to="link.href"
          :target="link.external ? '_blank' : undefined"
          class="inline-flex items-center gap-2px rounded-tiny text-12px text-ink-secondary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
        >
          {{ link.label }}
          <OnIcon v-if="link.external" name="arrow-up-right" :size="11" />
        </NuxtLink>
      </nav>
    </div>
    <div class="flex flex-wrap items-center gap-x-16px gap-y-6px text-11.5px text-ink-tertiary">
      <!-- C4: real links to this page in the other languages it is published in, for crawlers and visitors. -->
      <SiteLanguageNav />
      <div class="flex flex-wrap items-center gap-x-16px gap-y-6px xl:ml-auto">
        <p class="m-0">{{ t('footer.copyright', { year }) }}</p>
        <button
          v-if="consent.enabled.value"
          type="button"
          class="m-0 rounded-tiny border-none bg-transparent p-0 text-11.5px text-ink-tertiary outline-none transition-colors duration-fast ease-standard hover:text-ink-secondary focus-visible:shadow-focus-ring"
          @click="consent.openSettings()"
        >
          {{ t('analytics.settings') }}
        </button>
      </div>
    </div>
  </footer>
</template>
