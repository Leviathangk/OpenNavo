<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { RouterLink, useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { DESKTOP_LOCALES, LOCALE_METADATA, formatRelativeTime } from '@opennavo/shared';
import {
  OnModal,
  OnAboutContent,
  OnButton,
  OnCard,
  OnChip,
  OnPageHeader,
  OnMenu,
  OnSegmented,
  OnToggle
} from '@opennavo/ui';
import AppStoreSettings from '@/components/settings/AppStoreSettings.vue';
import CleanupSettings from '@/components/settings/CleanupSettings.vue';
import SecuritySettings from '@/components/settings/SecuritySettings.vue';
import MonitorSettings from '@/components/settings/MonitorSettings.vue';
import MirrorSettings from '@/components/settings/MirrorSettings.vue';
import AppUpdater from '@/components/settings/AppUpdater.vue';
import SettingRow from '@/components/settings/SettingRow.vue';
import PermissionGate from '@/components/permissions/PermissionGate.vue';
import RelaunchNotice from '@/components/permissions/RelaunchNotice.vue';
import type { AppInfo } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import type { AppSettings } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { fetchClientConfig } from '@/composables/useClientConfig';
import { useLoader } from '@/composables/useLoader';
import { useToasts } from '@/composables/useToasts';
import { useCatalogStore, useEnvStore, usePermissionsStore, useSettingsStore } from '@/stores';
import type { GuidePane } from '@/stores/permissions';
import { openExternal } from '@/utils/external';

// Settings (06 §11): General, Sources, Homebrew, Clean Up, Privacy, About; save the full object on every change (settings_set).
type Section =
  | 'general'
  | 'mirrors'
  | 'homebrew'
  | 'appstore'
  | 'cleanup'
  | 'monitor'
  | 'security'
  | 'privacy'
  | 'about';

const { t } = useI18n();
const { appLocale } = useAppLocale();
const route = useRoute();
const settings = useSettingsStore();
const env = useEnvStore();
const catalog = useCatalogStore();
const toasts = useToasts();
const syncingCatalog = ref(false);
async function syncCatalog() {
  if (syncingCatalog.value) return;
  syncingCatalog.value = true;
  try {
    await catalog.sync(true);
    // The casks synced, but the links source can still be down: say so instead of reporting a plain success.
    if (catalog.status?.linksError) toasts.push({ tone: 'danger', title: t('search.catalogIncomplete') });
  } catch {
    toasts.push({ tone: 'danger', title: t('common.refreshFailed') });
  } finally {
    syncingCatalog.value = false;
  }
}

const followSystemLabel = computed(() =>
  t('settings.general.followSystem', { language: LOCALE_METADATA[appLocale.value].name })
);
const languageItems = computed(() => [
  { key: 'system', label: followSystemLabel.value },
  ...DESKTOP_LOCALES.map(code => ({ key: code, label: LOCALE_METADATA[code].name }))
]);

// Appearance: System follows macOS; Rust applies it to every window (NSApp appearance) and the main window mirrors it.
const appearances = computed(() => [
  { value: 'system' as const, label: t('settings.general.appearanceSystem') },
  { value: 'light' as const, label: t('settings.general.appearanceLight') },
  { value: 'dark' as const, label: t('settings.general.appearanceDark') }
]);

const section = computed<Section>(() => (route.params.section as Section | undefined) || 'general');
const sections: Section[] = [
  'general',
  'mirrors',
  'homebrew',
  'appstore',
  'cleanup',
  'monitor',
  'security',
  'privacy',
  'about'
];

async function save<K extends keyof AppSettings>(key: K, value: AppSettings[K]) {
  await settings.update(key, value);
}

function selectLanguage(value: string) {
  if (value === 'system') {
    void settings.setLanguage('system');
    return;
  }
  const locale = DESKTOP_LOCALES.find(code => code === value);
  if (locale) void settings.setLanguage(locale);
}

// Privacy permissions (06 §12.3, §12.6): Enable opens System Settings and guidance; state updates live.
// A permission already on in System Settings but awaiting a reopen offers no Enable: the notice below reopens OpenNavo.
const permissions = usePermissionsStore();
const privacyRows = computed(() =>
  (
    [
      { pane: 'app_management', key: 'appManagement', state: permissions.appManagement },
      { pane: 'full_disk_access', key: 'fullDiskAccess', state: permissions.fullDiskAccess }
    ] as const
  ).map(row => ({ ...row, pending: permissions.pending(row.pane), waiting: permissions.guiding === row.pane }))
);
async function enablePermission(pane: GuidePane) {
  try {
    await permissions.guide(pane);
  } catch {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
  }
}

const general = computed(() => {
  const value = settings.value;
  if (!value) return [];
  return [
    {
      key: 'launchAtLogin' as const,
      label: t('settings.general.launchAtLogin'),
      hint: t('settings.general.launchAtLoginHint'),
      value: value.launchAtLogin
    },
    {
      key: 'keepInMenuBarOnClose' as const,
      label: t('settings.general.keepInMenuBar'),
      value: value.keepInMenuBarOnClose
    },
    {
      key: 'trayShowCount' as const,
      label: t('settings.general.trayShowCount'),
      value: value.trayShowCount
    },
    {
      key: 'notifyUpdates' as const,
      label: t('settings.general.notifyUpdates'),
      value: value.notifyUpdates
    }
  ];
});

// Uninstalling (uninstall decision 2): related files are suggested by default and go to the Trash unless chosen otherwise.
const deleteModes = computed(() => [
  { value: 'trash' as const, label: t('uninstall.mode.trash') },
  { value: 'delete' as const, label: t('uninstall.mode.delete') }
]);

const checks = computed(() => {
  const value = settings.value;
  if (!value) return [];
  return [
    { key: 'autoCheck' as const, label: t('settings.general.autoCheck'), value: value.autoCheck },
    {
      key: 'autoCheckAppUpdates' as const,
      label: t('updater.autoCheck'),
      hint: t('updater.autoCheckHint'),
      value: value.autoCheckAppUpdates
    },
    {
      key: 'runBrewUpdateOnCheck' as const,
      label: t('settings.general.runBrewUpdate'),
      hint: t('settings.general.runBrewUpdateHint'),
      value: value.runBrewUpdateOnCheck
    },
    {
      key: 'includeGreedy' as const,
      label: t('updates.auto.greedy'),
      hint: t('updates.auto.greedyHint'),
      value: value.includeGreedy
    },
    {
      key: 'autoUpgradeCasks' as const,
      label: t('updates.auto.casks'),
      hint: t('updates.auto.casksHint'),
      value: value.autoUpgradeCasks
    }
  ];
});

function onCheckTime(event: Event) {
  const value = (event.target as HTMLInputElement).value;
  if (value) void save('checkTime', value);
}

const brewPath = ref('');
onMounted(() => {
  brewPath.value = settings.value?.brewPath ?? '';
});

async function saveBrewPath() {
  await save('brewPath', brewPath.value.trim() || null);
  await env.detect();
  toasts.push({ tone: 'success', title: t('settings.saved') });
}

const appInfo = ref<AppInfo | null>(null);
const openSourceOpen = ref(false);
const { data: config } = useLoader(fetchClientConfig, [appLocale]);
onMounted(async () => {
  appInfo.value = await unwrap(commands.appInfo()).catch(() => null);
});
</script>

<template>
  <div>
    <OnPageHeader :title="t('settings.title')" />
    <div class="grid grid-cols-[180px_minmax(0,1fr)] gap-24px">
      <nav class="flex flex-col gap-2px" :aria-label="t('settings.title')">
        <RouterLink
          v-for="item in sections"
          :key="item"
          :to="`/settings/${item}`"
          class="rounded-default px-12px py-8px text-13.5px no-underline outline-none focus-visible:shadow-focus-ring"
          :class="
            section === item
              ? 'bg-component-item-active font-600 text-ink-primary'
              : 'text-ink-secondary hover:text-ink-primary'
          "
          :aria-current="section === item ? 'page' : undefined"
        >
          {{ t(`settings.sections.${item}`) }}
        </RouterLink>
      </nav>

      <div class="min-w-0 max-w-720px">
        <template v-if="section === 'general' && settings.value">
          <OnCard padding="none">
            <SettingRow
              :label="t('settings.general.language')"
              :hint="t('settings.general.systemDialogsRestartHint')"
              label-id="setting-language"
            >
              <OnMenu :label="t('settings.general.language')" :items="languageItems" @select="selectLanguage">
                <template #trigger="{ attrs }">
                  <OnButton v-bind="attrs" size="sm" icon="globe">
                    {{ settings.value.localeMode === 'system' ? followSystemLabel : LOCALE_METADATA[appLocale].name }}
                  </OnButton>
                </template>
              </OnMenu>
            </SettingRow>
            <SettingRow :label="t('settings.general.appearance')" label-id="setting-appearance">
              <OnSegmented
                :model-value="settings.value.appearance"
                :options="appearances"
                size="sm"
                :aria-label="t('settings.general.appearance')"
                @update:model-value="value => save('appearance', value)"
              />
            </SettingRow>
            <SettingRow
              v-for="item in general"
              :key="item.key"
              :label="item.label"
              :hint="item.hint"
              :label-id="`setting-${item.key}`"
            >
              <OnToggle
                :model-value="item.value"
                :aria-labelledby="`setting-${item.key}`"
                @update:model-value="value => save(item.key, value)"
              />
            </SettingRow>
          </OnCard>
          <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">
            {{ t('settings.general.uninstall') }}
          </h2>
          <OnCard padding="none">
            <SettingRow
              :label="t('settings.general.zapByDefault')"
              :hint="t('settings.general.zapByDefaultHint')"
              label-id="setting-zapByDefault"
            >
              <OnToggle
                :model-value="settings.value.zapByDefault"
                aria-labelledby="setting-zapByDefault"
                @update:model-value="value => save('zapByDefault', value)"
              />
            </SettingRow>
            <SettingRow
              :label="t('settings.general.deleteMode')"
              :hint="t('settings.general.deleteModeHint')"
              label-id="setting-uninstallDeleteMode"
            >
              <OnSegmented
                :model-value="settings.value.uninstallDeleteMode"
                :options="deleteModes"
                size="sm"
                :aria-label="t('settings.general.deleteMode')"
                @update:model-value="value => save('uninstallDeleteMode', value)"
              />
            </SettingRow>
          </OnCard>
          <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">
            {{ t('settings.general.updates') }}
          </h2>
          <OnCard padding="none">
            <SettingRow :label="t('settings.general.checkTime')" label-id="setting-checkTime">
              <input
                type="time"
                :value="settings.value.checkTime"
                aria-labelledby="setting-checkTime"
                class="h-30px rounded-small border border-solid border-line-default bg-surface-control px-8px font-sans text-13px text-ink-primary outline-none focus-visible:shadow-focus-ring"
                @change="onCheckTime"
              />
            </SettingRow>
            <SettingRow
              v-for="item in checks"
              :key="item.key"
              :label="item.label"
              :hint="item.hint"
              :label-id="`setting-${item.key}`"
            >
              <OnToggle
                :model-value="item.value"
                :aria-labelledby="`setting-${item.key}`"
                @update:model-value="value => save(item.key, value)"
              />
            </SettingRow>
          </OnCard>
        </template>

        <MirrorSettings v-else-if="section === 'mirrors'" />

        <OnCard v-else-if="section === 'homebrew'" padding="none">
          <template v-if="env.info?.brew">
            <SettingRow :label="t('settings.homebrew.version')">
              <span class="font-mono text-13px text-ink-secondary">{{ env.info.brew.version }}</span>
            </SettingRow>
            <SettingRow :label="t('settings.homebrew.prefix')">
              <span class="font-mono text-13px text-ink-secondary">{{ env.info.brew.prefix }}</span>
            </SettingRow>
          </template>
          <SettingRow v-else :label="t('settings.homebrew.missing')">
            <OnButton variant="primary" size="sm" href="/welcome" :link-as="RouterLink">{{
              t('settings.homebrew.install')
            }}</OnButton>
          </SettingRow>
          <SettingRow :label="t('settings.homebrew.path')" :hint="env.info?.brew?.path" label-id="setting-brewPath">
            <input
              v-model="brewPath"
              type="text"
              :placeholder="t('settings.homebrew.auto')"
              aria-labelledby="setting-brewPath"
              class="h-30px w-220px rounded-small border border-solid border-line-default bg-surface-control px-8px font-mono text-12.5px text-ink-primary outline-none focus-visible:shadow-focus-ring"
              @change="saveBrewPath"
            />
          </SettingRow>
          <SettingRow v-if="settings.value" :label="t('settings.homebrew.analytics')" label-id="setting-analytics">
            <OnSegmented
              :model-value="settings.value.homebrewAnalytics === false ? 'off' : 'follow'"
              size="sm"
              aria-labelledby="setting-analytics"
              :options="[
                { value: 'follow', label: t('settings.homebrew.analyticsFollow') },
                { value: 'off', label: t('settings.homebrew.analyticsOff') }
              ]"
              @update:model-value="value => save('homebrewAnalytics', value === 'off' ? false : null)"
            />
          </SettingRow>
        </OnCard>

        <AppStoreSettings v-else-if="section === 'appstore'" />

        <PermissionGate v-else-if="section === 'cleanup'" feature="cleanup" variant="section"
          ><CleanupSettings
        /></PermissionGate>

        <PermissionGate v-else-if="section === 'monitor'" feature="monitor" variant="section"
          ><MonitorSettings
        /></PermissionGate>

        <PermissionGate v-else-if="section === 'security'" feature="security" variant="section"
          ><SecuritySettings
        /></PermissionGate>

        <OnCard v-else-if="section === 'privacy'" padding="none">
          <SettingRow
            v-if="settings.value"
            :label="t('settings.privacy.crashReports')"
            :hint="t('settings.privacy.crashReportsHint')"
            label-id="setting-crashReports"
          >
            <OnToggle
              :model-value="settings.value.crashReports"
              aria-labelledby="setting-crashReports"
              @update:model-value="value => save('crashReports', value)"
            />
          </SettingRow>
          <SettingRow
            v-for="row in privacyRows"
            :key="row.pane"
            :label="t(`settings.privacy.${row.key}`)"
            :hint="t(`settings.privacy.${row.key}Hint`)"
          >
            <OnChip v-if="row.pending" tone="warning" dot>{{ t('permission.relaunch.pending') }}</OnChip>
            <OnChip v-else :tone="row.state === 'granted' ? 'success' : 'neutral'" dot>
              {{ t(`settings.privacy.${row.state}`) }}
            </OnChip>
            <OnButton
              v-if="row.state !== 'granted' && !row.pending"
              variant="secondary"
              size="sm"
              :loading="row.waiting"
              :aria-label="row.waiting ? undefined : t(`welcome.permissions.${row.key}.enable`)"
              @click="enablePermission(row.pane)"
            >
              {{ row.waiting ? t('settings.privacy.waiting') : t('settings.privacy.enable') }}
            </OnButton>
          </SettingRow>
          <div v-if="permissions.relaunchRequired" class="px-18px pb-14px">
            <RelaunchNotice />
          </div>
        </OnCard>

        <div v-else-if="section === 'about'" class="flex min-w-0 flex-col gap-28px">
          <OnCard padding="none">
            <SettingRow
              label="OpenNavo"
              :hint="appInfo ? t('settings.about.version', { version: appInfo.version }) : undefined"
            >
              <template v-if="config">
                <OnButton variant="ghost" size="sm" icon="arrow-up-right" @click="openExternal(config.links.web)">{{
                  t('settings.about.website')
                }}</OnButton>
                <OnButton
                  variant="ghost"
                  size="sm"
                  icon="arrow-up-right"
                  @click="openExternal(config.links.feedback)"
                  >{{ t('settings.about.feedback') }}</OnButton
                >
                <OnButton variant="ghost" size="sm" icon="arrow-up-right" @click="openExternal(config.links.privacy)">{{
                  t('settings.about.privacy')
                }}</OnButton>
              </template>
            </SettingRow>
            <AppUpdater :current-version="appInfo?.version ?? null" />
            <SettingRow
              v-if="catalog.status"
              :label="
                t(
                  'settings.about.catalog',
                  {
                    count: catalog.status.itemCount,
                    time: catalog.status.syncedAt
                      ? formatRelativeTime(catalog.status.syncedAt, { locale: appLocale })
                      : '—'
                  },
                  { plural: catalog.status.itemCount }
                )
              "
            >
              <OnButton
                variant="secondary"
                size="sm"
                icon="refresh-cw"
                :disabled="syncingCatalog"
                @click="syncCatalog"
                >{{ t('settings.about.sync') }}</OnButton
              >
            </SettingRow>
            <SettingRow :label="t('settings.about.openSource')">
              <OnButton variant="secondary" size="sm" :disabled="!config?.about" @click="openSourceOpen = true">
                {{ t('settings.about.view') }}
              </OnButton>
            </SettingRow>
          </OnCard>
          <OnModal v-model:open="openSourceOpen" :title="t('settings.about.openSource')" size="lg">
            <OnAboutContent
              v-if="config?.about"
              :content="{
                sections: config.about.sections.filter(item => item.key === 'openSource'),
                modules: config.about.modules
              }"
              :draft-label="t('settings.about.draft')"
            />
          </OnModal>
        </div>
      </div>
    </div>
  </div>
</template>
