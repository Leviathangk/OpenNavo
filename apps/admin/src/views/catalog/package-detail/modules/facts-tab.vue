<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { SelectOption } from 'naive-ui';
import {
  fetchPackageFacts,
  updatePackageAlternatives,
  updatePackageMacosSupport,
  updatePackagePrice
} from '@/service/api';
import type { RecordOf } from '@/typings/api/opennavo';
import { useAuth } from '@/hooks/business/auth';
import { useLocalizedPackage } from '@/hooks/business/localized';
import { $t } from '@/locales';
import { formatDateTime } from '@/utils/opennavo';
import FactsEvidence from './facts-evidence.vue';
import {
  AMOUNT_TYPES,
  ARCHS,
  AUDIENCE,
  LIMITS,
  MAX_ALTERNATIVES,
  REASONS,
  alternativesBody,
  alternativesValid,
  gapKey,
  macosBody,
  macosForm,
  macosProblem,
  priceBody,
  priceForm,
  priceProblem
} from './facts';
import type { AlternativeRow, Facts, MacosForm, PriceForm, PriceType } from './facts';

defineOptions({ name: 'FactsTab' });

// Sourced facts for one app (WP12): price type, a macOS supplement beside Homebrew's requirements and
// confirmed alternatives. Each save records a package revision that can be rolled back.
const props = defineProps<{ packageId: number }>();

const { hasAuth } = useAuth();
const { nameOf } = useLocalizedPackage();
const editable = computed(() => hasAuth('catalog:package:edit'));
const facts = ref<Facts>();
const price = ref<PriceForm>(priceForm(null));
const macos = ref<MacosForm>(macosForm(null));
const alternatives = ref<AlternativeRow[]>([]);
const loading = ref(false);
const loadFailed = ref(false);
const saving = ref<'' | 'price' | 'macos' | 'alternatives'>('');
const problems = ref<Record<string, string>>({});
const alternativesDisabled = computed(() => !editable.value || saving.value === 'alternatives');

const text = (key: string, values?: Record<string, unknown>) =>
  values
    ? $t(`page.catalog.packageDetail.facts.${key}` as App.I18n.I18nKey, values)
    : $t(`page.catalog.packageDetail.facts.${key}` as App.I18n.I18nKey);
const priceTypes: PriceType[] = ['unknown', 'free', 'free_with_iap', 'paid', 'subscription', 'trial'];
const options = (keys: readonly string[], group: string): SelectOption[] =>
  keys.map(value => ({ value, label: text(`${group}.${value}`) }));
const showAmount = computed(() => AMOUNT_TYPES.includes(price.value.type));

const homebrewSummary = computed(() => {
  const hb = facts.value?.homebrew;
  if (!hb) return '';
  const parts: string[] = [];
  if (hb.minimum) parts.push(text('homebrewMinimum', { version: hb.minimum }));
  if (hb.maximum) parts.push(text('homebrewMaximum', { version: hb.maximum }));
  if (hb.supportStatus === 'known') {
    for (const arch of ARCHS) parts.push(`${text(`archs.${arch}`)} ${hb[arch] ? '✓' : '✗'}`);
  } else parts.push(text('homebrewUnknown'));
  if (hb.requiresRosetta) parts.push(text('requiresRosetta'));
  return parts.join(' · ');
});
const conflictText = computed(() =>
  facts.value?.conflicts.length
    ? text('conflicts', { fields: facts.value.conflicts.map(code => text(`conflictFields.${code}`)).join(', ') })
    : ''
);

function clearProblem(section: string) {
  problems.value = Object.fromEntries(Object.entries(problems.value).filter(([key]) => key !== section));
}

function apply(value: Facts, section?: 'price' | 'macos' | 'alternatives') {
  facts.value = value;
  if (!section || section === 'price') price.value = priceForm(value.price);
  if (!section || section === 'macos') macos.value = macosForm(value.macos);
  if (!section || section === 'alternatives')
    alternatives.value = value.alternatives.map(item => ({
      ...item,
      reasons: [...item.reasons],
      limits: [...item.limits]
    }));
  if (section) clearProblem(section);
  else problems.value = {};
}

async function load() {
  loading.value = true;
  loadFailed.value = false;
  try {
    const { data, error } = await fetchPackageFacts(props.packageId);
    if (error) loadFailed.value = true;
    else apply(data);
  } finally {
    loading.value = false;
  }
}

async function save(section: 'price' | 'macos' | 'alternatives', clear = false) {
  if (saving.value) return;
  let problem: string | null = null;
  if (section === 'price') problem = priceProblem(price.value);
  if (section === 'macos' && !clear) problem = macosProblem(macos.value);
  if (section === 'alternatives' && !alternativesValid(alternatives.value)) problem = 'reasonRequired';
  if (problem) problems.value[section] = problem;
  else clearProblem(section);
  if (problem) return;
  saving.value = section;
  try {
    const { data, error } =
      section === 'price'
        ? await updatePackagePrice(props.packageId, priceBody(price.value))
        : section === 'macos'
          ? await updatePackageMacosSupport(props.packageId, clear ? { ranges: [] } : macosBody(macos.value))
          : await updatePackageAlternatives(props.packageId, alternativesBody(alternatives.value));
    if (error) problems.value[section] = 'saveFailed';
    else {
      apply(data, section);
      window.$message?.success($t('page.shared.saved'));
    }
  } finally {
    saving.value = '';
  }
}

function verifiedLine(value: { verifiedAt: string; verifiedBy: string; verifier: 'admin' | 'agent' }) {
  return text('verified', {
    time: formatDateTime(value.verifiedAt),
    name: value.verifiedBy || '—',
    verifier: text(`verifiers.${value.verifier}`)
  });
}

function addAlternative(row: RecordOf<'listAdminPackages'>) {
  if (
    alternativesDisabled.value ||
    row.id === props.packageId ||
    alternatives.value.length >= MAX_ALTERNATIVES ||
    alternatives.value.some(item => item.packageId === row.id)
  )
    return;
  alternatives.value = [
    ...alternatives.value,
    {
      packageId: row.id,
      token: row.token,
      name: row.name,
      displayName: nameOf(row),
      iconUrl: row.iconUrl ?? null,
      reasons: [],
      limits: [],
      bestFor: null,
      note: null,
      source: 'admin',
      available: !row.hidden && !row.disabled && !row.removed
    }
  ];
}

function move(index: number, offset: number) {
  if (alternativesDisabled.value) return;
  const next = [...alternatives.value];
  const [item] = next.splice(index, 1);
  next.splice(index + offset, 0, item!);
  alternatives.value = next;
}

watch(() => props.packageId, load, { immediate: true });
</script>

<template>
  <div class="max-w-960px flex-col gap-16px">
    <NAlert v-if="loadFailed" type="error">{{ text('loadFailed') }}</NAlert>
    <NSkeleton v-if="loading && !facts" text :repeat="6" />
    <template v-if="facts">
      <div class="flex flex-wrap items-center gap-8px" data-testid="facts-gaps">
        <NText strong>{{ text('gapsTitle') }}</NText>
        <NTag v-if="!facts.gaps.length" size="small" type="success" :bordered="false">{{ text('noGaps') }}</NTag>
        <NTag v-for="code in facts.gaps" :key="code" size="small" type="warning" :bordered="false">
          {{ text(`gaps.${gapKey(code)}`) }}
        </NTag>
        <NText v-if="facts.updatedAt" depth="3" class="ml-auto text-12px">
          {{ text('updatedAt', { time: formatDateTime(facts.updatedAt) }) }}
        </NText>
      </div>

      <NCard size="small" :title="text('priceTitle')" data-testid="facts-price">
        <NText depth="3" class="mb-12px block text-13px">{{ text('priceHint') }}</NText>
        <div v-if="facts.price" class="mb-12px flex flex-wrap items-center gap-8px text-13px">
          <NText depth="3">{{ verifiedLine(facts.price) }}</NText>
          <NTag v-if="facts.price.stale" size="small" type="warning" :bordered="false">{{ text('stale') }}</NTag>
          <NTag size="small" :type="facts.offerReady ? 'success' : 'default'" :bordered="false">
            {{ facts.offerReady ? text('offerReady') : text('offerMissing') }}
          </NTag>
        </div>
        <NForm label-placement="top" :disabled="!editable || saving === 'price'">
          <NFormItem :label="text('type')">
            <NRadioGroup v-model:value="price.type" :aria-label="text('type')">
              <NRadioButton v-for="value in priceTypes" :key="value" :value="value">
                {{ text(`types.${value}`) }}
              </NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <template v-if="price.type !== 'unknown'">
            <div class="grid grid-cols-1 items-start gap-x-16px md:grid-cols-3">
              <template v-if="showAmount">
                <NFormItem :label="text('amount')">
                  <NInputNumber
                    v-model:value="price.amount"
                    :min="0.01"
                    :max="10000000"
                    :precision="2"
                    clearable
                    class="w-full"
                    :input-props="{ 'aria-label': text('amount') }"
                  />
                </NFormItem>
                <NFormItem :label="text('currency')">
                  <NInput
                    v-model:value="price.currency"
                    :maxlength="3"
                    :placeholder="text('currencyPlaceholder')"
                    :input-props="{ 'aria-label': text('currency') }"
                    @update:value="value => (price.currency = value.toUpperCase())"
                  />
                </NFormItem>
                <NFormItem :label="text('period')">
                  <NSelect
                    v-model:value="price.period"
                    clearable
                    :options="options(['one_time', 'month', 'year'], 'periods')"
                    :aria-label="text('period')"
                  />
                </NFormItem>
                <NFormItem :label="text('region')">
                  <NInput
                    v-model:value="price.region"
                    :maxlength="2"
                    :placeholder="text('regionPlaceholder')"
                    :input-props="{ 'aria-label': text('region') }"
                    @update:value="value => (price.region = value.toUpperCase())"
                  />
                </NFormItem>
              </template>
              <NFormItem :label="text('plan')" :class="showAmount ? 'md:col-span-2' : 'md:col-span-3'">
                <NInput
                  v-model:value="price.plan"
                  :maxlength="80"
                  :placeholder="text('planPlaceholder')"
                  :input-props="{ 'aria-label': text('plan') }"
                />
              </NFormItem>
            </div>
            <FactsEvidence v-model="price.evidence" />
          </template>
          <NAlert v-if="problems.price" type="error" class="mb-12px">{{ text(problems.price) }}</NAlert>
          <NButton v-if="editable" type="primary" :loading="saving === 'price'" @click="save('price')">
            {{ text('save') }}
          </NButton>
        </NForm>
      </NCard>

      <NCard size="small" :title="text('macosTitle')" data-testid="facts-macos">
        <NText depth="3" class="mb-12px block text-13px">{{ text('macosHint') }}</NText>
        <NDescriptions :column="1" label-placement="left" bordered size="small" class="mb-12px">
          <NDescriptionsItem :label="text('homebrew')">{{ homebrewSummary }}</NDescriptionsItem>
        </NDescriptions>
        <NAlert v-if="conflictText" type="warning" class="mb-12px" data-testid="facts-conflicts">
          {{ conflictText }}
        </NAlert>
        <div v-if="facts.macos" class="mb-12px flex flex-wrap items-center gap-8px text-13px">
          <NText depth="3">{{ verifiedLine(facts.macos) }}</NText>
          <NTag v-if="facts.macos.stale" size="small" type="warning" :bordered="false">{{ text('stale') }}</NTag>
        </div>
        <NForm label-placement="top" :disabled="!editable || saving === 'macos'">
          <div v-for="arch in ARCHS" :key="arch" class="mb-8px grid grid-cols-1 items-start gap-x-16px md:grid-cols-4">
            <NFormItem :label="text(`archs.${arch}`)">
              <NCheckbox v-model:checked="macos.ranges[arch].include">{{ text('include') }}</NCheckbox>
            </NFormItem>
            <template v-if="macos.ranges[arch].include">
              <NFormItem :label="text('support')">
                <NSelect
                  v-model:value="macos.ranges[arch].support"
                  :options="
                    options(
                      arch === 'arm64' ? ['native', 'rosetta', 'unsupported'] : ['native', 'unsupported'],
                      'supports'
                    )
                  "
                  :aria-label="`${text(`archs.${arch}`)} ${text('support')}`"
                />
              </NFormItem>
              <template v-if="macos.ranges[arch].support !== 'unsupported'">
                <NFormItem :label="text('minimum')">
                  <NInput
                    v-model:value="macos.ranges[arch].minimum"
                    :maxlength="8"
                    :placeholder="text('versionPlaceholder')"
                    :input-props="{ 'aria-label': `${text(`archs.${arch}`)} ${text('minimum')}` }"
                  />
                </NFormItem>
                <NFormItem :label="text('maximum')">
                  <NInput
                    v-model:value="macos.ranges[arch].maximum"
                    :maxlength="8"
                    :placeholder="text('versionPlaceholder')"
                    :input-props="{ 'aria-label': `${text(`archs.${arch}`)} ${text('maximum')}` }"
                  />
                </NFormItem>
              </template>
            </template>
          </div>
          <FactsEvidence v-model="macos.evidence" />
          <NAlert v-if="problems.macos" type="error" class="mb-12px">{{ text(problems.macos) }}</NAlert>
          <div v-if="editable" class="flex flex-wrap gap-12px">
            <NButton type="primary" :loading="saving === 'macos'" @click="save('macos')">{{ text('save') }}</NButton>
            <NButton v-if="facts.macos" :disabled="Boolean(saving)" @click="save('macos', true)">
              {{ text('clear') }}
            </NButton>
          </div>
        </NForm>
      </NCard>

      <NCard size="small" :title="text('alternativesTitle')" data-testid="facts-alternatives">
        <NText depth="3" class="mb-12px block text-13px">{{ text('alternativesHint') }}</NText>
        <NEmpty v-if="!alternatives.length" :description="text('empty')" class="mb-12px" />
        <NList v-else bordered class="mb-12px">
          <NListItem v-for="(item, index) in alternatives" :key="item.packageId">
            <div class="flex-col gap-8px">
              <div class="flex flex-wrap items-center gap-8px">
                <NAvatar v-if="item.iconUrl" :src="item.iconUrl" :size="24" color="transparent" />
                <span class="font-500">{{ item.displayName }}</span>
                <NText depth="3" class="text-12px">{{ item.token }}</NText>
                <NTag v-if="!item.available" size="small" type="warning" :bordered="false">{{ text('hidden') }}</NTag>
                <NTag size="small" :bordered="false">{{ text(`verifiers.${item.source}`) }}</NTag>
                <div v-if="editable" class="ml-auto flex gap-4px">
                  <NButton
                    size="tiny"
                    quaternary
                    :disabled="alternativesDisabled || index === 0"
                    @click="move(index, -1)"
                  >
                    {{ text('moveUp') }}
                  </NButton>
                  <NButton
                    size="tiny"
                    quaternary
                    :disabled="alternativesDisabled || index === alternatives.length - 1"
                    @click="move(index, 1)"
                  >
                    {{ text('moveDown') }}
                  </NButton>
                  <NButton
                    size="tiny"
                    quaternary
                    type="error"
                    :disabled="alternativesDisabled"
                    @click="alternatives.splice(index, 1)"
                  >
                    {{ $t('common.delete') }}
                  </NButton>
                </div>
              </div>
              <div class="grid grid-cols-1 items-start gap-x-12px gap-y-8px md:grid-cols-3">
                <NSelect
                  v-model:value="item.reasons"
                  multiple
                  :disabled="alternativesDisabled"
                  :options="options(REASONS, 'reasonCodes')"
                  :placeholder="text('reasons')"
                  :aria-label="text('reasons')"
                  @update:value="(value: string[]) => (item.reasons = value.slice(0, 4) as typeof item.reasons)"
                />
                <NSelect
                  v-model:value="item.limits"
                  multiple
                  :disabled="alternativesDisabled"
                  :options="options(LIMITS, 'limitCodes')"
                  :placeholder="text('limits')"
                  :aria-label="text('limits')"
                  @update:value="(value: string[]) => (item.limits = value.slice(0, 3) as typeof item.limits)"
                />
                <NSelect
                  v-model:value="item.bestFor"
                  clearable
                  :disabled="alternativesDisabled"
                  :options="options(AUDIENCE, 'audience')"
                  :placeholder="text('bestFor')"
                  :aria-label="text('bestFor')"
                />
                <NInput
                  v-model:value="item.note"
                  class="md:col-span-3"
                  :disabled="alternativesDisabled"
                  :maxlength="500"
                  :placeholder="`${text('note')} · ${text('notePlaceholder')}`"
                  :input-props="{ 'aria-label': text('note') }"
                />
              </div>
            </div>
          </NListItem>
        </NList>
        <NAlert v-if="problems.alternatives" type="error" class="mb-12px">{{ text(problems.alternatives) }}</NAlert>
        <div v-if="editable" class="flex flex-wrap items-center gap-12px">
          <PackagePicker
            class="max-w-360px"
            :disabled="alternativesDisabled || alternatives.length >= MAX_ALTERNATIVES"
            :placeholder="alternatives.length >= MAX_ALTERNATIVES ? text('max') : text('add')"
            @pick="addAlternative"
          />
          <NButton type="primary" :loading="saving === 'alternatives'" class="ml-auto" @click="save('alternatives')">
            {{ text('save') }}
          </NButton>
        </div>
      </NCard>
    </template>
  </div>
</template>
