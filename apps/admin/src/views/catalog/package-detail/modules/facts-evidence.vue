<script setup lang="ts">
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';
import type { EvidenceForm } from './facts';

defineOptions({ name: 'FactsEvidence' });

// Evidence fields shared by the price and the macOS supplement: where and how the fact was checked.
defineProps<{ disabled?: boolean }>();
const model = defineModel<EvidenceForm>({ required: true });

const methods: Schemas['FactMethod'][] = ['vendor_site', 'app_store', 'vendor_docs', 'in_app', 'other'];
const statuses: Schemas['FactStatus'][] = ['verified', 'needs_review'];
const text = (key: string) => $t(`page.catalog.packageDetail.facts.${key}` as App.I18n.I18nKey);
</script>

<template>
  <div class="grid grid-cols-1 items-start gap-x-16px md:grid-cols-2">
    <NFormItem :label="text('sourceUrl')" class="md:col-span-2">
      <NInput
        v-model:value="model.sourceUrl"
        :disabled="disabled"
        :maxlength="2048"
        :placeholder="text('sourceUrlPlaceholder')"
        :input-props="{ 'aria-label': text('sourceUrl'), inputmode: 'url' }"
      />
    </NFormItem>
    <NFormItem :label="text('method')">
      <NSelect
        v-model:value="model.method"
        :disabled="disabled"
        :options="methods.map(value => ({ value, label: text(`methods.${value}`) }))"
        :aria-label="text('method')"
      />
    </NFormItem>
    <NFormItem :label="text('verifiedAt')">
      <NDatePicker
        v-model:value="model.verifiedAt"
        type="datetime"
        clearable
        :disabled="disabled"
        :placeholder="text('verifiedAtHint')"
        class="w-full"
      />
    </NFormItem>
    <NFormItem :label="text('appVersion')">
      <NInput
        v-model:value="model.appVersion"
        :disabled="disabled"
        :maxlength="64"
        :input-props="{ 'aria-label': text('appVersion') }"
      />
    </NFormItem>
    <NFormItem :label="text('status')">
      <NRadioGroup v-model:value="model.status" :disabled="disabled" :aria-label="text('status')">
        <NRadio v-for="value in statuses" :key="value" :value="value">{{ text(`statuses.${value}`) }}</NRadio>
      </NRadioGroup>
    </NFormItem>
    <NFormItem :label="text('note')" class="md:col-span-2">
      <NInput
        v-model:value="model.note"
        type="textarea"
        :disabled="disabled"
        :maxlength="1000"
        show-count
        :autosize="{ minRows: 1, maxRows: 4 }"
        :placeholder="text('notePlaceholder')"
        :input-props="{ 'aria-label': text('note') }"
      />
    </NFormItem>
  </div>
</template>
