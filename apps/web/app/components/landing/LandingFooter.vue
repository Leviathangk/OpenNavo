<script setup lang="ts">
import { OnIcon, OnLogo } from '@opennavo/ui';

// Landing footer (08 §10.14): brand/copy, theme and locale selectors on mobile only (the top bar carries them from sm
// up), two link groups that flow down two short columns from sm up. From lg up the brand sits beside the links and the
// language links share one row with the copyright, so the footer stays about as tall as the brand block.
const { t } = useI18n();
const localePath = useLocalePath();
const year = new Date().getFullYear();
const columns = useFooterLinks();
const consent = useAnalyticsConsent();
</script>

<template>
  <footer class="border-t border-t-solid border-line-subtle">
    <div
      class="mx-auto box-border flex max-w-1240px flex-col gap-32px px-24px pb-32px pt-40px lg:flex-row lg:justify-between lg:gap-48px"
    >
      <div class="flex max-w-360px flex-col items-start gap-12px">
        <NuxtLink
          :to="localePath('/')"
          class="inline-flex rounded-tiny no-underline outline-none focus-visible:shadow-focus-ring"
        >
          <OnLogo wordmark />
        </NuxtLink>
        <p class="m-0 text-13px leading-[1.65] text-ink-secondary">{{ t('footer.tagline') }}</p>
        <div class="flex items-center gap-2px sm:hidden">
          <LanguageSwitch align="start" />
          <ThemeSwitch align="start" />
        </div>
      </div>
      <nav
        class="grid grid-cols-2 gap-x-48px gap-y-24px sm:grid-cols-[repeat(2,max-content)] lg:gap-x-80px"
        :aria-label="t('landing.footerNav')"
      >
        <div v-for="column in columns" :key="column.key" class="flex flex-col gap-12px">
          <h2 class="m-0 text-label text-ink-tertiary">{{ column.title }}</h2>
          <ul
            class="m-0 grid list-none gap-x-32px gap-y-10px p-0 sm:grid-flow-col"
            :style="{ gridTemplateRows: `repeat(${Math.ceil(column.links.length / 2)}, auto)` }"
          >
            <li v-for="link in column.links" :key="link.key">
              <NuxtLink
                :to="link.href"
                :target="link.external ? '_blank' : undefined"
                class="inline-flex items-center gap-2px rounded-tiny sm:whitespace-nowrap text-13.5px text-ink-secondary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
              >
                {{ link.label }}
                <OnIcon v-if="link.external" name="arrow-up-right" :size="12" />
              </NuxtLink>
            </li>
          </ul>
        </div>
      </nav>
    </div>
    <div
      class="mx-auto box-border flex max-w-1240px flex-col gap-12px px-24px pb-32px lg:flex-row lg:items-center lg:justify-between lg:gap-24px"
    >
      <!-- C4: the same crawlable language links as the site footer. -->
      <SiteLanguageNav />
      <div class="flex shrink-0 flex-wrap items-center gap-x-16px gap-y-6px">
        <p class="m-0 text-12px text-ink-tertiary">{{ t('footer.copyright', { year }) }}</p>
        <button
          v-if="consent.enabled.value"
          type="button"
          class="m-0 rounded-tiny border-none bg-transparent p-0 text-12px text-ink-tertiary underline outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
          @click="consent.openSettings()"
        >
          {{ t('analytics.settings') }}
        </button>
      </div>
    </div>
  </footer>
</template>
