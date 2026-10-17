<script setup lang="ts">
import { computed, shallowRef, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnAppRow } from '@opennavo/ui';
import type { MasApp } from '@/ipc/bindings';
import { useSettingsStore } from '@/stores';
import { useMasStore } from '@/stores/mas';
import MasGetButton from './MasGetButton.vue';

// "Also available" on catalog detail pages (store/security plan §5, mockup A): the same app in the App Store, looked up
// by bundle ID straight from this Mac, so price and availability follow the user's storefront. Misses and failures
// (rate limit, offline) hide the panel; with App Store results turned off in Settings nothing is asked at all.
const props = defineProps<{ bundleIds: readonly string[] }>();

const { t } = useI18n();
const mas = useMasStore();
const settings = useSettingsStore();
const app = shallowRef<MasApp | null>(null);

const enabled = computed(() => settings.value?.appStoreSearch ?? true);
const ids = computed(() => [...new Set(props.bundleIds)].filter(Boolean).slice(0, 10));

let sequence = 0;
watch(
  [() => ids.value.join(' '), enabled],
  async ([key, on]) => {
    const current = ++sequence;
    app.value = null;
    if (!on || !key) return;
    try {
      void mas.ensureStatus().catch(() => undefined);
      const found = await mas.byBundles(ids.value);
      if (current !== sequence) return;
      app.value = ids.value.map(id => found.get(id)).find(Boolean) ?? null;
      if (app.value && !mas.installed.length) void mas.loadInstalled().catch(() => undefined);
    } catch {
      // The panel is optional; the rest of the page never waits on Apple.
    }
  },
  { immediate: true }
);

const meta = computed(() => {
  const value = app.value;
  if (!value) return '';
  return [value.price > 0 ? value.formattedPrice : t('appStore.free'), value.seller].filter(Boolean).join(' · ');
});
</script>

<template>
  <section
    v-if="app"
    class="flex flex-col gap-6px rounded-big border border-solid border-line-subtle bg-surface-card px-14px py-12px"
    aria-labelledby="mas-also"
  >
    <h2 id="mas-also" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('appStore.alsoTitle') }}
    </h2>
    <OnAppRow
      kind="mas"
      :token="String(app.adamId)"
      :name="t('appStore.title')"
      :src="app.iconUrl"
      :meta="meta"
      :href="`/appstore/${app.adamId}`"
      :link-as="RouterLink"
    >
      <template #actions>
        <MasGetButton :app="app" />
      </template>
    </OnAppRow>
  </section>
</template>
