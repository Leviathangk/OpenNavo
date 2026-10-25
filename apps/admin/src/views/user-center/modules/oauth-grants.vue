<script setup lang="ts">
import { ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { oauthMessages } from '@/locales/oauth';
import { oauthRequest } from '@/service/api/oauth';
import type { OAuthGrant, OAuthGrantPage } from '@/service/api/oauth';
import { permissionLabel } from '@/views/system/agent/modules/permissions';
import { formatDateTime } from '@/utils/opennavo';

const { t } = useI18n({ useScope: 'local', messages: oauthMessages });
const grants = ref<OAuthGrant[]>([]);
const loading = ref(false);
const nextCursor = ref<string | null>(null);
const revoking = ref<number | null>(null);
const failed = ref(false);
async function load(append = false) {
  if (loading.value) return;
  loading.value = true;
  failed.value = false;
  try {
    const query = append && nextCursor.value ? `?before=${encodeURIComponent(nextCursor.value)}` : '';
    const page = await oauthRequest<OAuthGrantPage>(`grants${query}`);
    grants.value = append ? [...grants.value, ...page.items] : page.items;
    nextCursor.value = page.nextCursor;
  } catch {
    failed.value = true;
  } finally {
    loading.value = false;
  }
}
async function revoke(id: number) {
  revoking.value = id;
  failed.value = false;
  try {
    await oauthRequest(`grants/${id}`, { method: 'DELETE' });
    const grant = grants.value.find(item => item.id === id);
    if (grant) grant.revokedAt = new Date().toISOString();
  } catch {
    failed.value = true;
  } finally {
    revoking.value = null;
  }
}
void load();
</script>

<template>
  <NCard :title="t('grants')" :bordered="false" size="small" class="card-wrapper" :aria-busy="loading">
    <NAlert v-if="failed" type="error" class="mb-16px" role="alert">
      {{ t('error') }}
      <NButton class="mt-12px" :loading="loading" @click="load()">{{ t('retry') }}</NButton>
    </NAlert>
    <NSpin v-if="loading && !grants.length" />
    <NEmpty v-else-if="!grants.length && !failed" :description="t('empty')" />
    <NList v-else>
      <NListItem v-for="grant in grants" :key="grant.id">
        <div class="flex flex-wrap items-start justify-between gap-16px">
          <div class="min-w-0 flex-1">
            <h3 class="break-words font-600">{{ grant.name }}</h3>
            <p class="mt-8px break-words">{{ grant.scopes.split(' ').map(permissionLabel).join(', ') }}</p>
            <p v-if="grant.allowDelete" class="mt-8px">{{ t('delete') }}</p>
            <p class="mt-8px">{{ t('expires') }}: {{ formatDateTime(grant.expiresAt) }}</p>
          </div>
          <NTag v-if="grant.revokedAt">{{ t('revoked') }}</NTag>
          <NPopconfirm v-else @positive-click="revoke(grant.id)">
            <template #trigger>
              <NButton :loading="revoking === grant.id" :disabled="revoking !== null || loading">
                {{ t('revoke') }}
              </NButton>
            </template>
            {{ t('confirm') }}
          </NPopconfirm>
        </div>
      </NListItem>
    </NList>
    <NButton
      v-if="nextCursor"
      class="mt-16px"
      :loading="loading"
      :disabled="loading || revoking !== null"
      @click="load(true)"
    >
      {{ t('loadMore') }}
    </NButton>
  </NCard>
</template>
