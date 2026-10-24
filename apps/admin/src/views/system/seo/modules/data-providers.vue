<script setup lang="ts">
import { ref, watch } from 'vue';
import type { SeoProviderKey } from '@/service/api/system';
import { $t } from '@/locales';
import ProviderSection from './provider-section.vue';

defineOptions({ name: 'SeoDataProviders' });

// Credentials for the server's own ingestion: Search Console (gsc), Bing Webmaster (bing) and the
// edge-vs-origin audit (audit). Each source loads and saves on its own endpoint; values are write-only.
const props = defineProps<{ siteUrl: string; revision: number }>();

const SOURCES: SeoProviderKey[] = ['gsc', 'bing', 'audit'];
const reload = ref(props.revision);

const text = (key: string) => $t(`page.system.seo.providers.${key}` as App.I18n.I18nKey);

watch(
  () => props.revision,
  value => (reload.value = value)
);
</script>

<template>
  <NCard :title="text('title')" :bordered="false" class="card-wrapper" data-testid="seo-providers">
    <template #header-extra>
      <NButton @click="reload++">{{ $t('common.refresh') }}</NButton>
    </template>
    <div class="max-w-860px">
      <NP class="mt-0">{{ text('hint') }}</NP>
      <NText depth="3" class="mb-12px block">{{ text('replaceHint') }}</NText>
      <ProviderSection
        v-for="source in SOURCES"
        :key="source"
        :source="source"
        :site-url="siteUrl"
        :revision="reload"
      />
    </div>
  </NCard>
</template>
