<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { OnIcon } from '@opennavo/ui';
import { fetchDeveloperSubmissionStatus, type DeveloperSubmissionStatus } from '~/composables/useDeveloperMap';

const props = defineProps<{ initialToken?: string }>();

const { t, locale } = useI18n();
const NuxtLink = resolveComponent('NuxtLink');
const localePath = useLocalePath();

const token = ref('');
const status = ref<DeveloperSubmissionStatus | null>(null);
const loading = ref(false);
const error = ref('');

const dateFormat = computed(() => new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }));

function formatDate(value?: string | null): string {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? '' : dateFormat.value.format(date);
}

/** Accept either the 32-char receipt token or a copied status link. */
function normalizeToken(value: string): string {
  const raw = value.trim();
  if (!raw) return '';
  const match = raw.match(/[a-f0-9]{32}/i);
  return match ? match[0].toLowerCase() : raw.toLowerCase();
}

async function check() {
  const value = normalizeToken(token.value);
  if (!/^[a-f0-9]{32}$/.test(value)) {
    status.value = null;
    error.value = t('developerForm.statusNotFound');
    return;
  }
  token.value = value;
  loading.value = true;
  error.value = '';
  try {
    const result = await fetchDeveloperSubmissionStatus(value);
    status.value = result;
    if (!result) error.value = t('developerForm.statusNotFound');
  } catch {
    status.value = null;
    error.value = t('developerForm.statusFailed');
  } finally {
    loading.value = false;
  }
}

function statusLabel(value: string): string {
  const key = `developerForm.status${value.replace(/(^|_)([a-z])/g, (_, __, letter: string) => letter.toUpperCase())}`;
  const label = t(key);
  return label === key ? value : label;
}

function kindLabel(value: string): string {
  const key = `developerForm.kind${value.charAt(0).toUpperCase()}${value.slice(1)}`;
  const label = t(key);
  return label === key ? value : label;
}

function statusTone(value: string): string {
  if (value === 'published' || value === 'approved') return 'bg-status-success-subtle text-status-success';
  if (value === 'changes_requested') return 'bg-status-warning-subtle text-status-warning';
  if (value === 'rejected') return 'bg-status-danger-subtle text-status-danger';
  return 'bg-surface-inset text-ink-secondary';
}

watch(
  () => props.initialToken,
  value => {
    const next = normalizeToken(value ?? '');
    if (!next) return;
    token.value = next;
    void check();
  },
  { immediate: true }
);
</script>

<template>
  <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
    <div class="flex flex-wrap items-start justify-between gap-10px">
      <div class="min-w-0">
        <h2 class="m-0 text-16px font-600 text-ink-primary">{{ t('developerForm.statusTitle') }}</h2>
        <p class="m-0 mt-4px max-w-680px text-12px leading-[1.7] text-ink-secondary">
          {{ t('developerForm.statusHint') }}
        </p>
      </div>
    </div>

    <form class="mt-12px flex flex-wrap items-center gap-8px" @submit.prevent="check">
      <input
        v-model="token"
        class="status-input flex-1"
        :placeholder="t('developerForm.statusPlaceholder')"
        autocomplete="off"
        spellcheck="false"
        :aria-label="t('developerForm.statusPlaceholder')"
      />
      <button
        type="submit"
        class="inline-flex h-34px items-center gap-5px rounded-default bg-brand-coral px-13px text-12px font-600 text-ink-inverse outline-none hover:opacity-90 focus-visible:shadow-focus-ring disabled:opacity-60"
        :disabled="loading"
      >
        <OnIcon v-if="loading" name="loader-circle" :size="13" />
        {{ loading ? t('developerForm.statusChecking') : t('developerForm.statusCheck') }}
      </button>
    </form>

    <p
      v-if="error"
      class="m-0 mt-9px rounded-default bg-status-danger-subtle px-9px py-7px text-11.5px text-status-danger"
    >
      {{ error }}
    </p>

    <article v-if="status" class="mt-12px rounded-default border border-solid border-line-subtle">
      <header
        class="flex flex-wrap items-center justify-between gap-8px border-b border-solid border-line-subtle px-12px py-9px"
      >
        <div class="flex flex-wrap items-center gap-8px">
          <b class="text-13px font-600 tabular-nums text-ink-primary">{{ status.reference }}</b>
          <span class="rounded-full px-7px py-1.5px text-10.5px font-600" :class="statusTone(status.status)">{{
            statusLabel(status.status)
          }}</span>
          <span class="text-11px text-ink-tertiary">{{ kindLabel(status.kind) }}</span>
        </div>
        <span class="text-10.5px text-ink-tertiary">{{ formatDate(status.updatedAt) }}</span>
      </header>

      <div class="grid grid-cols-1 gap-x-14px gap-y-8px px-12px py-10px text-11.5px sm:grid-cols-2">
        <p class="m-0 text-ink-secondary">
          {{ t('developerForm.statusSubmittedAt') }}:
          <b class="font-500 text-ink-primary">{{ formatDate(status.createdAt) }}</b>
        </p>
        <p v-if="status.decidedAt" class="m-0 text-ink-secondary">
          {{ t('developerForm.statusDecidedAt') }}:
          <b class="font-500 text-ink-primary">{{ formatDate(status.decidedAt) }}</b>
        </p>
        <p v-if="status.package" class="m-0 text-ink-secondary sm:col-span-2">
          {{ t('developerForm.statusPackage') }}:
          <NuxtLink :to="localePath(`/apps/${status.package.token}`)" class="font-600 text-ink-primary underline">{{
            status.package.name
          }}</NuxtLink>
        </p>
        <p v-if="status.decisionNote" class="m-0 text-ink-secondary sm:col-span-2">
          {{ t('developerForm.statusNote') }}: <span class="text-ink-primary">{{ status.decisionNote }}</span>
        </p>
      </div>

      <div class="border-t border-solid border-line-subtle px-12px py-10px">
        <h3 class="m-0 text-11px font-600 uppercase tracking-[0.08em] text-ink-tertiary">
          {{ t('developerForm.statusTimeline') }}
        </h3>
        <ol v-if="status.events.length" class="m-0 mt-8px flex list-none flex-col gap-7px p-0">
          <li
            v-for="(event, index) in status.events"
            :key="`${event.createdAt}-${index}`"
            class="flex items-start gap-8px"
          >
            <span class="mt-4px h-6px w-6px shrink-0 rounded-full" :class="statusTone(event.status)" />
            <div class="min-w-0">
              <p class="m-0 text-11.5px text-ink-primary">
                <b class="font-600">{{ statusLabel(event.status) }}</b>
                <span class="ml-5px text-10.5px text-ink-tertiary">{{ event.actor }}</span>
              </p>
              <p v-if="event.note" class="m-0 mt-1px text-10.5px leading-[1.6] text-ink-secondary">{{ event.note }}</p>
            </div>
            <span class="ml-auto shrink-0 text-10px tabular-nums text-ink-tertiary">{{
              formatDate(event.createdAt)
            }}</span>
          </li>
        </ol>
        <p v-else class="m-0 mt-6px text-11px text-ink-tertiary">{{ t('developerForm.statusEmpty') }}</p>
      </div>
    </article>
  </section>
</template>

<style scoped>
.status-input {
  box-sizing: border-box;
  min-width: 220px;
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

.status-input:focus {
  border-color: var(--on-border-strong);
  box-shadow: var(--on-shadow-focus-ring);
}
</style>
