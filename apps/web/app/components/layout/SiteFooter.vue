<script setup lang="ts">
// Footer sits below main content/rail in the shell column, with matching horizontal padding. It
// repeats the landing footer's link groups so every page links the core entry points (WP7).
const { t } = useI18n();
const year = new Date().getFullYear();
const columns = useFooterLinks();
const consent = useAnalyticsConsent();
</script>

<template>
  <footer class="flex flex-col gap-20px border-t border-t-solid border-line-subtle px-16px py-24px md:px-28px">
    <nav class="flex flex-wrap gap-x-48px gap-y-16px" :aria-label="t('landing.footerNav')">
      <div v-for="column in columns" :key="column.key" class="flex flex-col gap-8px">
        <h2 class="m-0 text-label text-ink-tertiary">{{ column.title }}</h2>
        <ul class="m-0 flex list-none flex-wrap gap-x-18px gap-y-6px p-0">
          <li v-for="link in column.links" :key="link.key">
            <NuxtLink
              :to="link.href"
              class="rounded-tiny text-13px text-ink-secondary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-primary focus-visible:shadow-focus-ring"
            >
              {{ link.label }}
            </NuxtLink>
          </li>
        </ul>
      </div>
    </nav>
    <div class="flex flex-col gap-6px">
      <p class="m-0 text-13px text-ink-secondary">{{ t('footer.tagline') }}</p>
      <p class="m-0 text-12px text-ink-tertiary">{{ t('footer.copyright', { year }) }}</p>
      <button
        v-if="consent.enabled.value"
        type="button"
        class="m-0 self-start rounded-tiny border-none bg-transparent p-0 text-12px text-ink-tertiary underline outline-none hover:text-ink-secondary focus-visible:shadow-focus-ring"
        @click="consent.openSettings()"
      >
        {{ t('analytics.settings') }}
      </button>
    </div>
  </footer>
</template>
