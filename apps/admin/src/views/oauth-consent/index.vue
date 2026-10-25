<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { authorizationParams, compatibleScopes, oauthRequest } from '@/service/api/oauth';
import type { OAuthConsent } from '@/service/api/oauth';
import { oauthMessages } from '@/locales/oauth';
import { permissionLabel } from '@/views/system/agent/modules/permissions';

const { t } = useI18n({ useScope: 'local', messages: oauthMessages });
const route = useRoute();
const consent = ref<OAuthConsent | null>(null);
const selected = ref<string[]>([]);
const allowDelete = ref(false);
const loading = ref(false);
const saving = ref(false);
const failed = ref(false);
const compatible = computed(() => compatibleScopes(selected.value));
let generation = 0;
let authorization: Record<string, string> = {};

async function load() {
  const current = ++generation;
  consent.value = null;
  allowDelete.value = false;
  failed.value = false;
  loading.value = true;
  try {
    const params = authorizationParams(route.fullPath.split('?')[1] || '');
    const data = await oauthRequest<OAuthConsent>(`consent?${new URLSearchParams(params)}`);
    if (current !== generation) return;
    authorization = params;
    consent.value = data;
    selected.value = data.requestedScopes.filter(
      scope => data.allowedScopes.includes(scope) && scope.endsWith(':view')
    );
  } catch {
    if (current === generation) failed.value = true;
  } finally {
    if (current === generation) loading.value = false;
  }
}

async function decide(approve: boolean) {
  if (!consent.value || saving.value) return;
  saving.value = true;
  failed.value = false;
  try {
    const result = await oauthRequest<{ redirect: string }>('consent', {
      method: 'POST',
      body: JSON.stringify({ authorization, scopes: selected.value, allowDelete: allowDelete.value, approve })
    });
    const callback = new URL(result.redirect);
    if (!['https:', 'http:'].includes(callback.protocol)) throw new Error('invalid_callback');
    window.location.assign(callback.href);
  } catch {
    failed.value = true;
  } finally {
    saving.value = false;
  }
}

watch(() => route.fullPath, load, { immediate: true });
</script>

<template>
  <main class="min-h-full flex justify-center bg-layout p-24px lt-sm:p-12px">
    <NCard :bordered="false" class="my-auto max-w-640px" :aria-busy="loading || saving">
      <header class="mb-24px flex items-center gap-12px">
        <SystemLogo class="size-32px shrink-0" inline />
        <h1 class="text-20px font-600">{{ t('title') }}</h1>
      </header>
      <NSpin v-if="loading" class="block py-32px" />
      <NAlert v-if="failed" type="error" class="mb-16px" role="alert">
        {{ t('error') }}
        <NButton v-if="!consent" class="mt-12px" :loading="loading" @click="load">{{ t('retry') }}</NButton>
      </NAlert>
      <template v-if="consent">
        <h2 class="break-words text-18px font-600">{{ consent.clientName }}</h2>
        <p class="mb-16px mt-8px">{{ t('description') }}</p>
        <dl class="mb-24px">
          <dt class="font-600">{{ t('callback') }}</dt>
          <dd class="mt-4px break-all">{{ consent.redirectUri }}</dd>
        </dl>
        <fieldset :disabled="saving" class="m-0 border-0 p-0">
          <legend class="mb-12px font-600">{{ t('permissions') }}</legend>
          <NCheckboxGroup v-model:value="selected">
            <div class="flex flex-col gap-12px">
              <NCheckbox
                v-for="scope in consent.requestedScopes"
                :key="scope"
                :value="scope"
                :disabled="saving || !consent.allowedScopes.includes(scope)"
              >
                {{ permissionLabel(scope) }}
                <span v-if="!consent.allowedScopes.includes(scope)">— {{ t('unavailable') }}</span>
              </NCheckbox>
            </div>
          </NCheckboxGroup>
          <NCheckbox v-model:checked="allowDelete" class="mt-24px" :disabled="saving">{{ t('delete') }}</NCheckbox>
        </fieldset>
        <NAlert v-if="!compatible" type="warning" class="mt-16px">{{ t('incompatible') }}</NAlert>
        <p class="my-24px">{{ t('limited') }}</p>
        <div class="flex flex-wrap justify-end gap-12px">
          <NButton :disabled="saving" @click="decide(false)">{{ t('deny') }}</NButton>
          <NButton
            type="primary"
            :disabled="!selected.length || !compatible || saving"
            :loading="saving"
            @click="decide(true)"
          >
            {{ t('approve') }}
          </NButton>
        </div>
      </template>
    </NCard>
  </main>
</template>
