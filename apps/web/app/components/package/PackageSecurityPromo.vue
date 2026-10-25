<script setup lang="ts">
import { OnIcon } from '@opennavo/ui';

// Security-category apps (antivirus, firewalls, privacy tools): people who look these up are the ones /security
// answers, so the page points there, and the security page gains links from topically related pages. The two
// features the promo names link to their own topic pages (F8) too, under those pages' titles.
const { t } = useI18n();
const localePath = useLocalePath();

const links = computed(() => [
  ...(['clamav', 'firewall'] as const).map(key => ({
    key,
    label: t(`securityTopics.links.${key}.title`),
    path: `/security/${key}`
  })),
  { key: 'security', label: t('package.securityPromo.link'), path: '/security' }
]);
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
    <ul class="m-0 mt-8px flex list-none flex-col items-start gap-4px p-0">
      <li v-for="link in links" :key="link.key">
        <NuxtLink
          :to="localePath(link.path)"
          class="inline-flex items-center gap-4px rounded-tiny text-12.5px font-600 text-ink-primary no-underline outline-none transition-colors duration-fast ease-standard hover:text-ink-secondary focus-visible:shadow-focus-ring"
        >
          {{ link.label }}
          <OnIcon name="arrow-right" :size="13" />
        </NuxtLink>
      </li>
    </ul>
  </section>
</template>
