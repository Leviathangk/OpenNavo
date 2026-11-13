<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnIcon } from '@opennavo/ui';
import {
  fetchDeveloperMetadata,
  submitDeveloperSubmission,
  type DeveloperSubmitResult
} from '@/composables/useDeveloperMap';

const emit = defineEmits<{ submitted: [token: string] }>();
const { t } = useI18n();

const link = ref('');
const source = ref<'github' | 'appstore' | 'website'>('github');
const fetching = ref(false);
const fetchError = ref('');
const warnings = ref<string[]>([]);
const showDetails = ref(false);
const submitting = ref(false);
const submitError = ref('');
const submitted = ref<DeveloperSubmitResult | null>(null);

const form = reactive({
  name: '',
  summary: '',
  developer: '',
  homepage: '',
  repoUrl: '',
  downloadUrl: '',
  iconUrl: '',
  version: '',
  category: '',
  countryCode: 'US',
  city: '',
  remote: false,
  contactEmail: ''
});

const sources = computed(() => [
  { key: 'github' as const, label: t('developerForm.sourceGithub') },
  { key: 'appstore' as const, label: t('developerForm.sourceAppStore') },
  { key: 'website' as const, label: t('developerForm.sourceWebsite') }
]);

const countries = [
  'US',
  'CA',
  'GB',
  'DE',
  'FR',
  'NL',
  'SE',
  'IT',
  'AT',
  'CZ',
  'UA',
  'CN',
  'JP',
  'KR',
  'IN',
  'AU',
  'IL'
];
const { locale } = useI18n();
const regionNames = computed(() => new Intl.DisplayNames([locale.value], { type: 'region' }));
function countryName(code: string) {
  try {
    return regionNames.value.of(code) ?? code;
  } catch {
    return code;
  }
}

const emailValid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.contactEmail));
const canSubmit = computed(() =>
  Boolean(form.name.trim() && form.summary.trim() && form.developer.trim() && emailValid.value)
);

async function fetchInfo() {
  const value = link.value.trim();
  if (!value) return;
  fetching.value = true;
  fetchError.value = '';
  warnings.value = [];
  try {
    const data = await fetchDeveloperMetadata(value);
    form.name = data.name || form.name;
    form.summary = data.summary || form.summary;
    form.developer = data.developer || form.developer;
    form.homepage = data.homepage || value;
    form.repoUrl = data.repoUrl || form.repoUrl;
    form.downloadUrl = data.downloadUrl || form.downloadUrl;
    form.iconUrl = data.iconUrl || form.iconUrl;
    form.version = data.version || form.version;
    form.category = data.category || form.category;
    warnings.value = data.warnings ?? [];
    showDetails.value = true;
  } catch {
    fetchError.value = t('developerForm.fetchFailed');
    showDetails.value = true;
  } finally {
    fetching.value = false;
  }
}

function manual() {
  fetchError.value = '';
  showDetails.value = true;
}

async function submit() {
  submitError.value = '';
  if (!canSubmit.value) {
    submitError.value = emailValid.value ? t('developerForm.required') : t('developerForm.invalidEmail');
    return;
  }
  submitting.value = true;
  try {
    const result = await submitDeveloperSubmission({
      kind: 'new',
      sourceKind: source.value,
      sourceUrl: link.value.trim(),
      contactEmail: form.contactEmail.trim(),
      payload: {
        name: form.name.trim(),
        summary: form.summary.trim(),
        developer: form.developer.trim(),
        category: form.category.trim(),
        homepage: form.homepage.trim(),
        repoUrl: form.repoUrl.trim(),
        downloadUrl: form.downloadUrl.trim(),
        iconUrl: form.iconUrl.trim(),
        version: form.version.trim(),
        countryCode: form.countryCode,
        city: form.city.trim(),
        remote: form.remote
      }
    });
    submitted.value = result;
    emit('submitted', result.token);
  } catch (error) {
    submitError.value = error instanceof Error ? error.message : t('developerForm.required');
  } finally {
    submitting.value = false;
  }
}

function reset() {
  submitted.value = null;
  showDetails.value = false;
  link.value = '';
  Object.assign(form, {
    name: '',
    summary: '',
    developer: '',
    homepage: '',
    repoUrl: '',
    downloadUrl: '',
    iconUrl: '',
    version: '',
    category: '',
    countryCode: 'US',
    city: '',
    remote: false,
    contactEmail: ''
  });
}
</script>

<template>
  <section v-if="submitted" class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
    <span class="grid h-32px w-32px place-items-center rounded-full bg-status-success-subtle text-status-success">
      <OnIcon name="check" :size="17" />
    </span>
    <h2 class="m-0 mt-9px text-16px font-600 text-ink-primary">{{ t('developerForm.successTitle') }}</h2>
    <p class="m-0 mt-4px max-w-680px text-12px leading-[1.7] text-ink-secondary">
      {{ t('developerForm.successBody') }}
    </p>
    <p class="m-0 mt-7px text-11px text-ink-tertiary">
      {{ t('developerForm.reference') }}: <b class="font-600 text-ink-primary">{{ submitted.reference }}</b> ·
      {{ t('developerForm.statusToken') }}: <b class="font-500 break-all text-ink-secondary">{{ submitted.token }}</b>
    </p>
    <div class="mt-12px flex flex-wrap gap-7px">
      <button
        type="button"
        class="h-28px rounded-default bg-brand-coral px-11px text-11.5px font-600 text-ink-inverse"
        @click="reset"
      >
        {{ t('developerForm.again') }}
      </button>
    </div>
  </section>

  <div v-else class="flex flex-col gap-12px">
    <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
      <h2 class="m-0 mt-7px text-16px font-600 text-ink-primary">{{ t('developerForm.linkTitle') }}</h2>
      <p class="m-0 mt-3px max-w-700px text-12px leading-[1.7] text-ink-secondary">{{ t('developerForm.linkHint') }}</p>
      <div class="mt-10px flex w-fit gap-2px rounded-[8px] bg-surface-inset p-2px">
        <button
          v-for="item in sources"
          :key="item.key"
          type="button"
          class="h-24px rounded-[6px] px-9px text-11px font-500"
          :class="
            source === item.key
              ? 'bg-surface-card text-ink-primary shadow-[0_1px_2px_rgba(0,0,0,0.18)]'
              : 'text-ink-secondary hover:text-ink-primary'
          "
          @click="source = item.key"
        >
          {{ item.label }}
        </button>
      </div>
      <div class="mt-9px flex flex-col gap-7px sm:flex-row">
        <label class="relative min-w-0 flex-1">
          <OnIcon name="globe" :size="13" class="absolute left-9px top-1/2 -translate-y-1/2 text-ink-tertiary" />
          <input
            v-model="link"
            type="url"
            :placeholder="t('developerForm.linkPlaceholder')"
            class="dev-input pl-28px"
            @keydown.enter.prevent="fetchInfo"
          />
        </label>
        <button
          type="button"
          class="h-32px shrink-0 rounded-default bg-brand-coral px-12px text-11.5px font-600 text-ink-inverse disabled:opacity-60"
          :disabled="fetching"
          @click="fetchInfo"
        >
          {{ fetching ? t('developerForm.fetching') : t('developerForm.fetch') }}
        </button>
      </div>
      <p
        v-if="fetchError"
        class="m-0 mt-7px rounded-default bg-status-danger-subtle px-8px py-6px text-11px text-status-danger"
      >
        {{ fetchError }}
        <button type="button" class="ml-4px font-600 underline" @click="manual">{{ t('developerForm.manual') }}</button>
      </p>
      <p v-for="warning in warnings" :key="warning" class="m-0 mt-4px text-10px text-ink-tertiary">· {{ warning }}</p>
    </section>

    <template v-if="showDetails">
      <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
        <h2 class="m-0 text-14px font-600 text-ink-primary">{{ t('developerForm.details') }}</h2>
        <div class="mt-9px grid grid-cols-1 gap-8px sm:grid-cols-2">
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.name') }} *</span
            ><input v-model="form.name" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.developer') }} *</span
            ><input v-model="form.developer" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px sm:col-span-2"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.summary') }} *</span
            ><input v-model="form.summary" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.category') }}</span
            ><input v-model="form.category" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.version') }}</span
            ><input v-model="form.version" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.homepage') }}</span
            ><input v-model="form.homepage" type="url" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.repo') }}</span
            ><input v-model="form.repoUrl" type="url" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.download') }}</span
            ><input v-model="form.downloadUrl" type="url" class="dev-input"
          /></label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.icon') }}</span
            ><input v-model="form.iconUrl" type="url" class="dev-input"
          /></label>
        </div>

        <h3 class="m-0 mt-14px border-t border-solid border-line-subtle pt-11px text-12.5px font-600 text-ink-primary">
          {{ t('developerForm.location') }}
        </h3>
        <div class="mt-8px grid grid-cols-1 gap-8px sm:grid-cols-2">
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.country') }} *</span>
            <select v-model="form.countryCode" class="dev-input">
              <option v-for="code in countries" :key="code" :value="code">{{ countryName(code) }}</option>
            </select>
          </label>
          <label class="flex flex-col gap-4px"
            ><span class="text-11px text-ink-secondary">{{ t('developerForm.city') }}</span
            ><input v-model="form.city" class="dev-input"
          /></label>
          <label class="flex items-center gap-6px text-11px text-ink-secondary sm:col-span-2"
            ><input v-model="form.remote" type="checkbox" /> {{ t('developerForm.remote') }}</label
          >
        </div>

        <h3 class="m-0 mt-14px border-t border-solid border-line-subtle pt-11px text-12.5px font-600 text-ink-primary">
          {{ t('developerForm.contact') }}
        </h3>
        <input
          v-model="form.contactEmail"
          type="email"
          :placeholder="t('developerForm.contactPlaceholder')"
          class="dev-input mt-7px"
        />
        <p class="m-0 mt-4px text-10px text-ink-tertiary">{{ t('developerForm.contactHint') }}</p>

        <p
          v-if="submitError"
          class="m-0 mt-9px rounded-default bg-status-danger-subtle px-8px py-6px text-11px text-status-danger"
        >
          {{ submitError }}
        </p>
        <button
          type="button"
          class="mt-11px h-30px rounded-default bg-brand-coral px-13px text-11.5px font-600 text-ink-inverse disabled:opacity-60"
          :disabled="submitting"
          @click="submit"
        >
          {{ submitting ? t('developerForm.submitting') : t('developerForm.submit') }}
        </button>
      </section>
    </template>
  </div>
</template>

<style scoped>
.dev-input {
  box-sizing: border-box;
  width: 100%;
  height: 32px;
  padding: 0 9px;
  border: 1px solid var(--on-border-default);
  border-radius: var(--on-radius-default);
  background: var(--on-surface-inset);
  color: var(--on-text-primary);
  font: inherit;
  font-size: 12px;
  outline: none;
}
.dev-input:focus {
  border-color: var(--on-border-strong);
  box-shadow: var(--on-shadow-focus-ring);
}
</style>
