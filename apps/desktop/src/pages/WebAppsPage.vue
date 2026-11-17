<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCategoryChips, OnConfirm, OnEmpty, OnPageHeader, OnSegmented, OnSheet } from '@opennavo/ui';
import CatalogAppCard from '@/components/webapps/CatalogAppCard.vue';
import WebAppCard from '@/components/webapps/WebAppCard.vue';
import {
  WEBAPP_CATALOG,
  WEBAPP_CATEGORIES,
  type WebAppCatalogEntry,
  type WebAppCategory
} from '@/components/webapps/catalog';
import type { WebApp } from '@/ipc/bindings';
import { IpcError, commands, unwrap } from '@/ipc/client';

type WebAppsTab = 'installed' | 'catalog';
type CatalogFilter = 'all' | WebAppCategory;

const { t } = useI18n();
const apps = ref<WebApp[]>([]);
const loading = ref(true);
const loadError = ref('');
const sheetOpen = ref(false);
const creating = ref(false);
const formError = ref('');
const nameTouched = ref(false);
const form = reactive({ name: '', url: '' });
const deleteOpen = ref(false);
const deleteTarget = ref<WebApp | null>(null);
const deleting = ref(false);
const tab = ref<WebAppsTab>('catalog');
const catalogFilter = ref<CatalogFilter>('all');
const catalogAdding = ref<string | null>(null);
const catalogError = ref('');

const canCreate = computed(() => Boolean(form.name.trim() && form.url.trim()));

const tabOptions = computed<{ value: WebAppsTab; label: string; count: number }[]>(() => [
  { value: 'installed', label: t('webapps.tabs.installed'), count: apps.value.length },
  { value: 'catalog', label: t('webapps.tabs.catalog'), count: WEBAPP_CATALOG.length }
]);

const catalogFilters = computed<{ value: CatalogFilter; label: string }[]>(() => [
  { value: 'all', label: t('webapps.catalog.all') },
  ...WEBAPP_CATEGORIES.map(category => ({
    value: category,
    label: t(`webapps.catalog.categories.${category}`)
  }))
]);

const catalogGroups = computed(() =>
  WEBAPP_CATEGORIES.map(category => ({
    category,
    entries: WEBAPP_CATALOG.filter(entry => entry.category === category)
  }))
);

const visibleCatalog = computed(() =>
  catalogFilter.value === 'all'
    ? WEBAPP_CATALOG
    : WEBAPP_CATALOG.filter(entry => entry.category === catalogFilter.value)
);

const installedHosts = computed(() => new Set(apps.value.map(app => hostKey(app.url))));

function normalizedUrl(value: string) {
  const trimmed = value.trim();
  return /^[a-z][a-z0-9+.-]*:\/\//i.test(trimmed) ? trimmed : `https://${trimmed}`;
}

function hostKey(value: string) {
  try {
    return new URL(normalizedUrl(value)).hostname.replace(/^www\./, '').toLowerCase();
  } catch {
    return value.trim().toLowerCase();
  }
}

function isInstalled(entry: WebAppCatalogEntry) {
  return installedHosts.value.has(hostKey(entry.url));
}

function uniqueName(base: string) {
  const names = new Set(apps.value.map(app => app.name));
  if (!names.has(base)) return base;
  for (let index = 2; index < 100; index += 1) {
    const candidate = `${base} ${index}`;
    if (!names.has(candidate)) return candidate;
  }
  return `${base} ${Date.now()}`;
}

function errorMessage(error: unknown) {
  if (error instanceof IpcError) {
    if (error.code === 'E_WEBAPP_NAME_INVALID') return t('webapps.invalidName');
    if (error.code === 'E_WEBAPP_URL_INVALID') return t('webapps.invalidUrl');
    if (error.code === 'E_WEBAPP_EXISTS') return t('webapps.exists');
  }
  return t('webapps.failed');
}

async function load() {
  loading.value = true;
  loadError.value = '';
  try {
    apps.value = await unwrap(commands.webappsList());
  } catch (error) {
    loadError.value = errorMessage(error);
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  form.name = '';
  form.url = '';
  formError.value = '';
  nameTouched.value = false;
  sheetOpen.value = true;
}

watch(
  () => form.url,
  value => {
    if (nameTouched.value) return;
    try {
      const host = new URL(normalizedUrl(value)).hostname.replace(/^www\./, '');
      const label = host.split('.')[0] ?? '';
      form.name = label ? label.charAt(0).toUpperCase() + label.slice(1) : '';
    } catch {
      // Keep the last useful name while the address is incomplete.
    }
  }
);

async function create() {
  formError.value = '';
  if (!form.name.trim()) {
    formError.value = t('webapps.invalidName');
    return;
  }
  if (!form.url.trim()) {
    formError.value = t('webapps.invalidUrl');
    return;
  }
  creating.value = true;
  try {
    const app = await unwrap(commands.webappsCreate({ name: form.name.trim(), url: normalizedUrl(form.url) }));
    apps.value = [app, ...apps.value];
    sheetOpen.value = false;
  } catch (error) {
    formError.value = errorMessage(error);
  } finally {
    creating.value = false;
  }
}

// Enter belongs to the IME while its candidate window is open.
function submitOnEnter(event: KeyboardEvent) {
  if (event.isComposing || event.keyCode === 229) return;
  event.preventDefault();
  void create();
}

async function addFromCatalog(entry: WebAppCatalogEntry) {
  if (catalogAdding.value) return;
  catalogError.value = '';
  catalogAdding.value = entry.id;
  try {
    const app = await unwrap(commands.webappsCreate({ name: uniqueName(entry.name), url: entry.url }));
    apps.value = [app, ...apps.value];
  } catch (error) {
    catalogError.value = errorMessage(error);
  } finally {
    catalogAdding.value = null;
  }
}

async function open(app: WebApp) {
  try {
    await unwrap(commands.webappsOpen(app.id));
  } catch (error) {
    loadError.value = errorMessage(error);
  }
}

async function reveal(app: WebApp) {
  try {
    await unwrap(commands.webappsReveal(app.id));
  } catch (error) {
    loadError.value = errorMessage(error);
  }
}

function askDelete(app: WebApp) {
  deleteTarget.value = app;
  deleteOpen.value = true;
}

async function confirmDelete() {
  if (!deleteTarget.value) return;
  deleting.value = true;
  try {
    await unwrap(commands.webappsDelete(deleteTarget.value.id));
    apps.value = apps.value.filter(app => app.id !== deleteTarget.value?.id);
    deleteOpen.value = false;
    deleteTarget.value = null;
    if (!apps.value.length) tab.value = 'catalog';
  } catch (error) {
    loadError.value = errorMessage(error);
  } finally {
    deleting.value = false;
  }
}

watch([tab, catalogFilter], () => {
  catalogError.value = '';
});

onMounted(load);
</script>

<template>
  <div class="mx-auto flex w-full max-w-1160 flex-col gap-8px pb-32px">
    <OnPageHeader :title="t('webapps.title')" :subtitle="t('webapps.subtitle')">
      <template #actions>
        <OnButton variant="primary" icon="plus" @click="openCreate">{{ t('webapps.new') }}</OnButton>
      </template>
    </OnPageHeader>

    <p
      v-if="loadError"
      class="m-0 rounded-default border border-solid border-line-subtle bg-status-danger-subtle px-10px py-8px text-11.5px text-status-danger"
    >
      {{ loadError }}
    </p>

    <OnSegmented
      v-model="tab"
      class="mt-2px self-start"
      size="sm"
      :options="tabOptions"
      :aria-label="t('webapps.title')"
    />

    <p v-if="loading" class="m-0 py-40px text-center text-12px text-ink-tertiary">{{ t('common.loading') }}</p>

    <template v-else-if="tab === 'installed'">
      <OnEmpty
        v-if="!apps.length"
        icon="lucide:app-window"
        :title="t('webapps.emptyTitle')"
        :description="t('webapps.emptyBody')"
      >
        <template #actions>
          <OnButton variant="primary" icon="plus" @click="openCreate">{{ t('webapps.new') }}</OnButton>
          <OnButton variant="secondary" icon="lucide:layout-grid" @click="tab = 'catalog'">
            {{ t('webapps.browseCatalog') }}
          </OnButton>
        </template>
      </OnEmpty>

      <ul v-else class="m-0 grid list-none gap-12px p-0 sm:grid-cols-2">
        <li v-for="app in apps" :key="app.id" class="min-w-0">
          <WebAppCard :app="app" @open="open(app)" @reveal="reveal(app)" @remove="askDelete(app)" />
        </li>
      </ul>
    </template>

    <div v-else class="flex flex-col gap-12px">
      <p class="m-0 max-w-680px text-11.5px leading-[1.6] text-ink-tertiary">
        {{ t('webapps.catalog.hint') }}
      </p>

      <OnCategoryChips v-model="catalogFilter" :items="catalogFilters" :aria-label="t('webapps.catalog.filter')" />

      <p
        v-if="catalogError"
        class="m-0 rounded-default border border-solid border-line-subtle bg-status-danger-subtle px-10px py-8px text-11.5px text-status-danger"
      >
        {{ catalogError }}
      </p>

      <template v-if="catalogFilter === 'all'">
        <section v-for="group in catalogGroups" :key="group.category" class="flex flex-col gap-9px">
          <h2 class="m-0 text-12px font-600 text-ink-secondary">
            {{ t(`webapps.catalog.categories.${group.category}`) }}
          </h2>
          <ul class="m-0 grid list-none gap-12px p-0 sm:grid-cols-2 lg:grid-cols-3">
            <li v-for="entry in group.entries" :key="entry.id" class="min-w-0" :data-catalog-id="entry.id">
              <CatalogAppCard
                :entry="entry"
                :installed="isInstalled(entry)"
                :adding="catalogAdding === entry.id"
                :disabled="catalogAdding !== null && catalogAdding !== entry.id"
                @add="addFromCatalog(entry)"
              />
            </li>
          </ul>
        </section>
      </template>

      <ul v-else class="m-0 grid list-none gap-12px p-0 sm:grid-cols-2 lg:grid-cols-3">
        <li v-for="entry in visibleCatalog" :key="entry.id" class="min-w-0" :data-catalog-id="entry.id">
          <CatalogAppCard
            :entry="entry"
            :installed="isInstalled(entry)"
            :adding="catalogAdding === entry.id"
            :disabled="catalogAdding !== null && catalogAdding !== entry.id"
            @add="addFromCatalog(entry)"
          />
        </li>
      </ul>
    </div>

    <p v-if="!loading" class="m-0 mt-4px text-10.5px leading-[1.6] text-ink-tertiary">
      {{ t('webapps.storageNote') }}
    </p>

    <OnSheet v-model:open="sheetOpen" :title="t('webapps.new')" initial-focus="#webapp-name">
      <div class="flex flex-col gap-16px">
        <label class="flex flex-col gap-5px">
          <span class="text-11px font-500 text-ink-secondary">{{ t('webapps.name') }}</span>
          <input
            id="webapp-name"
            v-model="form.name"
            class="webapp-input"
            maxlength="64"
            :placeholder="t('webapps.namePlaceholder')"
            @input="nameTouched = true"
          />
        </label>

        <label class="flex flex-col gap-5px">
          <span class="text-11px font-500 text-ink-secondary">{{ t('webapps.url') }}</span>
          <input
            v-model="form.url"
            class="webapp-input"
            type="url"
            inputmode="url"
            autocomplete="url"
            :placeholder="t('webapps.urlPlaceholder')"
            @keydown.enter="submitOnEnter"
          />
          <span class="text-10.5px leading-[1.5] text-ink-tertiary">{{ t('webapps.urlHint') }}</span>
        </label>

        <p
          v-if="formError"
          class="m-0 rounded-default bg-status-danger-subtle px-8px py-6px text-11px text-status-danger"
        >
          {{ formError }}
        </p>
      </div>

      <template #footer>
        <OnButton variant="ghost" :disabled="creating" @click="sheetOpen = false">{{ t('common.cancel') }}</OnButton>
        <OnButton variant="primary" :loading="creating" :disabled="!canCreate" @click="create">
          {{ t('webapps.create') }}
        </OnButton>
      </template>
    </OnSheet>

    <OnConfirm
      :open="deleteOpen"
      tone="danger"
      :title="t('webapps.deleteTitle', { name: deleteTarget?.name ?? '' })"
      :description="t('webapps.deleteBody')"
      :confirm-label="t('webapps.delete')"
      :loading="deleting"
      @update:open="deleteOpen = $event"
      @confirm="confirmDelete"
      @cancel="deleteTarget = null"
    />
  </div>
</template>

<style scoped>
.webapp-input {
  box-sizing: border-box;
  width: 100%;
  height: 34px;
  padding: 0 10px;
  border: 1px solid var(--on-border-default);
  border-radius: var(--on-radius-default);
  background: var(--on-surface-inset);
  color: var(--on-text-primary);
  font: inherit;
  font-size: 13px;
  outline: none;
}
.webapp-input::placeholder {
  color: var(--on-text-disabled);
}
.webapp-input:focus {
  border-color: var(--on-border-strong);
  box-shadow: var(--on-shadow-focus-ring);
}
</style>
