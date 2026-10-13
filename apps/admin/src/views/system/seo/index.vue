<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import type { FormInst, FormItemRule, FormRules } from 'naive-ui';
import { LOCKED_CRAWLERS, buildRobotsTxt } from '@opennavo/shared';
import { fetchSeoSettings, updateSeoSettings } from '@/service/api';
import type { BodyOf, DataOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import { formatDateTime } from '@/utils/opennavo';
import {
  INDEXNOW_KEY,
  MAX_BOTS,
  MEASUREMENT,
  VERIFICATION,
  ZONE,
  botProblem,
  randomIndexNowKey,
  validHttps,
  validLabel,
  validRegions,
  validSecret
} from './modules/validate';
import IndexReport from './modules/index-report.vue';

defineOptions({ name: 'SeoSettings' });

type View = DataOf<'getSeoSettings'>;
type Settings = View['settings'];
type CrawlerClass = 'training' | 'seoTools';

const crawlerClasses: CrawlerClass[] = ['training', 'seoTools'];
const crawlerModes = ['allow', 'limit', 'block'] as const;

// Placeholder until the first load; the form stays hidden while `ready` is false.
function blankSettings(): Settings {
  const rule = { mode: 'allow' as const, delaySeconds: 10, bots: [] };
  return {
    identity: { name: '', alternateNames: [], logoUrl: '', sameAs: [] },
    verification: { google: '', bing: '' },
    analytics: { ga4Enabled: false, ga4MeasurementId: '', consentRegions: [] },
    indexNow: { enabled: false },
    cloudflare: { zoneId: '', purgeEnabled: false },
    edgeCache: { enabled: false },
    crawlers: { training: { ...rule, bots: [] }, seoTools: { ...rule, bots: [] } },
    pages: { titleTemplate: 'new', indexPolicy: { mode: 'off', scope: 'new', since: '' }, versionsSitemap: false }
  };
}

const ready = ref(false);
const view = ref<View>({
  settings: blankSettings(),
  indexNowKeyConfigured: false,
  cloudflareTokenConfigured: false,
  updatedAt: '',
  revision: 0,
  siteUrl: '',
  changeFeed: { lastRunAt: null, lastUrls: 0, lastIndexnowStatus: '', lastPurgeStatus: '', lastError: '' }
});
const draft = ref<Settings>(blankSettings());
const baseline = ref('');
const indexNowKey = ref('');
const cloudflareToken = ref('');
const clearIndexNowKey = ref(false);
const clearCloudflareToken = ref(false);
const form = ref<FormInst>();
const loading = ref(false);
const saving = ref(false);
const loadFailed = ref(false);
const saveFailed = ref(false);
let disposed = false;

const text = (key: string, values?: Record<string, unknown>) =>
  values ? $t(`page.system.seo.${key}` as App.I18n.I18nKey, values) : $t(`page.system.seo.${key}` as App.I18n.I18nKey);
const hasIndexNowKey = computed(
  () => Boolean(indexNowKey.value) || (view.value.indexNowKeyConfigured && !clearIndexNowKey.value)
);
const hasCloudflareToken = computed(
  () => Boolean(cloudflareToken.value) || (view.value.cloudflareTokenConfigured && !clearCloudflareToken.value)
);
const dirty = computed(
  () =>
    ready.value &&
    (JSON.stringify(draft.value) !== baseline.value ||
      Boolean(indexNowKey.value || cloudflareToken.value) ||
      clearIndexNowKey.value ||
      clearCloudflareToken.value)
);
// Same builder as the web server's /robots.txt, so the preview matches the served file.
const robotsPreview = computed(() =>
  ready.value
    ? buildRobotsTxt({
        crawlers: draft.value.crawlers,
        indexable: true,
        sitemap: `${view.value.siteUrl}/sitemap_index.xml`
      })
    : ''
);

// No rule-level message: async-validator would let it replace the specific Error returned here.
const feedSummary = computed(() => {
  const feed = view.value.changeFeed;
  return feed.lastRunAt
    ? text('changeFeed.lastRun', { time: formatDateTime(feed.lastRunAt), count: feed.lastUrls })
    : text('changeFeed.never');
});

function check(valid: () => boolean | Error, message: string): FormItemRule {
  return {
    trigger: ['input', 'blur', 'change'],
    validator: () => {
      const result = valid();
      return result instanceof Error ? result : result || new Error(message);
    }
  };
}

function botRule(name: CrawlerClass): FormItemRule {
  return {
    trigger: ['change', 'blur'],
    validator: () => {
      const crawlers = draft.value.crawlers;
      const problem = botProblem(crawlers[name].bots, crawlers[name === 'training' ? 'seoTools' : 'training'].bots);
      if (!problem) return true;
      return new Error(
        problem.key === 'tooMany'
          ? text('errors.tooMany', { max: MAX_BOTS })
          : text(`errors.${problem.key}`, { bot: problem.bot })
      );
    }
  };
}

const rules = computed<FormRules>(() => {
  const s = draft.value;
  const delay = (name: CrawlerClass) =>
    check(() => {
      const value = s.crawlers[name].delaySeconds;
      return Number.isInteger(value) && value >= 1 && value <= 60;
    }, text('errors.invalidDelay'));
  return {
    siteName: check(() => validLabel(s.identity.name, 60), text('errors.invalidName')),
    alternateNames: check(
      () => s.identity.alternateNames.length <= 5 && s.identity.alternateNames.every(name => validLabel(name, 80)),
      text('errors.invalidAlternateName')
    ),
    logoUrl: check(() => !s.identity.logoUrl || validHttps(s.identity.logoUrl, 500), text('errors.invalidUrl')),
    sameAs: check(
      () => s.identity.sameAs.length <= 10 && s.identity.sameAs.every(link => validHttps(link, 300)),
      text('errors.invalidUrl')
    ),
    google: check(() => VERIFICATION.test(s.verification.google), text('errors.invalidCode')),
    bing: check(() => VERIFICATION.test(s.verification.bing), text('errors.invalidCode')),
    ga4MeasurementId: check(() => {
      if (!MEASUREMENT.test(s.analytics.ga4MeasurementId)) return false;
      return !s.analytics.ga4Enabled || Boolean(s.analytics.ga4MeasurementId) || new Error(text('errors.ga4Required'));
    }, text('errors.invalidGa4')),
    consentRegions: check(() => validRegions(s.analytics.consentRegions), text('errors.invalidRegion')),
    indexNowKey: check(() => {
      if (!INDEXNOW_KEY.test(indexNowKey.value)) return false;
      return !s.indexNow.enabled || hasIndexNowKey.value || new Error(text('errors.keyRequired'));
    }, text('errors.invalidKey')),
    zoneId: check(() => ZONE.test(s.cloudflare.zoneId), text('errors.invalidZone')),
    cloudflareToken: check(() => {
      if (!validSecret(cloudflareToken.value, 200)) return false;
      const purgeReady = hasCloudflareToken.value && Boolean(s.cloudflare.zoneId);
      return !s.cloudflare.purgeEnabled || purgeReady || new Error(text('errors.purgeRequires'));
    }, text('errors.invalidToken')),
    trainingBots: botRule('training'),
    seoToolsBots: botRule('seoTools'),
    trainingDelay: delay('training'),
    seoToolsDelay: delay('seoTools')
  };
});

function apply(value: View) {
  ready.value = true;
  view.value = value;
  draft.value = structuredClone(value.settings);
  baseline.value = JSON.stringify(draft.value);
  indexNowKey.value = '';
  cloudflareToken.value = '';
  clearIndexNowKey.value = false;
  clearCloudflareToken.value = false;
  form.value?.restoreValidation();
}

async function load() {
  if (loading.value || saving.value) return;
  loading.value = true;
  loadFailed.value = false;
  try {
    const { data, error } = await fetchSeoSettings();
    if (disposed) return;
    if (error) loadFailed.value = true;
    else apply(data);
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (!dirty.value || saving.value || loading.value || loadFailed.value) return;
  try {
    await form.value?.validate();
  } catch {
    return;
  }
  saving.value = true;
  saveFailed.value = false;
  try {
    const body: BodyOf<'updateSeoSettings'> = {
      settings: draft.value,
      clearIndexNowKey: clearIndexNowKey.value,
      clearCloudflareToken: clearCloudflareToken.value
    };
    if (indexNowKey.value) body.indexNowKey = indexNowKey.value;
    if (cloudflareToken.value) body.cloudflareToken = cloudflareToken.value;
    const { data, error } = await updateSeoSettings(body);
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

function generateKey() {
  indexNowKey.value = randomIndexNowKey();
  clearIndexNowKey.value = false;
}

onBeforeUnmount(() => {
  disposed = true;
  indexNowKey.value = '';
  cloudflareToken.value = '';
});
void load();
</script>

<template>
  <NCard :title="text('title')" :bordered="false" class="card-wrapper">
    <template #header-extra>
      <NButton :loading="loading" :disabled="saving" @click="load">{{ $t('common.refresh') }}</NButton>
    </template>
    <div class="max-w-860px">
      <NP class="mt-0">{{ text('scope') }}</NP>
      <NAlert v-if="loadFailed" type="error" class="mb-20px">{{ text('loadFailed') }}</NAlert>
      <NSkeleton v-if="loading && !ready" text :repeat="8" />
      <NForm
        v-if="ready"
        ref="form"
        :model="draft"
        :rules="rules"
        label-placement="top"
        :disabled="saving || loading || loadFailed"
      >
        <NDivider title-placement="left">{{ text('sections.identity') }}</NDivider>
        <div class="grid grid-cols-1 gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('siteName')" path="siteName">
            <NInput
              v-model:value="draft.identity.name"
              :maxlength="60"
              :input-props="{ 'aria-label': text('siteName') }"
            />
          </NFormItem>
          <NFormItem :label="text('logoUrl')" path="logoUrl">
            <NInput
              v-model:value="draft.identity.logoUrl"
              :maxlength="500"
              placeholder="https://"
              :input-props="{ 'aria-label': text('logoUrl') }"
            />
          </NFormItem>
        </div>
        <NFormItem :label="text('alternateNames')" path="alternateNames">
          <div class="w-full">
            <NDynamicTags v-model:value="draft.identity.alternateNames" :max="5" :input-props="{ maxlength: 80 }" />
            <NText depth="3" class="mt-8px block">{{ text('alternateNamesHint') }}</NText>
          </div>
        </NFormItem>
        <NFormItem :label="text('sameAs')" path="sameAs">
          <div class="w-full">
            <NDynamicInput v-model:value="draft.identity.sameAs" :max="10" placeholder="https://" />
            <NText depth="3" class="mt-8px block">{{ text('sameAsHint') }}</NText>
          </div>
        </NFormItem>

        <NDivider title-placement="left">{{ text('sections.verification') }}</NDivider>
        <div class="grid grid-cols-1 gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('google')" path="google">
            <NInput
              v-model:value="draft.verification.google"
              :maxlength="100"
              :input-props="{ 'aria-label': text('google'), spellcheck: false }"
            />
          </NFormItem>
          <NFormItem :label="text('bing')" path="bing">
            <NInput
              v-model:value="draft.verification.bing"
              :maxlength="100"
              :input-props="{ 'aria-label': text('bing'), spellcheck: false }"
            />
          </NFormItem>
        </div>
        <NText depth="3" class="mb-20px block">{{ text('verificationHint') }}</NText>

        <NDivider title-placement="left">{{ text('sections.analytics') }}</NDivider>
        <div class="grid grid-cols-1 gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('ga4Enabled')">
            <NSwitch v-model:value="draft.analytics.ga4Enabled" :aria-label="text('ga4Enabled')" />
          </NFormItem>
          <NFormItem :label="text('ga4MeasurementId')" path="ga4MeasurementId">
            <NInput
              v-model:value="draft.analytics.ga4MeasurementId"
              :maxlength="18"
              :placeholder="text('ga4Placeholder')"
              :input-props="{ 'aria-label': text('ga4MeasurementId'), spellcheck: false }"
            />
          </NFormItem>
        </div>
        <NFormItem :label="text('consentRegions')" path="consentRegions">
          <div class="w-full">
            <NDynamicTags
              v-model:value="draft.analytics.consentRegions"
              :max="60"
              :input-props="{ maxlength: 2 }"
              :on-create="(value: string) => value.trim().toUpperCase()"
            />
            <NText depth="3" class="mt-8px block">{{ text('consentRegionsHint') }}</NText>
          </div>
        </NFormItem>

        <NDivider title-placement="left">{{ text('sections.indexNow') }}</NDivider>
        <NFormItem :label="text('indexNowEnabled')">
          <NSwitch v-model:value="draft.indexNow.enabled" :aria-label="text('indexNowEnabled')" />
        </NFormItem>
        <NFormItem :label="text('indexNowKey')" path="indexNowKey">
          <div class="w-full">
            <div class="flex flex-wrap items-center gap-8px">
              <NInput
                v-model:value="indexNowKey"
                type="password"
                show-password-on="click"
                class="min-w-240px flex-1"
                :maxlength="128"
                :disabled="clearIndexNowKey"
                :placeholder="text('keyPlaceholder')"
                :input-props="{ autocomplete: 'new-password', spellcheck: false, 'aria-label': text('indexNowKey') }"
              />
              <NButton :disabled="clearIndexNowKey" @click="generateKey">{{ text('generateKey') }}</NButton>
            </div>
            <div class="mt-8px flex flex-wrap items-center gap-8px">
              <NTag size="small" :type="view.indexNowKeyConfigured ? 'success' : 'warning'">
                {{ text(view.indexNowKeyConfigured ? 'configured' : 'notConfigured') }}
              </NTag>
              <NText depth="3">{{ text('indexNowHint') }}</NText>
            </div>
          </div>
        </NFormItem>
        <NFormItem v-if="view.indexNowKeyConfigured" :label="text('clearIndexNowKey')">
          <NSwitch
            v-model:value="clearIndexNowKey"
            :disabled="Boolean(indexNowKey)"
            :aria-label="text('clearIndexNowKey')"
          />
        </NFormItem>

        <NDivider title-placement="left">{{ text('sections.cdn') }}</NDivider>
        <div class="grid grid-cols-1 gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('zoneId')" path="zoneId">
            <NInput
              v-model:value="draft.cloudflare.zoneId"
              :maxlength="32"
              :input-props="{ 'aria-label': text('zoneId'), spellcheck: false }"
            />
          </NFormItem>
          <NFormItem :label="text('cloudflareToken')" path="cloudflareToken">
            <div class="w-full">
              <NInput
                v-model:value="cloudflareToken"
                type="password"
                :maxlength="200"
                :disabled="clearCloudflareToken"
                :placeholder="text('tokenPlaceholder')"
                :input-props="{
                  autocomplete: 'new-password',
                  spellcheck: false,
                  'aria-label': text('cloudflareToken')
                }"
              />
              <div class="mt-8px flex flex-wrap items-center gap-8px">
                <NTag size="small" :type="view.cloudflareTokenConfigured ? 'success' : 'warning'">
                  {{ text(view.cloudflareTokenConfigured ? 'configured' : 'notConfigured') }}
                </NTag>
                <NText depth="3">{{ text('secretHint') }}</NText>
              </div>
            </div>
          </NFormItem>
        </div>
        <NFormItem v-if="view.cloudflareTokenConfigured" :label="text('clearCloudflareToken')">
          <NSwitch
            v-model:value="clearCloudflareToken"
            :disabled="Boolean(cloudflareToken)"
            :aria-label="text('clearCloudflareToken')"
          />
        </NFormItem>
        <div class="grid grid-cols-1 items-start gap-x-20px sm:grid-cols-2">
          <NFormItem :label="text('purgeEnabled')">
            <div>
              <NSwitch v-model:value="draft.cloudflare.purgeEnabled" :aria-label="text('purgeEnabled')" />
            </div>
          </NFormItem>
          <NFormItem :label="text('edgeCacheEnabled')">
            <div>
              <NSwitch v-model:value="draft.edgeCache.enabled" :aria-label="text('edgeCacheEnabled')" />
              <NText depth="3" class="mt-8px block">{{ text('edgeCacheHint') }}</NText>
            </div>
          </NFormItem>
        </div>

        <NFormItem :label="text('changeFeed.title')">
          <div class="flex flex-col gap-4px" data-testid="change-feed">
            <NText depth="3">{{ feedSummary }}</NText>
            <NText v-if="view.changeFeed.lastIndexnowStatus" depth="3">
              {{ text('changeFeed.indexNow', { status: view.changeFeed.lastIndexnowStatus }) }}
            </NText>
            <NText v-if="view.changeFeed.lastPurgeStatus" depth="3">
              {{ text('changeFeed.purge', { status: view.changeFeed.lastPurgeStatus }) }}
            </NText>
            <NText v-if="view.changeFeed.lastError" type="error">
              {{ text('changeFeed.error', { error: view.changeFeed.lastError }) }}
            </NText>
          </div>
        </NFormItem>

        <NDivider title-placement="left">{{ text('sections.crawlers') }}</NDivider>
        <NAlert type="info" :title="text('lockedTitle')" class="mb-20px">
          <NText>{{ text('lockedHint') }}</NText>
          <div class="mt-8px flex flex-wrap gap-6px">
            <NTag v-for="bot in LOCKED_CRAWLERS" :key="bot" size="small" :bordered="false">{{ bot }}</NTag>
          </div>
        </NAlert>
        <div v-for="name in crawlerClasses" :key="name" class="mb-12px">
          <NText strong class="mb-8px block">{{ text(`crawlerClasses.${name}`) }}</NText>
          <div class="flex flex-wrap items-start gap-x-32px">
            <NFormItem :label="text('mode')">
              <div>
                <NRadioGroup v-model:value="draft.crawlers[name].mode" :aria-label="text(`crawlerClasses.${name}`)">
                  <NRadioButton v-for="mode in crawlerModes" :key="mode" :value="mode">
                    {{ text(`modes.${mode}`) }}
                  </NRadioButton>
                </NRadioGroup>
                <NText depth="3" class="mt-8px block">{{ text(`modeHints.${draft.crawlers[name].mode}`) }}</NText>
              </div>
            </NFormItem>
            <NFormItem
              v-if="draft.crawlers[name].mode === 'limit'"
              :label="text('delaySeconds')"
              :path="`${name}Delay`"
              class="w-160px"
            >
              <NInputNumber
                :value="draft.crawlers[name].delaySeconds"
                :min="1"
                :max="60"
                :precision="0"
                :input-props="{ 'aria-label': text('delaySeconds') }"
                @update:value="value => (draft.crawlers[name].delaySeconds = value ?? 0)"
              />
            </NFormItem>
          </div>
          <NFormItem :label="text('bots')" :path="`${name}Bots`">
            <NDynamicTags
              v-model:value="draft.crawlers[name].bots"
              :max="MAX_BOTS"
              :input-props="{ maxlength: 40 }"
              :on-create="(value: string) => value.trim()"
            />
          </NFormItem>
        </div>
        <NFormItem :label="text('preview')">
          <div class="w-full">
            <div
              class="max-h-360px overflow-auto rd-6px bg-#f5f5f7 p-12px dark:bg-#18181c"
              data-testid="robots-preview"
            >
              <NCode :code="robotsPreview" class="text-12px" />
            </div>
            <NText depth="3" class="mt-8px block">{{ text('previewHint') }}</NText>
          </div>
        </NFormItem>

        <NDivider title-placement="left">{{ text('sections.pages') }}</NDivider>
        <NFormItem :label="text('versionsSitemap')">
          <div>
            <NSwitch v-model:value="draft.pages.versionsSitemap" :aria-label="text('versionsSitemap')" />
            <NText depth="3" class="mt-8px block">{{ text('versionsSitemapHint') }}</NText>
          </div>
        </NFormItem>
        <NAlert type="warning" :title="text('temporaryTitle')" class="mb-20px">{{ text('temporaryHint') }}</NAlert>
        <div class="grid grid-cols-1 gap-x-20px sm:grid-cols-3">
          <NFormItem :label="text('titleTemplate')">
            <NRadioGroup v-model:value="draft.pages.titleTemplate" :aria-label="text('titleTemplate')">
              <NRadioButton v-for="value in ['new', 'legacy']" :key="value" :value="value">
                {{ text(`titleTemplates.${value}`) }}
              </NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <NFormItem :label="text('indexMode')">
            <NRadioGroup v-model:value="draft.pages.indexPolicy.mode" :aria-label="text('indexMode')">
              <NRadioButton v-for="value in ['off', 'shadow', 'enforce']" :key="value" :value="value">
                {{ text(`indexModes.${value}`) }}
              </NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <NFormItem :label="text('indexScope')">
            <NRadioGroup v-model:value="draft.pages.indexPolicy.scope" :aria-label="text('indexScope')">
              <NRadioButton v-for="value in ['new', 'all']" :key="value" :value="value">
                {{ text(`indexScopes.${value}`) }}
              </NRadioButton>
            </NRadioGroup>
          </NFormItem>
        </div>
        <NText depth="3" class="mb-8px block">{{ text('indexScopeHint') }}</NText>
        <NText v-if="draft.pages.indexPolicy.since" depth="3" class="mb-8px block" data-testid="index-since">
          {{ text('indexSince', { date: formatDateTime(draft.pages.indexPolicy.since) }) }}
        </NText>
        <NText depth="3" class="mb-20px block">{{ text('overrideHint') }}</NText>
        <NFormItem :label="text('report.title')">
          <IndexReport />
        </NFormItem>

        <NAlert v-if="saveFailed" type="error" class="mb-20px">{{ text('saveFailed') }}</NAlert>
        <div class="flex flex-wrap items-center gap-12px">
          <NButton type="primary" :loading="saving" :disabled="!dirty || loading || loadFailed" @click="save">
            {{ text('save') }}
          </NButton>
          <NText depth="3">{{ text('effect') }}</NText>
          <NText depth="3">
            {{ text('savedAt', { time: formatDateTime(view.updatedAt), revision: view.revision }) }}
          </NText>
        </div>
      </NForm>
    </div>
  </NCard>
</template>
