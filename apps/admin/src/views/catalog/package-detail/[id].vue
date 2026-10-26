<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { fetchPackageDetail } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { $t } from '@/locales';
import AssetTab from './modules/asset-tab.vue';
import AuditTab from './modules/audit-tab.vue';
import BasicTab from './modules/basic-tab.vue';
import CategoryTab from './modules/category-tab.vue';
import ChangelogTab from './modules/changelog-tab.vue';
import DetailHeader from './modules/detail-header.vue';
import FactsTab from './modules/facts-tab.vue';
import I18nTab from './modules/i18n-tab.vue';
import IndexTab from './modules/index-tab.vue';

defineOptions({ name: 'CatalogPackageDetail' });

// Package details (07 §7.3): summary header and tabs; reload details after tab saves.
const route = useRoute();
const { hasAuth } = useAuth();

const id = computed(() => Number(route.params.id));
const detail = ref<DataOf<'getAdminPackage'> | null>(null);
const loading = ref(false);
const TABS = ['basic', 'i18n', 'categories', 'assets', 'facts', 'changelog', 'index', 'audit'];
const tab = ref(TABS.includes(String(route.query.tab)) ? String(route.query.tab) : 'basic');

async function load() {
  if (!Number.isFinite(id.value)) return;
  loading.value = true;
  const { data, error } = await fetchPackageDetail(id.value);
  if (!error) detail.value = data;
  loading.value = false;
}

watch(id, load, { immediate: true });
</script>

<template>
  <NSpin :show="loading && !detail">
    <div v-if="detail" class="flex-col gap-16px">
      <DetailHeader :detail="detail" />
      <NCard :bordered="false" size="small" class="card-wrapper">
        <NTabs v-model:value="tab" type="line" animated>
          <NTabPane name="basic" :tab="$t('page.catalog.packageDetail.tabs.basic')">
            <BasicTab :detail="detail" @saved="load" />
          </NTabPane>
          <NTabPane name="i18n" :tab="$t('page.catalog.packageDetail.tabs.i18n')">
            <I18nTab :detail="detail" @saved="load" />
          </NTabPane>
          <NTabPane name="categories" :tab="$t('page.catalog.packageDetail.tabs.categories')">
            <CategoryTab :detail="detail" @saved="load" />
          </NTabPane>
          <NTabPane name="assets" :tab="$t('page.catalog.packageDetail.tabs.assets')">
            <AssetTab :detail="detail" @saved="load" />
          </NTabPane>
          <NTabPane name="facts" :tab="$t('page.catalog.packageDetail.tabs.facts')" display-directive="if">
            <FactsTab :key="detail.id" :package-id="detail.id" />
          </NTabPane>
          <NTabPane name="changelog" :tab="$t('page.catalog.packageDetail.tabs.changelog')" display-directive="if">
            <ChangelogTab :key="detail.id" :package-id="detail.id" />
          </NTabPane>
          <NTabPane
            v-if="hasAuth(['system:seo:edit', 'catalog:package:view'])"
            name="index"
            :tab="$t('page.catalog.packageDetail.tabs.index')"
            display-directive="if"
          >
            <IndexTab :key="detail.id" :package-id="detail.id" />
          </NTabPane>
          <NTabPane
            v-if="hasAuth('system:audit:view')"
            name="audit"
            :tab="$t('page.catalog.packageDetail.tabs.audit')"
            display-directive="if"
          >
            <AuditTab entity-type="package" :entity-id="String(detail.id)" />
          </NTabPane>
        </NTabs>
      </NCard>
    </div>
    <NCard v-else-if="!loading" :bordered="false" class="card-wrapper">
      <NEmpty />
    </NCard>
  </NSpin>
</template>
