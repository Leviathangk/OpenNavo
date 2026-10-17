<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnAppRow, OnButton, OnChip, OnEmpty, OnSectionHeader, OnSkeleton } from '@opennavo/ui';
import type { MasApp } from '@/ipc/bindings';
import MasGetButton from './MasGetButton.vue';
import { masRegionName } from './meta';
import { useAppLocale } from '@/composables/useAppLocale';

// App Store group on the search page (store/security plan §5, mockup B): separate from the catalog list because these
// rows come from Apple, not from our catalog. Shows six at first; the rest of the fetched page expands in place.
const props = defineProps<{
  results: MasApp[] | null;
  query: string;
  loading: boolean;
  /** E_* code of the last failure for this query. */
  error: string | null;
  country: string | null;
  /** Only this group is shown (source filter), so it has room for every result. */
  expanded?: boolean;
}>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const showAll = ref(false);
watch(
  () => props.query,
  () => (showAll.value = false)
);

const visible = computed(() =>
  props.results ? (props.expanded || showAll.value ? props.results : props.results.slice(0, 6)) : null
);
const hint = computed(() =>
  props.country
    ? t('appStore.fromApple', { region: masRegionName(props.country, appLocale.value) })
    : t('appStore.fromAppleNoRegion')
);
const meta = (app: MasApp) =>
  [
    app.seller,
    app.rating != null && app.ratingCount
      ? t('appStore.rating', { rating: app.rating.toFixed(1), count: app.ratingCount }, { plural: app.ratingCount })
      : null
  ]
    .filter(Boolean)
    .join(' · ');
const errorText = computed(() =>
  props.error === 'E_MAS_RATE_LIMITED'
    ? t('appStore.rateLimited')
    : props.error === 'E_NETWORK'
      ? t('appStore.offline')
      : props.error
        ? t('appStore.searchFailed')
        : null
);
</script>

<template>
  <section class="mt-28px" :aria-busy="loading ? 'true' : 'false'">
    <OnSectionHeader :title="t('appStore.title')" :hint="hint" />
    <p v-if="errorText" class="m-0 mb-12px text-13px text-ink-secondary" role="status">{{ errorText }}</p>
    <div v-if="visible === null && loading" class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 3" :key="index" height="64px" radius="default" />
    </div>
    <ul v-else-if="visible?.length" class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
      <li
        v-for="app in visible"
        :key="app.adamId"
        class="border-t border-t-solid border-line-subtle px-14px first:border-t-0"
      >
        <OnAppRow
          kind="mas"
          :token="String(app.adamId)"
          :name="app.name"
          :src="app.iconUrl"
          :meta="meta(app)"
          :description="app.description"
          :href="`/appstore/${app.adamId}`"
          :link-as="RouterLink"
        >
          <template #badges>
            <OnChip tone="info">{{ t('appStore.badge') }}</OnChip>
          </template>
          <template #actions>
            <MasGetButton :app="app" />
          </template>
        </OnAppRow>
      </li>
    </ul>
    <OnEmpty
      v-else-if="visible && !errorText"
      :title="t('appStore.empty', { q: query })"
      :description="t('appStore.emptyHint')"
      icon="lucide:search"
    />
    <div v-if="!expanded && !showAll && (results?.length ?? 0) > 6" class="mt-10px">
      <OnButton variant="ghost" size="sm" @click="showAll = true">
        {{ t('appStore.showAll', { count: results?.length ?? 0 }, { plural: results?.length ?? 0 }) }}
      </OnButton>
    </div>
  </section>
</template>
