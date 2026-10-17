<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatDate } from '@opennavo/shared';
import {
  OnBreadcrumb,
  OnButton,
  OnEmpty,
  OnErrorState,
  OnKeyValueList,
  OnPackageHeader,
  OnScreenshotGallery,
  OnSkeleton
} from '@opennavo/ui';
import type { OnKeyValueItem, OnPackageHeaderChip } from '@opennavo/ui';
import MasGetButton from '@/components/appstore/MasGetButton.vue';
import { masRegionName } from '@/components/appstore/meta';
import { IpcError } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLoader } from '@/composables/useLoader';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useMasStore } from '@/stores/mas';

// App Store detail (store/security plan §5, mockup C): everything comes from one iTunes lookup in the user's storefront.
// Get opens the product page until the bundled helper can install (M2); uninstall stays in Installed.
const props = defineProps<{ id: string }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const mas = useMasStore();
const toasts = useToasts();
const { errorText } = usePackageState();
const adamId = computed(() => Number(props.id));

const {
  data: app,
  error,
  loading,
  reload
} = useLoader(async () => {
  await Promise.all([
    mas.status ? undefined : mas.loadStatus().catch(() => undefined),
    mas.loadInstalled().catch(() => undefined)
  ]);
  return mas.get(adamId.value);
}, [adamId]);

const region = computed(() => (mas.status ? masRegionName(mas.status.country, appLocale.value) : null));
const installed = computed(() => (app.value ? mas.installedFor(app.value.adamId) : null));

const chips = computed<OnPackageHeaderChip[]>(() => {
  const value = app.value;
  if (!value) return [];
  const items: OnPackageHeaderChip[] = [{ key: 'source', label: t('appStore.badge'), tone: 'info' }];
  items.push({ key: 'price', label: value.price > 0 ? value.formattedPrice : t('appStore.free') });
  if (value.minimumOs) items.push({ key: 'os', label: t('appStore.requiresOs', { version: value.minimumOs }) });
  if (installed.value)
    items.push({
      key: 'installed',
      label: t('appStore.installedVersion', { version: installed.value.version }),
      tone: 'success',
      dot: true
    });
  return items;
});

const subtitle = computed(() => {
  const value = app.value;
  if (!value) return '';
  const rating =
    value.rating != null && value.ratingCount
      ? t(
          'appStore.rating',
          { rating: value.rating.toFixed(1), count: value.ratingCount },
          { plural: value.ratingCount }
        )
      : null;
  return [value.seller, value.genres[0], rating].filter(Boolean).join(' · ');
});

const shots = computed(() => (app.value?.screenshots ?? []).map(src => ({ src })));

const info = computed<OnKeyValueItem[]>(() => {
  const value = app.value;
  if (!value) return [];
  const items: OnKeyValueItem[] = [
    { key: 'seller', label: t('appStore.info.seller'), value: value.seller },
    { key: 'version', label: t('appStore.info.version'), value: value.version }
  ];
  if (value.sizeBytes)
    items.push({
      key: 'size',
      label: t('appStore.info.size'),
      value: formatBytes(value.sizeBytes, { locale: appLocale.value })
    });
  if (value.minimumOs)
    items.push({
      key: 'os',
      label: t('appStore.info.requires'),
      value: t('appStore.requiresOs', { version: value.minimumOs })
    });
  if (value.genres.length)
    items.push({ key: 'genre', label: t('appStore.info.category'), value: value.genres.join(' · ') });
  if (region.value) items.push({ key: 'region', label: t('appStore.info.region'), value: region.value });
  items.push({ key: 'bundle', label: t('appStore.info.bundleId'), value: value.bundleId, mono: true });
  return items;
});

const breadcrumb = computed(() => [
  { label: t('appStore.title'), href: '/search?source=appstore' },
  { label: app.value?.name ?? props.id }
]);

const missing = computed(() => !loading.value && !error.value && app.value === null);
const failure = computed(() =>
  error.value instanceof IpcError && error.value.code === 'E_MAS_RATE_LIMITED'
    ? t('appStore.rateLimited')
    : error.value instanceof IpcError && error.value.code === 'E_NETWORK'
      ? t('appStore.offline')
      : undefined
);

async function openStore() {
  if (!app.value) return;
  try {
    await mas.openStore(app.value.adamId);
  } catch (cause) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(cause) });
  }
}
</script>

<template>
  <div class="flex flex-col gap-14px pt-6px">
    <OnBreadcrumb :items="breadcrumb" :link-as="RouterLink" />
    <template v-if="app">
      <OnPackageHeader
        kind="mas"
        :token="String(app.adamId)"
        :name="app.name"
        :subtitle="subtitle"
        :icon-url="app.iconUrl"
        :chips="chips"
        :meta="t('appStore.headerMeta')"
        priority
      >
        <template #actions>
          <MasGetButton :app="app" appearance="button" />
          <OnButton variant="secondary" icon="arrow-up-right" @click="openStore">{{
            t('appStore.viewInStore')
          }}</OnButton>
        </template>
      </OnPackageHeader>
      <div class="grid grid-cols-1 gap-20px pt-4px lg:grid-cols-[minmax(0,1fr)_320px]">
        <div class="flex min-w-0 flex-col gap-16px">
          <OnScreenshotGallery v-if="shots.length" :items="shots" :label="t('package.screenshots')" />
          <section
            v-if="app.releaseNotes"
            class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-14px"
            aria-labelledby="mas-whats-new"
          >
            <h2 id="mas-whats-new" class="m-0 text-13.5px font-600 text-ink-primary">
              {{
                app.releasedAt
                  ? t('appStore.whatsNewDated', {
                      version: app.version,
                      date: formatDate(app.releasedAt, { locale: appLocale })
                    })
                  : t('appStore.whatsNew', { version: app.version })
              }}
            </h2>
            <p class="m-0 mt-8px whitespace-pre-line text-13px leading-[1.65] text-ink-secondary">
              {{ app.releaseNotes }}
            </p>
          </section>
          <section aria-labelledby="mas-about">
            <h2 id="mas-about" class="m-0 text-headline text-ink-primary">{{ t('appStore.about') }}</h2>
            <p class="m-0 mt-8px whitespace-pre-line text-14px leading-[1.7] text-ink-secondary">
              {{ app.description }}
            </p>
          </section>
        </div>
        <aside class="flex min-w-0 flex-col gap-14px">
          <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-16px py-4px">
            <OnKeyValueList :items="info" />
          </section>
          <p class="m-0 text-12px leading-[1.6] text-ink-tertiary">{{ t('appStore.privacyNote') }}</p>
        </aside>
      </div>
    </template>
    <OnEmpty
      v-else-if="missing"
      :title="t('appStore.notInRegion', { region: region ?? '' })"
      :description="t('appStore.notInRegionHint')"
      icon="lucide:package"
    />
    <OnErrorState v-else-if="error" :description="failure" retryable @retry="reload()" />
    <div v-else class="flex flex-col gap-14px" aria-hidden="true">
      <OnSkeleton height="146px" radius="huge" />
      <OnSkeleton height="320px" radius="big" />
    </div>
  </div>
</template>
