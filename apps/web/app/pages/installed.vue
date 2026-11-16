<script setup lang="ts">
import ClientFeaturePage from '~/components/client/ClientFeaturePage.vue';
import { SAMPLE_APPS } from '~/utils/landing';
import { desktopAppId } from '~/utils/structuredData';

// Installed apps live on the Mac, so the web page explains the app's list and uninstall review and
// hands the visitor to the client. The demo uses sample app names only.
const { t, locale } = useI18n();
const app = SAMPLE_APPS[0];
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');

const appPoints = computed(() => [
  t('landing.toolkit.uninstall.title'),
  t('landing.toolkit.uninstall.body'),
  t('landing.install.points.history')
]);

usePageSeo({
  title: () => t('client.pages.installed.metaTitle'),
  description: () => t('client.pages.installed.lede')
});
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: t('client.pages.installed.metaTitle'),
  description: t('client.pages.installed.lede'),
  ...useOgFrame()
});
usePageSchema({ about: desktopAppId(siteUrl) });
</script>

<template>
  <ClientFeaturePage
    feature="installed"
    route="installed"
    :title="t('landing.window.installed')"
    :lede="t('client.pages.installed.lede')"
    :app-points="appPoints"
  >
    <template #preview>
      <LandingUninstallDemo v-if="app" :app="app" />
    </template>
  </ClientFeaturePage>
</template>
