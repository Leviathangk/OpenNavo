<script setup lang="ts">
import ClientFeaturePage from '~/components/client/ClientFeaturePage.vue';
import { desktopAppId } from '~/utils/structuredData';

// Web Apps become separate Mac app bundles, which a browser cannot create. The page explains the
// catalog and hands the visitor to the client; the preview uses real brand icons with sample state.
const { t, locale } = useI18n();
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');

const appPoints = computed(() => [
  t('client.pages.webapps.app.window'),
  t('client.pages.webapps.app.catalog'),
  t('client.pages.webapps.app.data')
]);

usePageSeo({
  title: () => t('client.pages.webapps.metaTitle'),
  description: () => t('client.pages.webapps.lede')
});
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: t('client.pages.webapps.metaTitle'),
  description: t('client.pages.webapps.lede'),
  ...useOgFrame()
});
usePageSchema({ about: desktopAppId(siteUrl) });
</script>

<template>
  <ClientFeaturePage
    feature="webapps"
    route="webapps"
    :title="t('landing.window.webapps')"
    :lede="t('client.pages.webapps.lede')"
    :app-points="appPoints"
  >
    <template #preview><LandingWebAppsDemo /></template>
  </ClientFeaturePage>
</template>
