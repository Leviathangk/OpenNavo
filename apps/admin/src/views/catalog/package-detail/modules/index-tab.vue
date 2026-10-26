<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { LOCALE_METADATA } from '@opennavo/shared';
import { fetchPackageIndexStatus, updatePackageIndexStatus } from '@/service/api';
import type { DataOf } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import { formatDateTime } from '@/utils/opennavo';
import { useAuth } from '@/hooks/business/auth';
import IntentPages from './intent-pages.vue';
import SeoOverride from './seo-override.vue';

defineOptions({ name: 'IndexTab' });

// Index eligibility for one app (WP11): the rule's verdict, what pages do now, and the manual
// override with its reason (shown in the SEO impact report). Below it, the per-language intent page
// switches and search snippet overrides, which agents can set through MCP as well.
const props = defineProps<{ packageId: number }>();

const { hasAuth } = useAuth();
// Index status is a site setting (system:seo:edit); the two blocks below are catalog data.
const canIndex = computed(() => hasAuth('system:seo:edit'));
const canView = computed(() => hasAuth('catalog:package:view'));
const canEdit = computed(() => hasAuth('catalog:package:edit'));

type Status = DataOf<'getPackageIndexStatus'>;
type Override = Status['override'];
const overrides: Override[] = ['auto', 'index', 'noindex'];
const status = ref<Status>();
const override = ref<Override>('auto');
const reason = ref('');
const loading = ref(false);
const saving = ref(false);
const loadFailed = ref(false);
const saveFailed = ref(false);
const reasonError = ref(false);

const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.catalog.packageDetail.index.${key}` as App.I18n.I18nKey, values)
    : $t(`page.catalog.packageDetail.index.${key}` as App.I18n.I18nKey);
const reasonLabel = (value: string | null | undefined) =>
  value ? $t(`page.system.seo.reasons.${value}` as App.I18n.I18nKey) : '';
const dirty = computed(
  () =>
    Boolean(status.value) &&
    (override.value !== status.value?.override ||
      (override.value !== 'auto' && reason.value.trim() !== (status.value?.overrideReason ?? '')))
);
const languages = computed(() => status.value?.locales.map(code => LOCALE_METADATA[code].name).join(' · ') || '');

function apply(value: Status) {
  status.value = value;
  override.value = value.override;
  reason.value = value.overrideReason ?? '';
  reasonError.value = false;
}

async function load() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const { data, error } = await fetchPackageIndexStatus(props.packageId);
    if (error) loadFailed.value = true;
    else apply(data);
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (!dirty.value || saving.value) return;
  const trimmed = reason.value.trim();
  reasonError.value = override.value !== 'auto' && (!trimmed || [...trimmed].length > 500);
  if (reasonError.value) return;
  saving.value = true;
  saveFailed.value = false;
  try {
    const { data, error } = await updatePackageIndexStatus(
      props.packageId,
      override.value === 'auto' ? { override: 'auto' } : { override: override.value, reason: trimmed }
    );
    if (error) saveFailed.value = true;
    else {
      apply(data);
      window.$message?.success($t('page.shared.saved'));
    }
  } finally {
    saving.value = false;
  }
}

watch(
  () => props.packageId,
  () => {
    if (canIndex.value) void load();
  },
  { immediate: true }
);
</script>

<template>
  <div class="max-w-720px">
    <template v-if="canIndex">
      <NDivider title-placement="left" class="!mt-0">{{ text('sections.status') }}</NDivider>
      <NAlert v-if="loadFailed" type="error" class="mb-16px">{{ text('loadFailed') }}</NAlert>
      <NSkeleton v-if="loading && !status" text :repeat="4" />
      <template v-if="status">
        <NDescriptions :column="1" label-placement="left" bordered size="small" class="mb-20px">
          <NDescriptionsItem :label="text('verdict')">
            <NTag size="small" :type="status.eligible ? 'success' : 'warning'">
              {{ status.eligible ? text('eligible') : text('ineligible', { reason: reasonLabel(status.reason) }) }}
            </NTag>
          </NDescriptionsItem>
          <NDescriptionsItem :label="text('effect')">
            <span data-testid="index-effect">
              {{ status.enforced ? (status.indexable ? text('indexed') : text('noindexed')) : text('notEnforced') }}
            </span>
          </NDescriptionsItem>
          <NDescriptionsItem :label="text('locales')">{{ languages || text('none') }}</NDescriptionsItem>
        </NDescriptions>
        <NForm label-placement="top" :disabled="saving">
          <NFormItem :label="text('override')">
            <NRadioGroup v-model:value="override" :aria-label="text('override')">
              <NRadioButton v-for="value in overrides" :key="value" :value="value">
                {{ text(`overrides.${value}`) }}
              </NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <NFormItem
            v-if="override !== 'auto'"
            :label="text('reason')"
            :validation-status="reasonError ? 'error' : undefined"
            :feedback="reasonError ? text('reasonRequired') : undefined"
          >
            <NInput
              v-model:value="reason"
              type="textarea"
              :maxlength="500"
              show-count
              :autosize="{ minRows: 2, maxRows: 5 }"
              :placeholder="text('reasonPlaceholder')"
              :input-props="{ 'aria-label': text('reason') }"
            />
          </NFormItem>
          <NText depth="3" class="mb-16px block">{{ text('overrideHint') }}</NText>
          <NAlert v-if="saveFailed" type="error" class="mb-16px">{{ text('saveFailed') }}</NAlert>
          <div class="flex flex-wrap items-center gap-12px">
            <NButton type="primary" :loading="saving" :disabled="!dirty" @click="save">{{ text('save') }}</NButton>
            <NText v-if="status.overrideAt" depth="3">
              {{ text('overriddenAt', { time: formatDateTime(status.overrideAt) }) }}
            </NText>
          </div>
        </NForm>
      </template>
    </template>
    <template v-if="canView">
      <NDivider title-placement="left" :class="{ '!mt-0': !canIndex }">{{ text('sections.intent') }}</NDivider>
      <IntentPages :package-id="packageId" :editable="canEdit" />
      <NDivider title-placement="left">{{ text('sections.snippet') }}</NDivider>
      <SeoOverride :package-id="packageId" :editable="canEdit" />
    </template>
  </div>
</template>
