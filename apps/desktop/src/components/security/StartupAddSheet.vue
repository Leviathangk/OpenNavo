<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnEmpty, OnSearchField, OnSheet, OnSkeleton } from '@opennavo/ui';
import type { LocalApp } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useLocalAppsStore } from '@/stores/localApps';

// Pick an app to open at login: the apps on this Mac, with the names and icons Installed shows. Apps that already
// open at login are left out.
const props = defineProps<{ open: boolean; opening: ReadonlySet<string>; adding: string | null }>();
const emit = defineEmits<{ close: []; add: [app: LocalApp] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { localApp } = usePackageIdentity();
const localApps = useLocalAppsStore();
const query = ref('');
const loadFailed = ref(false);

watch(
  () => props.open,
  open => {
    if (!open) return;
    query.value = '';
    if (!localApps.loaded) void localApps.load().catch(() => (loadFailed.value = true));
  },
  { immediate: true }
);

const apps = computed(() => {
  const q = query.value.trim().toLowerCase();
  const collator = new Intl.Collator(appLocale.value);
  return localApps.items
    .filter(app => !props.opening.has(app.path))
    .map(app => ({ app, ...localApp(app) }))
    .filter(({ app, name }) => !q || [name, app.name, app.bundleId].some(value => value?.toLowerCase().includes(q)))
    .sort((a, b) => collator.compare(a.name, b.name));
});
const folder = (app: LocalApp) => app.path.slice(0, app.path.lastIndexOf('/')) || '/';
</script>

<template>
  <OnSheet :open="open" :title="t('security.startup.add')" @close="emit('close')">
    <div class="flex flex-col gap-12px">
      <p class="m-0 text-12.5px text-ink-tertiary">{{ t('security.startup.addHint') }}</p>
      <OnSearchField v-model="query" :placeholder="t('security.startup.addSearch')" />
      <div v-if="!localApps.loaded && !loadFailed" class="flex flex-col gap-8px" aria-hidden="true">
        <OnSkeleton v-for="index in 6" :key="index" height="44px" radius="default" />
      </div>
      <ul v-else-if="apps.length" class="m-0 list-none p-0">
        <li v-for="{ app, name, icon } in apps" :key="app.id">
          <button
            type="button"
            class="flex w-full cursor-pointer items-center gap-10px rounded-default border-none bg-transparent px-8px py-7px text-left font-sans outline-none hover:bg-surface-raised focus-visible:shadow-focus-ring disabled:cursor-default disabled:opacity-60"
            :disabled="adding !== null"
            @click="emit('add', app)"
          >
            <OnAppIcon v-bind="icon" :size="28" />
            <span class="min-w-0 flex-1">
              <span class="block truncate text-13px font-500 text-ink-primary">{{ name }}</span>
              <span class="block truncate text-11px text-ink-tertiary">{{ folder(app) }}</span>
            </span>
            <span v-if="adding === app.path" class="shrink-0 text-11.5px text-ink-tertiary">{{
              t('security.startup.adding')
            }}</span>
          </button>
        </li>
      </ul>
      <OnEmpty v-else :title="t('security.startup.addEmpty')" icon="lucide:search" />
    </div>
  </OnSheet>
</template>
