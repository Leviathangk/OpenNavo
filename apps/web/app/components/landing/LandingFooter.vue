<script setup lang="ts">
import { OnIcon, OnLogo } from '@opennavo/ui';

// Landing footer (08 §10.14): brand/copy, locale selector moved here on mobile, two link groups.
const { t } = useI18n();
const localePath = useLocalePath();
const year = new Date().getFullYear();
const columns = useFooterLinks();
const consent = useAnalyticsConsent();
</script>

<template>
  <footer class="border-t border-t-solid border-line-subtle">
    <div
      class="mx-auto box-border flex max-w-1240px flex-col gap-40px px-24px pb-32px pt-48px md:flex-row md:justify-between"
    >
      <div class="flex max-w-360px flex-col items-start gap-14px">
        <NuxtLink
          :to="localePath('/')"
          class="inline-flex rounded-tiny no-underline outline-none focus-visible:shadow-focus-ring"
        >
          <OnLogo wordmark />
        </NuxtLink>
        <p class="m-0 text-13px leading-[1.65] text-ink-secondary">{{ t('footer.tagline') }}</p>
        <LanguageSwitch align="start" />
      </div>
      <nav class="grid grid-cols-2 gap-x-64px gap-y-24px" :aria-label="t('landing.footerNav')">
        <div v-for="column in columns" :key="column.key" class="flex flex-col gap-12px">
          <h2 class="m-0 text-label text-ink-tertiary">{{ column.title }}</h2>
          <ul class="m-0 flex list-none flex-col gap-10px p-0">
            <li v-for="link in column.links" :key="link.key">
              <NuxtLink
                :to="link.href"
                :target="link.external ? '_blank' : undefined"
                class="inline-flex items-center gap-2px rounded-tiny text-13.5px text-ink-secondary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
              >
                {{ link.label }}
                <OnIcon v-if="link.external" name="arrow-up-right" :size="12" />
              </NuxtLink>
            </li>
          </ul>
        </div>
      </nav>
    </div>
    <!-- C4: the same crawlable language links as the site footer. -->
    <div class="mx-auto box-border max-w-1240px px-24px pb-16px">
      <SiteLanguageNav />
    </div>
    <div class="mx-auto box-border flex max-w-1240px flex-wrap items-center gap-x-16px gap-y-6px px-24px pb-32px">
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
  </footer>
</template>
