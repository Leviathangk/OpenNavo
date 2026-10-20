<script setup lang="ts">
import { OnIcon, OnLogo } from '@opennavo/ui';

// Footer sits below main content/rail in the shell column, with matching horizontal padding. It
// repeats the landing footer's link groups so every page links the core entry points (WP7), All Apps
// and the A–Z directory included (T1). The sidebar already labels these entries, so each group is one
// unlabeled row: brand and tagline on the left, link rows on the right at xl+, stacked below that. The
// language links (C4) sit above the copyright line.
const { t } = useI18n();
const localePath = useLocalePath();
const year = new Date().getFullYear();
const columns = useFooterLinks();
const consent = useAnalyticsConsent();
</script>

<template>
  <footer class="flex flex-col gap-24px border-t border-t-solid border-line-subtle px-16px py-28px md:px-28px">
    <div class="flex flex-col gap-20px xl:flex-row xl:items-start xl:justify-between xl:gap-48px">
      <div class="flex max-w-480px flex-col items-start gap-10px">
        <NuxtLink
          :to="localePath('/')"
          class="inline-flex rounded-tiny no-underline outline-none focus-visible:shadow-focus-ring"
        >
          <OnLogo wordmark />
        </NuxtLink>
        <p class="m-0 text-13px leading-[1.6] text-ink-tertiary">{{ t('footer.tagline') }}</p>
      </div>
      <nav class="flex flex-col gap-8px xl:items-end" :aria-label="t('landing.footerNav')">
        <ul
          v-for="column in columns"
          :key="column.key"
          :aria-label="column.title"
          class="m-0 flex list-none flex-wrap gap-x-20px gap-y-8px p-0 xl:justify-end"
        >
          <li v-for="link in column.links" :key="link.key">
            <NuxtLink
              :to="link.href"
              :target="link.external ? '_blank' : undefined"
              class="inline-flex items-center gap-2px rounded-tiny text-13px text-ink-secondary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
            >
              {{ link.label }}
              <OnIcon v-if="link.external" name="arrow-up-right" :size="12" />
            </NuxtLink>
          </li>
        </ul>
      </nav>
    </div>
    <!-- C4: real links to this page in the other languages it is published in, for crawlers and visitors. -->
    <SiteLanguageNav />
    <div class="flex flex-wrap items-center gap-x-16px gap-y-6px text-12px text-ink-tertiary xl:justify-between">
      <p class="m-0">{{ t('footer.copyright', { year }) }}</p>
      <button
        v-if="consent.enabled.value"
        type="button"
        class="m-0 rounded-tiny border-none bg-transparent p-0 text-12px text-ink-tertiary outline-none transition-colors duration-fast ease-standard hover:text-ink-secondary focus-visible:shadow-focus-ring"
        @click="consent.openSettings()"
      >
        {{ t('analytics.settings') }}
      </button>
    </div>
  </footer>
</template>
