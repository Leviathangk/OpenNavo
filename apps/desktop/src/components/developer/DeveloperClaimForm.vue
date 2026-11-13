<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { unwrap, type PackageSummary } from '@opennavo/api';
import { OnAppIcon, OnIcon } from '@opennavo/ui';
import { api } from '@/api';
import { submitDeveloperSubmission, type DeveloperSubmitResult } from '@/composables/useDeveloperMap';

const emit = defineEmits<{ submitted: [token: string] }>();
const { t, locale } = useI18n();

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
function countryName(code: string) {
  try {
    return regionNames.value.of(code) ?? code;
  } catch {
    return code;
  }
}

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
    results.value = [];
    searchError.value = t('developerForm.noResults');
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
    emit('submitted', result.token);
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
  <section v-if="submitted" class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
    <span class="grid h-32px w-32px place-items-center rounded-full bg-status-success-subtle text-status-success"
      ><OnIcon name="check" :size="17"
    /></span>
    <h2 class="m-0 mt-9px text-16px font-600 text-ink-primary">{{ t('developerForm.claimSuccess') }}</h2>
    <p class="m-0 mt-4px text-12px text-ink-secondary">{{ t('developerForm.successBody') }}</p>
    <p v-if="submitted?.reference" class="m-0 mt-6px text-10.5px text-ink-tertiary">
      {{ t('developerForm.reference') }}: <b class="font-600 text-ink-primary">{{ submitted.reference }}</b>
    </p>
    <p v-if="submitted?.token" class="m-0 mt-4px break-all text-10px text-ink-tertiary">
      {{ t('developerForm.statusToken') }}: <b class="font-500 text-ink-secondary">{{ submitted.token }}</b>
    </p>
    <button
      type="button"
      class="mt-12px h-28px rounded-default bg-brand-coral px-11px text-11.5px font-600 text-ink-inverse"
      @click="reset"
    >
      {{ t('developerForm.again') }}
    </button>
  </section>

  <div v-else class="flex flex-col gap-12px">
    <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
      <h2 class="m-0 text-16px font-600 text-ink-primary">{{ t('developerForm.selectApp') }}</h2>
      <p class="m-0 mt-3px text-12px leading-[1.7] text-ink-secondary">{{ t('developerForm.claimHint') }}</p>
      <div class="mt-9px flex flex-col gap-7px sm:flex-row">
        <label class="relative min-w-0 flex-1">
          <OnIcon name="search" :size="13" class="absolute left-9px top-1/2 -translate-y-1/2 text-ink-tertiary" />
          <input
            v-model="query"
            type="search"
            :placeholder="t('developerForm.searchPlaceholder')"
            class="dev-input pl-28px"
            @keydown.enter.prevent="search"
          />
        </label>
        <button
          type="button"
          class="h-32px shrink-0 rounded-default border border-solid border-line-default px-12px text-11.5px font-500 text-ink-primary disabled:opacity-60"
          :disabled="searching"
          @click="search"
        >
          {{ searching ? t('developerForm.fetching') : t('developerForm.search') }}
        </button>
      </div>
      <p v-if="searchError" class="m-0 mt-7px text-11px text-ink-tertiary">{{ searchError }}</p>
      <div v-if="results.length" class="mt-8px flex flex-col gap-4px">
        <button
          v-for="pkg in results"
          :key="`${pkg.kind}/${pkg.token}`"
          type="button"
          class="flex min-w-0 items-center gap-8px rounded-default border border-solid border-line-subtle px-8px py-6px text-left hover:bg-surface-raised"
          @click="choose(pkg)"
        >
          <OnAppIcon
            :kind="pkg.kind"
            :token="pkg.token"
            :name="pkg.displayName"
            :src="pkg.iconUrl"
            :accent="pkg.accentColor"
            :size="28"
          />
          <span class="min-w-0 flex-1"
            ><b class="block truncate text-11.5px font-600 text-ink-primary">{{ pkg.displayName }}</b
            ><span class="block truncate text-10px text-ink-tertiary">{{ pkg.summary }}</span></span
          >
          <span class="shrink-0 text-9.5px text-ink-tertiary">{{ pkg.kind }}</span>
        </button>
      </div>
      <div
        v-if="selected"
        class="mt-8px flex min-w-0 items-center gap-8px rounded-default border border-solid border-line-accent bg-accent-coral-subtle px-8px py-7px"
      >
        <OnAppIcon
          :kind="selected.kind"
          :token="selected.token"
          :name="selected.displayName"
          :src="selected.iconUrl"
          :accent="selected.accentColor"
          :size="28"
        />
        <span class="min-w-0 flex-1"
          ><b class="block truncate text-11.5px font-600 text-ink-primary">{{ selected.displayName }}</b
          ><span class="block truncate text-10px text-ink-tertiary">{{ selected.summary }}</span></span
        >
        <span class="shrink-0 rounded-[4px] bg-surface-card px-5px py-2px text-9px font-600 text-ink-secondary">{{
          t('developerForm.selected')
        }}</span>
      </div>
    </section>

    <section v-if="selected" class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
      <h3 class="m-0 text-12.5px font-600 text-ink-primary">{{ t('developerForm.proof') }}</h3>
      <input v-model="form.proof" :placeholder="t('developerForm.proofPlaceholder')" class="dev-input mt-7px" />
      <p class="m-0 mt-4px text-10px text-ink-tertiary">{{ t('developerForm.proofHint') }}</p>

      <h3 class="m-0 mt-13px border-t border-solid border-line-subtle pt-11px text-12.5px font-600 text-ink-primary">
        {{ t('developerForm.updateContent') }}
      </h3>
      <div class="mt-7px flex flex-col gap-6px">
        <label class="flex items-center gap-6px text-11px text-ink-secondary"
          ><input v-model="form.updateLocation" type="checkbox" /> {{ t('developerForm.updateLocation') }}</label
        >
        <label class="flex items-center gap-6px text-11px text-ink-secondary"
          ><input v-model="form.updateSummary" type="checkbox" /> {{ t('developerForm.updateSummary') }}</label
        >
        <label class="flex items-center gap-6px text-11px text-ink-secondary"
          ><input v-model="form.updateIcon" type="checkbox" /> {{ t('developerForm.updateIcon') }}</label
        >
      </div>

      <template v-if="form.updateLocation">
        <h3 class="m-0 mt-13px border-t border-solid border-line-subtle pt-11px text-12.5px font-600 text-ink-primary">
          {{ t('developerForm.location') }}
        </h3>
        <div class="mt-7px grid grid-cols-1 gap-7px sm:grid-cols-2">
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
      </template>

      <h3 class="m-0 mt-13px border-t border-solid border-line-subtle pt-11px text-12.5px font-600 text-ink-primary">
        {{ t('developerForm.evidence') }}
      </h3>
      <input v-model="form.evidence" type="url" placeholder="https://example.com/about" class="dev-input mt-7px" />
      <textarea
        v-model="form.note"
        rows="2"
        :placeholder="t('developerForm.note')"
        class="dev-input mt-7px h-auto py-6px"
      />

      <input
        v-model="form.contactEmail"
        type="email"
        :placeholder="t('developerForm.contactPlaceholder')"
        class="dev-input mt-9px"
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
        {{ submitting ? t('developerForm.submitting') : t('developerForm.claimSubmit') }}
      </button>
    </section>
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
