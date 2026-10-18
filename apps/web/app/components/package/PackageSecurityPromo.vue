<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// Security-category apps (antivirus, firewalls, privacy tools): people who look these up are the ones /security
// answers, so the page points there, and the security page gains links from topically related pages.
const { t } = useI18n();
const localePath = useLocalePath();
</script>

<template>
  <section
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-14px"
    aria-labelledby="package-security-promo"
  >
    <h2 id="package-security-promo" class="m-0 flex items-center gap-8px text-13.5px font-600 text-ink-primary">
      <span
        class="grid h-24px w-24px shrink-0 place-items-center rounded-default bg-status-success-subtle text-status-success"
        aria-hidden="true"
      >
        <OnIcon name="shield-check" :size="14" />
      </span>
      {{ t('package.securityPromo.title') }}
    </h2>
    <p class="m-0 mt-8px text-12.5px leading-[1.6] text-ink-secondary">{{ t('package.securityPromo.body') }}</p>
    <NuxtLink
      :to="localePath('/security')"
      class="mt-8px inline-flex items-center gap-4px rounded-tiny text-12.5px font-600 text-ink-primary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-secondary focus-visible:shadow-focus-ring"
    >
      {{ t('package.securityPromo.link') }}
      <OnIcon name="arrow-right" :size="13" />
    </NuxtLink>
  </section>
</template>
