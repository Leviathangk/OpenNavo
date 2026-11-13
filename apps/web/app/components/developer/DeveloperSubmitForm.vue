<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { OnAppIcon, OnIcon } from '@opennavo/ui';
import {
  fetchDeveloperMetadata,
  submitDeveloperSubmission,
  type DeveloperSubmitResult
} from '~/composables/useDeveloperMap';

const { t, locale } = useI18n();
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const source = ref<'github' | 'appstore' | 'website'>('github');
const link = ref('https://github.com/ghostty-org/ghostty');
const fetching = ref(false);
const showDetails = ref(false);
const fetchError = ref('');
const warnings = ref<string[]>([]);
const submitting = ref(false);
const submitError = ref('');
const submitted = ref<DeveloperSubmitResult | null>(null);

const form = reactive({
  name: '',
  summary: '',
  description: '',
  developer: '',
  category: '',
  homepage: '',
  repoUrl: '',
  downloadUrl: '',
  iconUrl: '',
  version: '',
  minMacos: '',
  license: '',
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

const placeholder = computed(() =>
  source.value === 'github'
    ? 'https://github.com/owner/app'
    : source.value === 'appstore'
      ? 'https://apps.apple.com/app/id123456789'
      : 'https://example.com'
);

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
const regionNames = computed(() => new Intl.DisplayNames([locale.value], { type: 'region' }));
const countryName = (code: string) => {
  try {
    return regionNames.value.of(code) ?? code;
  } catch {
    return code;
  }
};

const previewLocation = computed(() => [form.city, countryName(form.countryCode)].filter(Boolean).join(' · '));
const iconToken = computed(
  () =>
    form.name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-|-$/g, '') || 'app'
);
const emailValid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.contactEmail));
const canSubmit = computed(() =>
  Boolean(form.name.trim() && form.summary.trim() && form.developer.trim() && emailValid.value)
);

async function fetchInfo() {
  fetchError.value = '';
  warnings.value = [];
  if (!link.value.trim()) {
    fetchError.value = t('developerForm.fetchFailed');
    return;
  }
  fetching.value = true;
  try {
    const data = await fetchDeveloperMetadata(link.value.trim());
    form.name = data.name || form.name;
    form.summary = data.summary || form.summary;
    form.description = data.description || form.description;
    form.developer = data.developer || form.developer;
    form.category = data.category || form.category;
    form.homepage = data.homepage || form.homepage;
    form.repoUrl = data.repoUrl || form.repoUrl;
    form.downloadUrl = data.downloadUrl || form.downloadUrl;
    form.iconUrl = data.iconUrl || form.iconUrl;
    form.version = data.version || form.version;
    form.minMacos = data.minMacos || form.minMacos;
    form.license = data.license || form.license;
    if (data.source === 'github' || data.source === 'appstore' || data.source === 'website') source.value = data.source;
    warnings.value = data.warnings ?? [];
    showDetails.value = true;
  } catch (error) {
    fetchError.value = error instanceof Error ? error.message : t('developerForm.fetchFailed');
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
        description: form.description.trim(),
        developer: form.developer.trim(),
        category: form.category.trim(),
        homepage: form.homepage.trim(),
        repoUrl: form.repoUrl.trim(),
        downloadUrl: form.downloadUrl.trim(),
        iconUrl: form.iconUrl.trim(),
        version: form.version.trim(),
        minMacos: form.minMacos.trim(),
        license: form.license.trim(),
        countryCode: form.countryCode,
        city: form.city.trim(),
        remote: form.remote
      }
    });
    submitted.value = result;
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
    description: '',
    developer: '',
    category: '',
    homepage: '',
    repoUrl: '',
    downloadUrl: '',
    iconUrl: '',
    version: '',
    minMacos: '',
    license: '',
    countryCode: 'US',
    city: '',
    remote: false,
    contactEmail: ''
  });
}
</script>

<template>
  <section v-if="submitted" class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
    <span class="grid h-34px w-34px place-items-center rounded-full bg-status-success-subtle text-status-success">
      <OnIcon name="check" :size="18" />
    </span>
    <h2 class="m-0 mt-10px text-18px font-600 text-ink-primary">{{ t('developerForm.successTitle') }}</h2>
    <p class="m-0 mt-5px max-w-680px text-12.5px leading-[1.7] text-ink-secondary">
      {{ t('developerForm.successBody') }}
    </p>
    <p v-if="submitted.reference" class="m-0 mt-8px text-11.5px text-ink-tertiary">
      {{ t('developerForm.reference') }}: <b class="font-600 text-ink-primary">{{ submitted.reference }}</b>
    </p>
    <p v-if="submitted.token" class="m-0 mt-5px max-w-680px break-all text-10.5px leading-[1.6] text-ink-tertiary">
      {{ t('developerForm.statusToken') }}: <b class="font-500 text-ink-secondary">{{ submitted.token }}</b>
    </p>
    <div class="mt-14px flex flex-wrap gap-8px">
      <NuxtLink
        v-if="submitted.token"
        :to="{ path: localePath('/developer-map'), query: { tab: 'submit', submission: submitted.token } }"
        class="inline-flex h-30px items-center rounded-default bg-brand-coral px-12px text-12px font-600 text-ink-inverse no-underline"
      >
        {{ t('developerForm.viewStatus') }}
      </NuxtLink>
      <button
        type="button"
        class="h-30px rounded-default border border-solid border-line-subtle px-12px text-12px font-500 text-ink-secondary hover:bg-surface-raised hover:text-ink-primary"
        @click="reset"
      >
        {{ t('developerForm.again') }}
      </button>
      <NuxtLink
        :to="localePath('/developer-map')"
        class="inline-flex h-30px items-center rounded-default border border-solid border-line-subtle px-12px text-12px font-500 text-ink-secondary no-underline hover:bg-surface-raised hover:text-ink-primary"
        >{{ t('developerForm.back') }}</NuxtLink
      >
    </div>
  </section>

  <div v-else class="flex flex-col gap-14px">
    <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
      <h2 class="m-0 mt-8px text-18px font-600 text-ink-primary">{{ t('developerForm.linkTitle') }}</h2>
      <p class="m-0 mt-4px max-w-700px text-12.5px leading-[1.7] text-ink-secondary">
        {{ t('developerForm.linkHint') }}
      </p>

      <div class="mt-12px flex w-fit gap-2px rounded-[8px] bg-surface-inset p-2px">
        <button
          v-for="item in sources"
          :key="item.key"
          type="button"
          class="h-26px rounded-[6px] px-10px text-11.5px font-500 outline-none"
          :class="
            source === item.key
              ? 'bg-surface-card text-ink-primary shadow-[0_1px_2px_rgba(0,0,0,0.16)]'
              : 'text-ink-secondary hover:text-ink-primary'
          "
          @click="source = item.key"
        >
          {{ item.label }}
        </button>
      </div>

      <div class="mt-10px flex flex-col gap-8px sm:flex-row">
        <label class="relative min-w-0 flex-1">
          <OnIcon name="globe" :size="14" class="absolute left-10px top-1/2 -translate-y-1/2 text-ink-tertiary" />
          <input
            v-model="link"
            type="url"
            :placeholder="placeholder"
            class="h-36px w-full rounded-default border border-solid border-line-default bg-surface-inset pl-31px pr-10px text-12.5px text-ink-primary outline-none placeholder:text-ink-tertiary focus:border-line-strong focus:shadow-focus-ring"
            @keydown.enter.prevent="fetchInfo"
          />
        </label>
        <button
          type="button"
          class="inline-flex h-36px shrink-0 items-center justify-center gap-5px rounded-default bg-brand-coral px-14px text-12px font-600 text-ink-inverse outline-none hover:opacity-90 disabled:cursor-wait disabled:opacity-60"
          :disabled="fetching"
          @click="fetchInfo"
        >
          {{ fetching ? t('developerForm.fetching') : t('developerForm.fetch') }}
        </button>
      </div>
      <p class="m-0 mt-7px text-10.5px leading-[1.6] text-ink-tertiary">{{ t('developerForm.linkHint') }}</p>
      <p
        v-if="fetchError"
        class="m-0 mt-8px rounded-default bg-status-danger-subtle px-9px py-7px text-11.5px text-status-danger"
      >
        {{ fetchError }}
        <button type="button" class="ml-5px font-600 underline" @click="manual">{{ t('developerForm.manual') }}</button>
      </p>
      <ul v-if="warnings.length" class="m-0 mt-8px flex list-none flex-col gap-3px p-0 text-10.5px text-ink-tertiary">
        <li v-for="warning in warnings" :key="warning">· {{ warning }}</li>
      </ul>
    </section>

    <template v-if="showDetails">
      <div class="grid grid-cols-1 items-start gap-12px lg:grid-cols-[minmax(0,1fr)_300px]">
        <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
          <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('developerForm.details') }}</h2>
          <div class="mt-12px grid grid-cols-1 gap-10px sm:grid-cols-2">
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.name') }} *</span
              ><input v-model="form.name" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.developer') }} *</span
              ><input v-model="form.developer" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px sm:col-span-2"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.summary') }} *</span
              ><input v-model="form.summary" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px sm:col-span-2"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.description') }}</span
              ><textarea v-model="form.description" rows="4" class="dev-input min-h-80px resize-y py-8px" />
            </label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.category') }}</span
              ><input v-model="form.category" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.version') }}</span
              ><input v-model="form.version" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.homepage') }}</span
              ><input v-model="form.homepage" type="url" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.repo') }}</span
              ><input v-model="form.repoUrl" type="url" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.download') }}</span
              ><input v-model="form.downloadUrl" type="url" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.icon') }}</span
              ><input v-model="form.iconUrl" type="url" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.minMacos') }}</span
              ><input v-model="form.minMacos" class="dev-input"
            /></label>
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.license') }}</span
              ><input v-model="form.license" class="dev-input"
            /></label>
          </div>

          <h3 class="m-0 mt-18px border-t border-solid border-line-subtle pt-14px text-13px font-600 text-ink-primary">
            {{ t('developerForm.location') }}
          </h3>
          <div class="mt-9px grid grid-cols-1 gap-10px sm:grid-cols-2">
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.country') }} *</span
              ><select v-model="form.countryCode" class="dev-input">
                <option v-for="code in countries" :key="code" :value="code">{{ countryName(code) }}</option>
              </select></label
            >
            <label class="flex flex-col gap-4px"
              ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.city') }}</span
              ><input v-model="form.city" class="dev-input"
            /></label>
            <label class="flex items-center gap-7px text-11.5px text-ink-secondary sm:col-span-2"
              ><input v-model="form.remote" type="checkbox" /> {{ t('developerForm.remote') }}</label
            >
          </div>

          <h3 class="m-0 mt-18px border-t border-solid border-line-subtle pt-14px text-13px font-600 text-ink-primary">
            {{ t('developerForm.contact') }} *
          </h3>
          <label class="mt-9px flex flex-col gap-4px"
            ><input
              v-model="form.contactEmail"
              type="email"
              :placeholder="t('developerForm.contactPlaceholder')"
              class="dev-input"
            /><span class="text-10.5px text-ink-tertiary">{{ t('developerForm.contactHint') }}</span></label
          >

          <p
            v-if="submitError"
            class="m-0 mt-10px rounded-default bg-status-danger-subtle px-9px py-7px text-11.5px text-status-danger"
          >
            {{ submitError }}
          </p>
          <div class="mt-14px flex justify-end">
            <button
              type="button"
              class="inline-flex h-32px items-center gap-5px rounded-default bg-brand-coral px-14px text-12px font-600 text-ink-inverse outline-none hover:opacity-90 disabled:cursor-wait disabled:opacity-60"
              :disabled="submitting"
              @click="submit"
            >
              {{ submitting ? t('developerForm.submitting') : t('developerForm.submit') }}
            </button>
          </div>
        </section>

        <aside class="flex flex-col gap-10px">
          <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-13px">
            <span class="text-10px font-600 uppercase tracking-[0.08em] text-ink-tertiary">{{
              t('developerForm.preview')
            }}</span>
            <div class="mt-9px flex items-center gap-10px">
              <OnAppIcon
                v-if="form.iconUrl"
                kind="cask"
                :token="iconToken"
                :name="form.name || 'App'"
                :src="form.iconUrl"
                :size="46"
              />
              <span
                v-else
                class="grid h-46px w-46px place-items-center rounded-app-icon bg-surface-raised text-17px font-700 text-ink-secondary"
                >{{ (form.name || 'A').slice(0, 1) }}</span
              >
              <div class="min-w-0">
                <b class="block truncate text-13px font-600 text-ink-primary">{{
                  form.name || t('developerForm.name')
                }}</b>
                <span class="block truncate text-11px text-ink-secondary">{{
                  form.developer || t('developerForm.developer')
                }}</span>
              </div>
            </div>
            <p class="m-0 mt-9px line-clamp-3 text-11.5px leading-[1.6] text-ink-secondary">
              {{ form.summary || t('developerForm.summary') }}
            </p>
            <div
              class="mt-9px flex flex-wrap items-center gap-5px border-t border-solid border-line-subtle pt-8px text-10.5px text-ink-tertiary"
            >
              <span>{{ previewLocation }}</span>
              <span v-if="form.remote">· {{ t('developerForm.remote') }}</span>
            </div>
          </section>
          <p class="m-0 text-10.5px leading-[1.65] text-ink-tertiary">{{ t('developerForm.fetchFirst') }}</p>
        </aside>
      </div>
    </template>
  </div>
</template>

<style scoped>
.dev-input {
  box-sizing: border-box;
  width: 100%;
  height: 34px;
  padding: 0 10px;
  border: 1px solid var(--on-border-default);
  border-radius: var(--on-radius-default);
  background: var(--on-surface-inset);
  color: var(--on-text-primary);
  font: inherit;
  font-size: 12.5px;
  outline: none;
}

.dev-input:focus {
  border-color: var(--on-border-strong);
  box-shadow: var(--on-shadow-focus-ring);
}
</style>
