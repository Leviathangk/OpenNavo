<script setup lang="ts">
import { OnButton, OnEmpty } from '@opennavo/ui';

// A withdrawn article (WP14/WP17): the page answers 410 and points to the other articles.
const { t } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

usePageSeo({ title: () => t('articles.withdrawnMetaTitle'), noindex: true });
</script>

<template>
  <section class="flex flex-col items-center gap-16px py-48px" data-testid="article-withdrawn">
    <OnEmpty icon="lucide:newspaper" :title="t('articles.withdrawnTitle')" :description="t('articles.withdrawnBody')" />
    <OnButton variant="primary" shape="round" :href="localePath('/articles')" :link-as="NuxtLink">
      {{ t('articles.backToList') }}
    </OnButton>
  </section>
</template>
