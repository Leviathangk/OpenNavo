<script setup lang="ts">
import ClientFeaturePage from '~/components/client/ClientFeaturePage.vue';
import { desktopAppId } from '~/utils/structuredData';

// Readings come from this Mac and stay in memory. The demo is illustrative; the page never suggests
// that a browser can read the visitor's sensors.
const { t, locale } = useI18n();
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');

const appPoints = computed(() => [
  t('landing.toolkit.monitor.title'),
  t('landing.toolkit.monitor.body'),
  t('landing.more.privacy.body')
]);

usePageSeo({
  title: () => `${t('landing.window.monitor')} · OpenNavo`,
  description: () => t('landing.toolkit.monitor.body')
});
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: `${t('landing.window.monitor')} · OpenNavo`,
  description: t('landing.toolkit.monitor.body'),
  ...useOgFrame()
});
usePageSchema({ about: desktopAppId(siteUrl) });
</script>

<template>
  <ClientFeaturePage
    feature="monitor"
    route="monitor"
    :title="t('landing.window.monitor')"
    :lede="t('landing.toolkit.monitor.body')"
    :app-points="appPoints"
  >
    <template #preview><LandingMonitorDemo /></template>
  </ClientFeaturePage>
</template>
