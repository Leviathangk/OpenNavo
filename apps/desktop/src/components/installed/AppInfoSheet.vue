<script setup lang="ts">
import { computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatDate, formatRelativeTime } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnChip, OnIcon, OnKeyValueList, OnSheet, OnStackBar } from '@opennavo/ui';
import type { OnKeyValueItem } from '@opennavo/ui';
import type { LocalApp } from '@/ipc/bindings';
import { useLocalAppsStore } from '@/stores/localApps';
import { usePermissionsStore } from '@/stores';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLocalAppMenu } from '@/composables/useLocalAppMenu';
import { usePackageIdentity } from '@/composables/usePackageIdentity';

// Installed row details (decision 1): local facts first; catalog apps link to their OpenNavo page,
// drag-installed ones offer Homebrew adoption. Sizes come from a size-only scan (no uninstall plan).
const props = defineProps<{ app: LocalApp | null }>();
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{
  uninstall: [app: LocalApp];
  detail: [token: string];
}>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { localApp } = usePackageIdentity();
const menu = useLocalAppMenu();
const localApps = useLocalAppsStore();
const permissions = usePermissionsStore();

const identity = computed(() => (props.app ? localApp(props.app) : null));
const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });

const footprint = computed(() => (open.value && props.app ? (localApps.footprints[props.app.id] ?? null) : null));
watch(
  () => (open.value ? props.app?.id : undefined),
  id => {
    if (id) void localApps.footprint(id);
  },
  { immediate: true }
);

const parts = computed(() => {
  const value = footprint.value;
  if (!value) return [];
  return [
    {
      key: 'app',
      label: t('appInfo.appBytes'),
      value: value.appBytes,
      display: format(value.appBytes)
    },
    {
      key: 'data',
      label: t('appInfo.dataBytes'),
      value: value.dataBytes,
      display: format(value.dataBytes)
    },
    {
      key: 'cache',
      label: t('appInfo.cacheBytes'),
      value: value.cacheBytes,
      display: format(value.cacheBytes)
    }
  ];
});
const total = computed(() =>
  footprint.value ? footprint.value.appBytes + footprint.value.dataBytes + footprint.value.cacheBytes : null
);

const fields = computed<OnKeyValueItem[]>(() => {
  const app = props.app;
  if (!app) return [];
  const unknown = t('appInfo.unknown');
  const date = (value: number | null, relative = false) =>
    value === null
      ? unknown
      : relative
        ? formatRelativeTime(value, { locale: appLocale.value })
        : formatDate(value, { locale: appLocale.value });
  return [
    {
      key: 'path',
      label: t('appInfo.fields.path'),
      value: app.path,
      mono: true
    },
    {
      key: 'bundleId',
      label: t('appInfo.fields.bundleId'),
      value: app.bundleId ?? unknown,
      mono: Boolean(app.bundleId)
    },
    {
      key: 'version',
      label: t('appInfo.fields.version'),
      value: app.version ?? unknown
    },
    {
      key: 'vendor',
      label: t('appInfo.fields.vendor'),
      value: [app.vendor, app.teamId].filter(Boolean).join(' · ') || unknown
    },
    {
      key: 'arch',
      label: t('appInfo.fields.arch'),
      value: app.arch ? t(`appInfo.arch.${app.arch}`) : unknown
    },
    {
      key: 'source',
      label: t('appInfo.fields.source'),
      value: t(`library.sources.${app.source}`)
    },
    {
      key: 'installedAt',
      label: t('appInfo.fields.installedAt'),
      value: date(app.installedAt)
    },
    {
      key: 'lastUsed',
      label: t('appInfo.fields.lastUsed'),
      value: date(app.lastUsedAt, true)
    }
  ];
});

const catalogToken = computed(() => props.app?.cask ?? props.app?.catalogToken ?? null);
// Only drag-installed and .pkg apps: an App Store copy stays with the App Store (Homebrew would replace its build).
const adoptable = computed(() =>
  Boolean(props.app?.catalogToken && (props.app.source === 'direct' || props.app.source === 'pkg'))
);
const partialNote = computed(() =>
  permissions.fullDiskAccess === 'granted' ? t('appInfo.footprintPartial') : t('appInfo.footprintIncomplete')
);
</script>

<template>
  <OnSheet v-model:open="open" :title="t('appInfo.title')">
    <div v-if="app && identity" class="flex flex-col gap-14px">
      <div class="flex items-start gap-14px">
        <OnAppIcon v-bind="identity.icon" :size="54" />
        <div class="min-w-0 flex-1">
          <div class="truncate text-18px font-600 tracking-[-0.01em] text-ink-primary">
            {{ identity.name }}
          </div>
          <div class="mb-10px mt-2px truncate text-12.5px text-ink-tertiary">
            {{ [app.vendor, t(`library.sources.${app.source}`)].filter(Boolean).join(' · ') }}
          </div>
          <div class="flex flex-wrap gap-6px">
            <OnButton size="xs" icon="arrow-up-right" @click="menu.select(app, identity.name, 'open')">{{
              t('appInfo.open')
            }}</OnButton>
            <OnButton size="xs" icon="folder" @click="menu.select(app, identity.name, 'reveal')">{{
              t('appInfo.reveal')
            }}</OnButton>
            <OnButton v-if="catalogToken" size="xs" variant="ghost" @click="emit('detail', catalogToken)">{{
              t('appInfo.detail')
            }}</OnButton>
          </div>
        </div>
      </div>

      <section
        v-if="adoptable"
        class="rounded-big border border-solid border-[rgba(102,164,250,0.25)] bg-status-info-subtle px-16px py-14px"
      >
        <h3 class="m-0 mb-4px flex items-center gap-7px text-13.5px font-600 text-status-info">
          <OnIcon name="sparkles" :size="14" />{{ t('appInfo.adoptTitle') }}
        </h3>
        <p class="m-0 mb-10px text-12.5px leading-[1.55] text-ink-secondary">
          {{ t('appInfo.adoptBody', { name: identity.name }) }}
        </p>
        <OnButton size="xs" @click="menu.adopt(app)">{{ t('appInfo.adopt') }}</OnButton>
      </section>

      <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px">
        <h3 class="m-0 mb-10px flex items-center gap-8px text-14px font-600 text-ink-primary">
          {{ t('appInfo.footprint') }}
          <OnChip v-if="footprint" size="sm">{{ t('appInfo.estimate') }}</OnChip>
        </h3>
        <p class="m-0 mb-12px text-26px font-600 leading-none text-ink-primary">
          {{ total === null ? '—' : format(total) }}
        </p>
        <OnStackBar v-if="parts.length" :items="parts" :caption="t('appInfo.footprintCaption')" :format="format" />
        <p v-if="footprint && !footprint.complete" class="m-0 mt-10px text-12px text-ink-tertiary">
          {{ partialNote }}
        </p>
      </section>

      <OnKeyValueList :items="fields" />
    </div>
    <template #footer>
      <template v-if="app">
        <span v-if="!app.removable" class="mr-auto self-center text-12px leading-[1.5] text-ink-tertiary">{{
          t(`appInfo.protected.${app.protectedReason ?? 'system'}`)
        }}</span>
        <OnButton v-else variant="danger" size="sm" icon="trash" @click="emit('uninstall', app)">{{
          t('appInfo.uninstall')
        }}</OnButton>
      </template>
    </template>
  </OnSheet>
</template>
