<script setup lang="ts">
import { OnAboutContent } from '@opennavo/ui';
import { unwrap } from '@opennavo/api';

const { t, locale } = useI18n();
const api = useApi();
const { data: config } = await useAsyncData(
  () => `about:${locale.value}`,
  () => unwrap(api.GET('/config/client', { params: { query: { platform: 'web' } } }))
);

const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
usePageSchema({ type: 'AboutPage', mainEntity: `${siteUrl}/#identity` });

usePageSeo({ title: () => t('about.metaTitle'), description: () => t('about.description') });
// Share image (WP13): the site's default card, as on the home page.
defineOgImage('OgDefaultSatori', {
  locale: toAppLocale(locale.value),
  variant: 'home',
  title: t('og.homeTitle'),
  description: t('og.homeDescription'),
  ...useOgFrame()
});
</script>

<template>
  <div class="mx-auto box-border flex max-w-760px flex-col gap-28px pt-14px">
    <PageHeader :title="t('about.title')" :description="t('about.description')" />
    <OnAboutContent v-if="config?.about" :content="config.about" :draft-label="t('about.draft')" />
  </div>
</template>
