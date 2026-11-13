<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { unwrap, type PackageSummary } from '@opennavo/api';
import { OnAppIcon, OnIcon } from '@opennavo/ui';
import { submitDeveloperSubmission, type DeveloperSubmitResult } from '~/composables/useDeveloperMap';

const { t, locale } = useI18n();
const localePath = useLocalePath();
const api = useApi();
const NuxtLink = resolveComponent('NuxtLink');

const query = ref('');
const results = ref<PackageSummary[]>([]);
const searching = ref(false);
const searchError = ref('');
const selected = ref<PackageSummary | null>(null);
const submitting = ref(false);
const submitError = ref('');
const submitted = ref<DeveloperSubmitResult | null>(null);

const form = reactive({
  proof: '',
  updateLocation: true,
  updateSummary: true,
  updateIcon: false,
  countryCode: 'US',
  city: '',
  remote: false,
  evidence: '',
  note: '',
  contactEmail: ''
});

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
const emailValid = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.contactEmail));
const canSubmit = computed(() =>
  Boolean(selected.value && emailValid.value && (!form.updateLocation || form.countryCode))
);

async function search() {
  const value = query.value.trim();
  if (!value) return;
  searching.value = true;
  searchError.value = '';
  try {
    const response = await unwrap(api.GET('/search', { params: { query: { q: value, size: 8 } } }));
    results.value = response.records.map(record => record.package);
    if (!results.value.length) searchError.value = t('developerForm.noResults');
  } catch {
    searchError.value = t('developerForm.noResults');
    results.value = [];
  } finally {
    searching.value = false;
  }
}

function choose(pkg: PackageSummary) {
  selected.value = pkg;
  results.value = [];
  query.value = pkg.displayName;
  submitError.value = '';
}

async function submit() {
  submitError.value = '';
  if (!selected.value) {
    submitError.value = t('developerForm.selectApp');
    return;
  }
  if (!canSubmit.value) {
    submitError.value = emailValid.value ? t('developerForm.required') : t('developerForm.invalidEmail');
    return;
  }
  submitting.value = true;
  try {
    const result = await submitDeveloperSubmission({
      kind: 'claim',
      packageKind: selected.value.kind,
      packageToken: selected.value.token,
      contactEmail: form.contactEmail.trim(),
      proof: form.proof.trim(),
      payload: {
        updateLocation: form.updateLocation,
        updateSummary: form.updateSummary,
        updateIcon: form.updateIcon,
        countryCode: form.updateLocation ? form.countryCode : '',
        city: form.updateLocation ? form.city.trim() : '',
        remote: form.updateLocation ? form.remote : false,
        evidence: form.evidence.trim(),
        note: form.note.trim()
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
  selected.value = null;
  query.value = '';
  results.value = [];
  Object.assign(form, {
    proof: '',
    updateLocation: true,
    updateSummary: true,
    updateIcon: false,
    countryCode: 'US',
    city: '',
    remote: false,
    evidence: '',
    note: '',
    contactEmail: ''
  });
}
</script>

<template>
  <section v-if="submitted" class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
    <span class="grid h-34px w-34px place-items-center rounded-full bg-status-success-subtle text-status-success"
      ><OnIcon name="check" :size="18"
    /></span>
    <h2 class="m-0 mt-10px text-18px font-600 text-ink-primary">{{ t('developerForm.claimSuccess') }}</h2>
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
      <h2 class="m-0 text-18px font-600 text-ink-primary">{{ t('developerForm.selectApp') }}</h2>
      <p class="m-0 mt-4px text-12.5px leading-[1.7] text-ink-secondary">{{ t('developerForm.claimHint') }}</p>
      <div class="mt-11px flex flex-col gap-8px sm:flex-row">
        <label class="relative min-w-0 flex-1">
          <OnIcon name="search" :size="14" class="absolute left-10px top-1/2 -translate-y-1/2 text-ink-tertiary" />
          <input
            v-model="query"
            type="search"
            :placeholder="t('developerForm.searchPlaceholder')"
            class="h-36px w-full rounded-default border border-solid border-line-default bg-surface-inset pl-31px pr-10px text-12.5px text-ink-primary outline-none placeholder:text-ink-tertiary focus:border-line-strong focus:shadow-focus-ring"
            @keydown.enter.prevent="search"
          />
        </label>
        <button
          type="button"
          class="inline-flex h-36px shrink-0 items-center justify-center rounded-default border border-solid border-line-default px-14px text-12px font-500 text-ink-primary outline-none hover:bg-surface-raised disabled:opacity-60"
          :disabled="searching"
          @click="search"
        >
          {{ searching ? t('developerForm.fetching') : t('developerForm.search') }}
        </button>
      </div>
      <p v-if="searchError" class="m-0 mt-8px text-11.5px text-ink-tertiary">{{ searchError }}</p>
      <div v-if="results.length" class="mt-10px flex flex-col gap-4px">
        <button
          v-for="pkg in results"
          :key="`${pkg.kind}/${pkg.token}`"
          type="button"
          class="flex min-w-0 items-center gap-9px rounded-default border border-solid border-line-subtle px-9px py-7px text-left outline-none hover:bg-surface-raised focus-visible:shadow-focus-ring"
          @click="choose(pkg)"
        >
          <OnAppIcon
            :kind="pkg.kind"
            :token="pkg.token"
            :name="pkg.displayName"
            :src="pkg.iconUrl"
            :accent="pkg.accentColor"
            :size="30"
          />
          <span class="min-w-0 flex-1"
            ><b class="block truncate text-12px font-600 text-ink-primary">{{ pkg.displayName }}</b
            ><span class="block truncate text-10.5px text-ink-tertiary">{{ pkg.summary }}</span></span
          >
          <span class="shrink-0 text-10px text-ink-tertiary">{{ pkg.kind }}</span>
        </button>
      </div>
      <div
        v-if="selected"
        class="mt-10px flex min-w-0 items-center gap-9px rounded-default border border-solid border-line-accent bg-accent-coral-subtle px-9px py-8px"
      >
        <OnAppIcon
          :kind="selected.kind"
          :token="selected.token"
          :name="selected.displayName"
          :src="selected.iconUrl"
          :accent="selected.accentColor"
          :size="30"
        />
        <span class="min-w-0 flex-1"
          ><b class="block truncate text-12px font-600 text-ink-primary">{{ selected.displayName }}</b
          ><span class="block truncate text-10.5px text-ink-tertiary">{{ selected.summary }}</span></span
        >
        <span class="shrink-0 rounded-[4px] bg-surface-card px-5px py-2px text-9.5px font-600 text-ink-secondary">{{
          t('developerForm.selected')
        }}</span>
      </div>
    </section>

    <template v-if="selected">
      <div class="grid grid-cols-1 items-start gap-12px lg:grid-cols-[minmax(0,1fr)_300px]">
        <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
          <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('developerForm.proof') }}</h2>
          <label class="mt-9px flex flex-col gap-4px">
            <input v-model="form.proof" class="dev-input" :placeholder="t('developerForm.proofPlaceholder')" />
            <span class="text-10.5px text-ink-tertiary">{{ t('developerForm.proofHint') }}</span>
          </label>

          <h3 class="m-0 mt-16px border-t border-solid border-line-subtle pt-13px text-13px font-600 text-ink-primary">
            {{ t('developerForm.updateContent') }}
          </h3>
          <div class="mt-8px flex flex-col gap-7px">
            <label class="flex items-center gap-7px text-11.5px text-ink-secondary"
              ><input v-model="form.updateLocation" type="checkbox" /> {{ t('developerForm.updateLocation') }}</label
            >
            <label class="flex items-center gap-7px text-11.5px text-ink-secondary"
              ><input v-model="form.updateSummary" type="checkbox" /> {{ t('developerForm.updateSummary') }}</label
            >
            <label class="flex items-center gap-7px text-11.5px text-ink-secondary"
              ><input v-model="form.updateIcon" type="checkbox" /> {{ t('developerForm.updateIcon') }}</label
            >
          </div>

          <div
            v-if="form.updateLocation"
            class="mt-12px rounded-default border border-solid border-line-subtle bg-surface-inset p-11px"
          >
            <h3 class="m-0 text-12.5px font-600 text-ink-primary">{{ t('developerForm.location') }}</h3>
            <div class="mt-8px grid grid-cols-1 gap-9px sm:grid-cols-2">
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
              <label class="flex flex-col gap-4px sm:col-span-2"
                ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.evidence') }}</span
                ><input v-model="form.evidence" type="url" class="dev-input" placeholder="https://example.com/about"
              /></label>
            </div>
          </div>

          <label class="mt-12px flex flex-col gap-4px"
            ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.note') }}</span
            ><textarea v-model="form.note" rows="3" class="dev-input min-h-70px resize-y py-8px" />
          </label>
          <label class="mt-11px flex flex-col gap-4px"
            ><span class="text-11px font-500 text-ink-secondary">{{ t('developerForm.contact') }} *</span
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
              {{ submitting ? t('developerForm.submitting') : t('developerForm.claimSubmit') }}
            </button>
          </div>
        </section>

        <aside class="rounded-big border border-solid border-line-subtle bg-surface-card p-13px">
          <span class="text-10px font-600 uppercase tracking-[0.08em] text-ink-tertiary">{{
            t('developerForm.preview')
          }}</span>
          <div class="mt-9px flex items-center gap-10px">
            <OnAppIcon
              :kind="selected.kind"
              :token="selected.token"
              :name="selected.displayName"
              :src="selected.iconUrl"
              :accent="selected.accentColor"
              :size="46"
            />
            <div class="min-w-0">
              <b class="block truncate text-13px font-600 text-ink-primary">{{ selected.displayName }}</b
              ><span class="block truncate text-11px text-ink-secondary">{{ selected.summary }}</span>
            </div>
          </div>
          <div class="mt-9px border-t border-solid border-line-subtle pt-8px text-10.5px text-ink-tertiary">
            {{ [form.city, countryName(form.countryCode)].filter(Boolean).join(' · ') }}
          </div>
          <p class="m-0 mt-8px text-10.5px leading-[1.65] text-ink-tertiary">{{ t('developerForm.proofHint') }}</p>
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
