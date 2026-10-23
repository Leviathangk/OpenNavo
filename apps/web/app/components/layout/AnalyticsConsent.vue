<script setup lang="ts">
// GA4 consent banner (WP8). Allow and Don't allow carry equal weight; the footer "Analytics settings"
// link reopens it. It waits while the language suggestion is showing, so only one prompt appears.
const { t } = useI18n();
const localePath = useLocalePath();
const consent = useAnalyticsConsent();
const banner = consent.banner;
const languagePrompt = useState('onv-language-suggestion', () => false);
const buttonClass =
  'rounded-default border border-solid border-line-default bg-transparent px-14px py-6px text-14px text-ink-primary transition-colors duration-fast ease-standard hover:bg-surface-hover focus-visible:shadow-focus-ring';
</script>

<template>
  <section
    v-if="banner && !languagePrompt"
    role="region"
    :aria-label="t('analytics.label')"
    data-testid="analytics-consent"
    class="fixed bottom-16px left-16px right-16px z-50 mx-auto flex max-w-560px flex-wrap items-center gap-12px rounded-default border border-line-default bg-surface-popover p-16px shadow-popover"
  >
    <p class="m-0 min-w-0 flex-1 basis-240px text-14px leading-[1.6] text-ink-primary">
      {{ t('analytics.message') }}
      <NuxtLink
        :to="`${localePath('/about')}#privacy`"
        class="rounded-tiny text-ink-secondary underline outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
      >
        {{ t('analytics.privacy') }}
      </NuxtLink>
    </p>
    <div class="flex flex-wrap gap-8px">
      <button type="button" :class="buttonClass" @click="consent.accept()">{{ t('analytics.accept') }}</button>
      <button type="button" :class="buttonClass" @click="consent.decline()">{{ t('analytics.decline') }}</button>
    </div>
  </section>
</template>
