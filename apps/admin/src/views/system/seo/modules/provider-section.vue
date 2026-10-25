<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { fetchSeoProvider, updateSeoProvider } from '@/service/api';
import type { SeoProviderKey } from '@/service/api/system';
import type { BodyOf, DataOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import {
  GSC_PROPERTY_EXAMPLE,
  MAX_CREDENTIAL,
  MAX_SERVICE_ACCOUNT,
  serviceAccountProblem,
  validGscProperty,
  validHttps,
  validOrigin,
  validSecret
} from './validate';

defineOptions({ name: 'SeoProviderSection' });

// One ingestion source: its state, data progress and write-only credential form. Saves on its own endpoint.
const props = defineProps<{ source: SeoProviderKey; siteUrl: string; revision: number }>();

type View = DataOf<'getSeoProvider'>;
type Body = BodyOf<'updateSeoProvider'>;
type Watermark = View['watermarks'][number];

const blank = () => ({
  enabled: false,
  clear: false,
  mode: 'service_account' as 'service_account' | 'oauth',
  property: '',
  serviceAccountJson: '',
  keyFrom: 'none' as 'none' | 'file' | 'paste',
  keyEmail: '',
  clientId: '',
  clientSecret: '',
  refreshToken: '',
  apiKey: '',
  originUrl: ''
});

const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.system.seo.providers.${key}` as App.I18n.I18nKey, values)
    : $t(`page.system.seo.providers.${key}` as App.I18n.I18nKey);

const view = ref<View | null>(null);
const draft = reactive(blank());
const loading = ref(false);
const loadFailed = ref(false);
const saving = ref(false);
const saveFailed = ref(false);
const problem = ref('');
let disposed = false;

function apply(value: View) {
  view.value = value;
  Object.assign(draft, blank(), {
    enabled: value.enabled,
    mode: value.credentialType === 'oauth' ? 'oauth' : 'service_account'
  });
  problem.value = '';
  saveFailed.value = false;
}

async function load() {
  if (loading.value || saving.value) return;
  loading.value = true;
  try {
    const { data, error } = await fetchSeoProvider(props.source);
    if (disposed) return;
    loadFailed.value = Boolean(error);
    if (!error) apply(data);
  } finally {
    loading.value = false;
  }
}

/** Whether new credential values were typed or chosen. */
const entered = computed(() => {
  if (props.source === 'gsc')
    return Boolean(
      draft.property || draft.serviceAccountJson || draft.clientId || draft.clientSecret || draft.refreshToken
    );
  if (props.source === 'bing') return Boolean(draft.property || draft.apiKey);
  return Boolean(draft.originUrl);
});

const dirty = computed(
  () => Boolean(view.value) && (draft.enabled !== view.value!.enabled || draft.clear || entered.value)
);

/** First problem as a message key under providers.errors, or '' when the draft can be saved. */
function check(): string {
  if (draft.clear) return '';
  if (entered.value) {
    if (props.source === 'gsc') {
      if (!validGscProperty(draft.property.trim())) return 'errors.gscProperty';
      if (draft.mode === 'service_account') {
        // Credentials are replaced whole, so changing only the property still needs the key.
        if (!draft.serviceAccountJson.trim()) return 'errors.serviceAccount.missing';
        const issue = serviceAccountProblem(draft.serviceAccountJson);
        if (issue) return `errors.serviceAccount.${issue}`;
      } else if (
        ![draft.clientId, draft.clientSecret, draft.refreshToken].every(
          value => value && validSecret(value, MAX_CREDENTIAL)
        )
      ) {
        return 'errors.oauth';
      }
    } else if (props.source === 'bing') {
      if (!validHttps(draft.property.trim(), MAX_CREDENTIAL)) return 'errors.bingProperty';
      if (!draft.apiKey || !validSecret(draft.apiKey, MAX_CREDENTIAL)) return 'errors.apiKey';
    } else if (!validOrigin(draft.originUrl.trim(), props.siteUrl)) {
      return 'errors.origin';
    }
  }
  if (draft.enabled && !entered.value && !view.value?.configured) return 'errors.credentialsRequired';
  return '';
}

function body(): Body {
  // Clearing stored credentials also stops ingestion; the server rejects credentials plus clear together.
  if (draft.clear) return { enabled: false, clearCredentials: true };
  const result: Body = { enabled: draft.enabled };
  if (!entered.value) return result;
  if (props.source === 'gsc') {
    result.credentials =
      draft.mode === 'service_account'
        ? { property: draft.property.trim(), serviceAccountJson: draft.serviceAccountJson }
        : {
            property: draft.property.trim(),
            clientId: draft.clientId,
            clientSecret: draft.clientSecret,
            refreshToken: draft.refreshToken
          };
  } else if (props.source === 'bing') {
    result.credentials = { property: draft.property.trim(), apiKey: draft.apiKey };
  } else {
    result.credentials = { originUrl: draft.originUrl.trim() };
  }
  return result;
}

async function save() {
  if (!dirty.value || saving.value || loading.value) return;
  const issue = check();
  problem.value = issue ? text(issue) : '';
  if (issue) return;
  saving.value = true;
  saveFailed.value = false;
  try {
    const { data, error } = await updateSeoProvider(props.source, body());
    if (disposed) return;
    if (error) saveFailed.value = true;
    else {
      apply(data);
      window.$message?.success($t('page.shared.saved'));
    }
  } finally {
    saving.value = false;
  }
}

/** Clearing credentials also stops ingestion, so the switch follows; turning it back restores the saved state. */
function setClear(value: boolean) {
  draft.clear = value;
  draft.enabled = value ? false : Boolean(view.value?.enabled);
}

/** Reads a downloaded key file into memory only; the key itself is never rendered. */
async function chooseKey(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  Object.assign(draft, { serviceAccountJson: '', keyFrom: 'file', keyEmail: '' });
  if (file.size > MAX_SERVICE_ACCOUNT) {
    problem.value = text('errors.serviceAccount.tooLong');
    return;
  }
  const content = await file.text();
  if (disposed) return;
  const issue = serviceAccountProblem(content);
  draft.serviceAccountJson = content;
  draft.keyEmail = issue ? '' : (JSON.parse(content) as { client_email: string }).client_email;
  problem.value = issue ? text(`errors.serviceAccount.${issue}`) : '';
}

function forgetKey() {
  Object.assign(draft, { serviceAccountJson: '', keyFrom: 'none', keyEmail: '' });
  problem.value = '';
}

function pasteKey() {
  forgetKey();
  draft.keyFrom = 'paste';
}

// completeThrough/freshThrough are calendar days (YYYY-MM-DD), shown as sent.
function progress(mark: Watermark) {
  const label = text(`watermarks.${mark.source}`);
  if (!mark.completeThrough && !mark.freshThrough) return text('progressNone', { label });
  return text('progress', {
    label,
    complete: mark.completeThrough ?? text('none'),
    fresh: mark.freshThrough ?? text('none')
  });
}

watch(
  () => props.revision,
  () => void load()
);
onBeforeUnmount(() => {
  disposed = true;
  Object.assign(draft, { serviceAccountJson: '', clientSecret: '', refreshToken: '', apiKey: '' });
});
void load();
</script>

<template>
  <section class="mb-24px" :data-testid="`provider-${source}`">
    <NDivider title-placement="left">{{ text(`sources.${source}.title`) }}</NDivider>
    <NAlert v-if="loadFailed" type="error" class="mb-12px">{{ text('loadFailed') }}</NAlert>
    <NSkeleton v-else-if="!view" text :repeat="3" />
    <template v-else>
      <div class="mb-8px flex flex-wrap items-center gap-8px">
        <NTag size="small" :type="view.enabled ? 'success' : 'default'">
          {{ text(view.enabled ? 'state.enabled' : 'state.disabled') }}
        </NTag>
        <NTag size="small" :type="view.configured ? 'success' : 'warning'">
          {{ text(view.configured ? 'state.configured' : 'state.notConfigured') }}
        </NTag>
        <NTag v-if="view.credentialType" size="small" :bordered="false">
          {{ text(`credentialTypes.${view.credentialType}`) }}
        </NTag>
      </div>
      <NText depth="3" class="mb-8px block">{{ text(`sources.${source}.hint`) }}</NText>
      <div class="mb-12px flex flex-col gap-2px" :data-testid="`progress-${source}`">
        <NText v-for="mark in view.watermarks" :key="mark.source" depth="3">{{ progress(mark) }}</NText>
        <NText v-if="!view.watermarks.length" depth="3">{{ text('noProgress') }}</NText>
      </div>
      <NForm label-placement="top" :disabled="saving || loading" @submit.prevent>
        <template v-if="source === 'gsc'">
          <NFormItem :label="text('sources.gsc.mode')">
            <NRadioGroup
              v-model:value="draft.mode"
              :disabled="saving || loading || draft.clear"
              :aria-label="text('sources.gsc.mode')"
            >
              <NRadioButton value="service_account">{{ text('sources.gsc.modes.service_account') }}</NRadioButton>
              <NRadioButton value="oauth">{{ text('sources.gsc.modes.oauth') }}</NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <NFormItem :label="text('sources.gsc.property')">
            <div class="w-full">
              <NInput
                v-model:value="draft.property"
                :maxlength="MAX_CREDENTIAL"
                :disabled="saving || loading || draft.clear"
                :placeholder="GSC_PROPERTY_EXAMPLE"
                :input-props="{ spellcheck: false, autocomplete: 'off', 'aria-label': text('sources.gsc.property') }"
              />
              <NText depth="3" class="mt-8px block">{{ text('sources.gsc.propertyHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem v-if="draft.mode === 'service_account'" :label="text('sources.gsc.serviceAccountJson')">
            <div class="w-full">
              <div class="flex flex-wrap items-center gap-8px">
                <label class="cursor-pointer">
                  <input
                    type="file"
                    accept=".json,application/json"
                    class="hidden"
                    data-testid="service-account-file"
                    :disabled="saving || loading || draft.clear"
                    @change="chooseKey"
                  />
                  <NButton tag="span" :disabled="saving || loading || draft.clear">
                    {{ text('sources.gsc.chooseFile') }}
                  </NButton>
                </label>
                <NButton
                  v-if="draft.keyFrom !== 'paste'"
                  text
                  :disabled="saving || loading || draft.clear"
                  @click="pasteKey"
                >
                  {{ text('sources.gsc.pasteJson') }}
                </NButton>
                <NText v-if="draft.keyFrom === 'file'" depth="3" data-testid="key-summary">
                  {{
                    draft.keyEmail
                      ? text('sources.gsc.keyLoaded', { email: draft.keyEmail })
                      : text('sources.gsc.keyInvalid')
                  }}
                </NText>
                <NButton
                  v-if="draft.keyFrom === 'file'"
                  text
                  :disabled="saving || loading || draft.clear"
                  @click="forgetKey"
                >
                  {{ text('sources.gsc.forgetKey') }}
                </NButton>
              </div>
              <NInput
                v-if="draft.keyFrom === 'paste'"
                v-model:value="draft.serviceAccountJson"
                type="textarea"
                class="mt-8px"
                :autosize="{ minRows: 3, maxRows: 6 }"
                :maxlength="MAX_SERVICE_ACCOUNT"
                :disabled="saving || loading || draft.clear"
                :placeholder="text('sources.gsc.pastePlaceholder')"
                :input-props="{
                  spellcheck: false,
                  autocomplete: 'off',
                  'aria-label': text('sources.gsc.serviceAccountJson')
                }"
              />
              <NText depth="3" class="mt-8px block">{{ text('sources.gsc.serviceAccountHint') }}</NText>
            </div>
          </NFormItem>
          <div v-else class="grid grid-cols-1 gap-x-20px sm:grid-cols-3">
            <NFormItem :label="text('sources.gsc.clientId')">
              <NInput
                v-model:value="draft.clientId"
                :maxlength="MAX_CREDENTIAL"
                :disabled="saving || loading || draft.clear"
                :input-props="{ spellcheck: false, autocomplete: 'off', 'aria-label': text('sources.gsc.clientId') }"
              />
            </NFormItem>
            <NFormItem :label="text('sources.gsc.clientSecret')">
              <NInput
                v-model:value="draft.clientSecret"
                type="password"
                :maxlength="MAX_CREDENTIAL"
                :disabled="saving || loading || draft.clear"
                :input-props="{
                  autocomplete: 'new-password',
                  spellcheck: false,
                  'aria-label': text('sources.gsc.clientSecret')
                }"
              />
            </NFormItem>
            <NFormItem :label="text('sources.gsc.refreshToken')">
              <NInput
                v-model:value="draft.refreshToken"
                type="password"
                :maxlength="MAX_CREDENTIAL"
                :disabled="saving || loading || draft.clear"
                :input-props="{
                  autocomplete: 'new-password',
                  spellcheck: false,
                  'aria-label': text('sources.gsc.refreshToken')
                }"
              />
            </NFormItem>
          </div>
        </template>
        <div v-else-if="source === 'bing'" class="grid grid-cols-1 gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('sources.bing.property')">
            <NInput
              v-model:value="draft.property"
              :maxlength="MAX_CREDENTIAL"
              :disabled="saving || loading || draft.clear"
              placeholder="https://opennavo.com/"
              :input-props="{ spellcheck: false, autocomplete: 'off', 'aria-label': text('sources.bing.property') }"
            />
          </NFormItem>
          <NFormItem :label="text('sources.bing.apiKey')">
            <div class="w-full">
              <NInput
                v-model:value="draft.apiKey"
                type="password"
                :maxlength="MAX_CREDENTIAL"
                :disabled="saving || loading || draft.clear"
                :input-props="{
                  autocomplete: 'new-password',
                  spellcheck: false,
                  'aria-label': text('sources.bing.apiKey')
                }"
              />
              <NText depth="3" class="mt-8px block">{{ text('sources.bing.apiKeyHint') }}</NText>
            </div>
          </NFormItem>
        </div>
        <NFormItem v-else :label="text('sources.audit.originUrl')">
          <div class="w-full">
            <NInput
              v-model:value="draft.originUrl"
              :maxlength="MAX_CREDENTIAL"
              :disabled="saving || loading || draft.clear"
              placeholder="https://origin.example.com"
              :input-props="{ spellcheck: false, autocomplete: 'off', 'aria-label': text('sources.audit.originUrl') }"
            />
            <NText depth="3" class="mt-8px block">{{ text('sources.audit.originHint') }}</NText>
          </div>
        </NFormItem>
        <div class="grid grid-cols-1 gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('enabled')">
            <div>
              <NSwitch
                v-model:value="draft.enabled"
                :disabled="saving || loading || draft.clear"
                :aria-label="text('enabled')"
              />
              <NText depth="3" class="mt-8px block">{{ text('enabledHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem v-if="view?.configured" :label="text('clearCredentials')">
            <div>
              <NSwitch
                :value="draft.clear"
                :disabled="saving || loading"
                :aria-label="text('clearCredentials')"
                @update:value="setClear"
              />
              <NText depth="3" class="mt-8px block">{{ text('clearHint') }}</NText>
            </div>
          </NFormItem>
        </div>
        <NAlert v-if="problem" type="warning" class="mb-12px" :data-testid="`problem-${source}`">{{ problem }}</NAlert>
        <NAlert v-if="saveFailed" type="error" class="mb-12px">{{ text('saveFailed') }}</NAlert>
        <NButton
          type="primary"
          :loading="saving"
          :disabled="saving || loading || !dirty"
          :data-testid="`save-${source}`"
          @click="save"
        >
          {{ text('save') }}
        </NButton>
      </NForm>
    </template>
  </section>
</template>
