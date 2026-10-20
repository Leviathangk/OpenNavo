<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { NAlert, NButton, NCheckbox, NFormItem, NInput, NSelect, NText } from 'naive-ui';
import { captureArticleEvidence, createArticleTestArtifact } from '@/service/api';
import type { Schemas } from '@/typings/api/opennavo';
import { isDryRun } from '@/utils/opennavo';
import { $t } from '@/locales';

const props = defineProps<{
  articleId: number;
  claim: Schemas['ArticleClaim'];
  canPublish: boolean;
  prepare: () => Promise<boolean>;
}>();
const emit = defineEmits<{ change: [value: Partial<Schemas['ArticleClaim']>] }>();
const text = (key: string) => $t(`page.content.articleDetail.${key}` as App.I18n.I18nKey);
const url = ref('');
const busy = ref('');
const error = ref('');
const snapshot = ref<Schemas['SeoEvidence'] | null>(null);
const reportOpen = ref(false);
const report = reactive({
  macOS: '',
  architecture: 'arm64' as Schemas['ArticleTestArtifactInput']['architecture'],
  performedAt: '',
  runner: '',
  command: '',
  environment: '',
  output: '',
  attested: false
});
const canCapture = computed(() => /^https:\/\/\S+$/.test(url.value.trim()) && Boolean(props.claim.excerpt.trim()));
const canRegister = computed(
  () =>
    props.canPublish &&
    props.claim.packageId &&
    props.claim.appVersion.trim() &&
    props.claim.text.trim() &&
    /^[A-Z]{2}$/.test(props.claim.region) &&
    report.macOS.trim() &&
    report.runner.trim() &&
    report.command.trim() &&
    report.environment.trim() &&
    report.output.trim() &&
    Number.isFinite(Date.parse(report.performedAt)) &&
    report.attested
);

async function capture() {
  if (busy.value || !canCapture.value) return;
  busy.value = 'capture';
  error.value = '';
  // Keep the submitted scope stable while the network request is in flight.
  const claim = { ...props.claim };
  try {
    if (!(await props.prepare())) return;
    const { data, error: failure } = await captureArticleEvidence(props.articleId, {
      url: url.value.trim(),
      excerpt: claim.excerpt,
      packageId: claim.packageId ?? null,
      idempotencyKey: crypto.randomUUID()
    });
    if (failure || !data) {
      error.value = text('captureFailed');
      return;
    }
    if (props.claim.packageId !== claim.packageId || props.claim.excerpt !== claim.excerpt) {
      error.value = text('scopeChanged');
      return;
    }
    snapshot.value = data;
    emit('change', { evidenceId: data.id, excerpt: data.excerpt });
  } finally {
    busy.value = '';
  }
}
async function register() {
  if (busy.value || !canRegister.value || !props.claim.packageId) return;
  busy.value = 'report';
  error.value = '';
  const claim = { ...props.claim };
  try {
    if (!(await props.prepare())) return;
    const { data, error: failure } = await createArticleTestArtifact(props.articleId, {
      packageId: claim.packageId!,
      appVersion: claim.appVersion,
      region: claim.region,
      macOS: report.macOS.trim(),
      architecture: report.architecture,
      performedAt: new Date(report.performedAt).toISOString(),
      runner: report.runner.trim(),
      command: report.command.trim(),
      environment: { description: report.environment.trim() },
      output: report.output,
      conclusions: [claim.text],
      attested: true
    });
    if (failure || !data || isDryRun(data)) {
      error.value = text('reportFailed');
      return;
    }
    if (
      props.claim.packageId !== claim.packageId ||
      props.claim.appVersion !== claim.appVersion ||
      props.claim.region !== claim.region ||
      props.claim.text !== claim.text
    ) {
      error.value = text('scopeChanged');
      return;
    }
    emit('change', { testArtifactId: data.id });
    reportOpen.value = false;
  } finally {
    busy.value = '';
  }
}
</script>

<template>
  <div class="mb-16px flex-col gap-8px">
    <NFormItem :label="text('sourceUrl')">
      <NInput
        v-model:value="url"
        :disabled="Boolean(busy)"
        :maxlength="2048"
        placeholder="https://"
        :input-props="{ 'aria-label': text('sourceUrl') }"
      />
    </NFormItem>
    <NButton :disabled="!canCapture || Boolean(busy)" :loading="busy === 'capture'" @click="capture">
      {{ text('captureSource') }}
    </NButton>
    <NText v-if="snapshot" depth="3">
      {{ text('capturedSource') }}:
      <a :href="snapshot.finalUrl" target="_blank" rel="noopener noreferrer">{{ snapshot.finalUrl }}</a>
    </NText>
    <NButton v-if="canPublish" :disabled="Boolean(busy)" @click="reportOpen = !reportOpen">
      {{ text('registerReport') }}
    </NButton>
    <NText v-else depth="3">{{ text('reportPermission') }}</NText>
    <fieldset v-if="reportOpen" class="min-w-0 border-0 p-0">
      <legend class="mb-8px">{{ text('registerReport') }}</legend>
      <NText depth="3" class="mb-12px block">{{ text('reportHint') }}</NText>
      <div class="grid grid-cols-1 gap-x-16px md:grid-cols-2">
        <NFormItem :label="text('macOS')">
          <NInput v-model:value="report.macOS" :maxlength="100" :input-props="{ 'aria-label': text('macOS') }" />
        </NFormItem>
        <NFormItem :label="text('architecture')">
          <NSelect
            v-model:value="report.architecture"
            :options="['arm64', 'x86_64', 'universal'].map(value => ({ label: value, value }))"
          />
        </NFormItem>
        <NFormItem :label="text('performedAt')">
          <NInput
            v-model:value="report.performedAt"
            :input-props="{ type: 'datetime-local', 'aria-label': text('performedAt') }"
          />
        </NFormItem>
        <NFormItem :label="text('runner')">
          <NInput v-model:value="report.runner" :maxlength="200" :input-props="{ 'aria-label': text('runner') }" />
        </NFormItem>
      </div>
      <NFormItem :label="text('testEnvironment')">
        <NInput
          v-model:value="report.environment"
          type="textarea"
          :maxlength="2000"
          :input-props="{ 'aria-label': text('testEnvironment') }"
        />
      </NFormItem>
      <NFormItem :label="text('testCommand')">
        <NInput v-model:value="report.command" :maxlength="4096" :input-props="{ 'aria-label': text('testCommand') }" />
      </NFormItem>
      <NFormItem :label="text('testOutput')">
        <NInput
          v-model:value="report.output"
          type="textarea"
          :maxlength="262144"
          :autosize="{ minRows: 4, maxRows: 12 }"
          :input-props="{ 'aria-label': text('testOutput') }"
        />
      </NFormItem>
      <NCheckbox v-model:checked="report.attested" class="mb-12px">{{ text('attestReport') }}</NCheckbox>
      <NButton :disabled="!canRegister || Boolean(busy)" :loading="busy === 'report'" @click="register">
        {{ text('saveReport') }}
      </NButton>
    </fieldset>
    <NAlert v-if="error" type="error" role="alert">{{ error }}</NAlert>
  </div>
</template>
