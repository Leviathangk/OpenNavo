<script setup lang="ts">
import ClientFeaturePage from '~/components/client/ClientFeaturePage.vue';
import { desktopAppId } from '~/utils/structuredData';

// Clean Up scans this Mac, so the web page previews the review flow and the risk tiers. Every number
// in the demo is sample data.
const { t, locale } = useI18n();
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');

const appPoints = computed(() => [
  t('landing.toolkit.cleanup.title'),
  t('client.pages.cleanup.app.review'),
  t('client.pages.cleanup.app.undo')
]);

usePageSeo({
  title: () => `${t('landing.window.cleanup')} · OpenNavo`,
  description: () => t('landing.toolkit.cleanup.body')
});
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: `${t('landing.window.cleanup')} · OpenNavo`,
  description: t('landing.toolkit.cleanup.body'),
  ...useOgFrame()
});
usePageSchema({ about: desktopAppId(siteUrl) });
</script>

<template>
  <ClientFeaturePage
    feature="cleanup"
    route="cleanup"
    :title="t('landing.window.cleanup')"
    :lede="t('landing.toolkit.cleanup.body')"
    :app-points="appPoints"
  >
    <template #preview><LandingCleanupDemo /></template>
  </ClientFeaturePage>
</template>
