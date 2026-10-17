<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatDate, formatTime } from '@opennavo/shared';
import { OnButton, OnCard, OnMenu, OnSegmented, OnToggle } from '@opennavo/ui';
import SettingRow from './SettingRow.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import type { SecuritySettings } from '@/ipc/bindings';
import { useSecurityStore } from '@/stores/security';
import { displayPath } from '@/utils/paths';
import { pickFolder } from '@/utils/files';

// Settings → Security (store/security plan §6): database update interval (never below the official one hour),
// keeping the engine loaded, the S1 protections, scanning before installs, scheduled scans (S2), folders scans
// skip, and the two optional features that stay off until turned on (D8): paste protection and VirusTotal.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const security = useSecurityStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const value = computed(() => security.securitySettings);
const intervals = computed(() =>
  ([4, 12, 24] as const).map(hours => ({
    value: String(hours),
    label: t('security.settings.everyHours', { count: hours }, { plural: hours })
  }))
);

async function save<K extends keyof SecuritySettings>(key: K, entry: SecuritySettings[K]) {
  try {
    await security.setSecuritySetting(key, entry);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Scheduled scans: a quick scan at a set time, daily or on one weekday; Rust skips it on battery or without a database.
const schedules = computed(() =>
  (['off', 'daily', 'weekly'] as const).map(key => ({ value: key, label: t(`security.settings.schedule.${key}`) }))
);
// 2024-01-01 was a Monday, so day n of that week is weekday n (Monday = 1).
const weekdayName = (day: number) =>
  new Intl.DateTimeFormat(appLocale.value, { weekday: 'long' }).format(new Date(2024, 0, day));
const weekdays = computed(() => [1, 2, 3, 4, 5, 6, 7].map(day => ({ key: String(day), label: weekdayName(day) })));
const scheduleLine = computed(() => {
  const status = security.schedule;
  if (!status || value.value.avSchedule === 'off') return '';
  const parts: string[] = [];
  if (status.nextRunAt)
    parts.push(
      t('security.settings.schedule.next', {
        time: `${formatDate(status.nextRunAt, { locale: appLocale.value })} ${formatTime(status.nextRunAt)}`
      })
    );
  const last = status.lastRun;
  if (last?.skipped)
    parts.push(
      t('security.settings.schedule.skipped', { reason: t(`security.settings.schedule.reason.${last.skipped}`) })
    );
  return parts.join(' · ');
});
watch(
  () => [value.value.avSchedule, value.value.avScheduleTime, value.value.avScheduleWeekday],
  () => void security.loadSchedule().catch(() => undefined),
  { immediate: true }
);

function onScheduleTime(event: Event) {
  const time = (event.target as HTMLInputElement).value;
  if (time) void save('avScheduleTime', time);
}

// Optional features (D8): statuses come from Rust; Accessibility is requested only on this click.
onMounted(() => {
  void security.loadPasteStatus().catch(() => undefined);
  void security.loadVirusTotal().catch(() => undefined);
});
watch(
  () => value.value.pasteProtection,
  () => void security.loadPasteStatus().catch(() => undefined)
);

async function grantAccessibility() {
  try {
    await security.requestPasteAccess();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

const virusTotalKey = ref('');
async function saveVirusTotalKey(key: string | null) {
  try {
    await security.setVirusTotalKey(key);
    virusTotalKey.value = '';
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function addExclusion() {
  const path = await pickFolder();
  if (path && !value.value.avExclusions.includes(path)) await save('avExclusions', [...value.value.avExclusions, path]);
}
</script>

<template>
  <OnCard padding="none">
    <SettingRow
      :label="t('security.settings.interval')"
      :hint="t('security.settings.intervalHint')"
      label-id="setting-avDbCheckHours"
    >
      <OnSegmented
        :model-value="String(value.avDbCheckHours)"
        :options="intervals"
        size="sm"
        :aria-label="t('security.settings.interval')"
        @update:model-value="hours => save('avDbCheckHours', Number(hours))"
      />
    </SettingRow>
    <SettingRow
      :label="t('security.settings.resident')"
      :hint="t('security.settings.residentHint')"
      label-id="setting-avKeepResident"
    >
      <OnToggle
        :model-value="value.avKeepResident"
        aria-labelledby="setting-avKeepResident"
        @update:model-value="on => save('avKeepResident', on)"
      />
    </SettingRow>
    <SettingRow
      :label="t('security.protection.watchDownloads.title')"
      :hint="t('security.settings.watchDownloadsHint')"
      label-id="setting-watchDownloads"
    >
      <OnToggle
        :model-value="value.watchDownloads"
        aria-labelledby="setting-watchDownloads"
        @update:model-value="on => save('watchDownloads', on)"
      />
    </SettingRow>
    <SettingRow
      :label="t('security.protection.startupAlerts.title')"
      :hint="t('security.settings.startupAlertsHint')"
      label-id="setting-startupAlerts"
    >
      <OnToggle
        :model-value="value.startupAlerts"
        aria-labelledby="setting-startupAlerts"
        @update:model-value="on => save('startupAlerts', on)"
      />
    </SettingRow>
    <SettingRow
      :label="t('security.settings.beforeInstall')"
      :hint="t('security.settings.beforeInstallHint')"
      label-id="setting-avScanBeforeInstall"
    >
      <OnToggle
        :model-value="value.avScanBeforeInstall"
        aria-labelledby="setting-avScanBeforeInstall"
        @update:model-value="on => save('avScanBeforeInstall', on)"
      />
    </SettingRow>
  </OnCard>

  <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">{{ t('security.settings.schedule.title') }}</h2>
  <OnCard padding="none">
    <SettingRow
      :label="t('security.settings.schedule.frequency')"
      :hint="scheduleLine || t('security.settings.schedule.hint')"
      label-id="setting-avSchedule"
    >
      <OnSegmented
        :model-value="value.avSchedule"
        :options="schedules"
        size="sm"
        :aria-label="t('security.settings.schedule.frequency')"
        @update:model-value="key => save('avSchedule', key)"
      />
    </SettingRow>
    <SettingRow
      v-if="value.avSchedule === 'weekly'"
      :label="t('security.settings.schedule.weekday')"
      label-id="setting-avScheduleWeekday"
    >
      <OnMenu
        :label="t('security.settings.schedule.weekday')"
        :items="weekdays"
        align="end"
        @select="key => save('avScheduleWeekday', Number(key))"
      >
        <template #trigger="{ attrs }">
          <OnButton v-bind="attrs" size="sm">{{ weekdayName(value.avScheduleWeekday) }}</OnButton>
        </template>
      </OnMenu>
    </SettingRow>
    <SettingRow
      v-if="value.avSchedule !== 'off'"
      :label="t('security.settings.schedule.time')"
      label-id="setting-avScheduleTime"
    >
      <input
        type="time"
        :value="value.avScheduleTime"
        aria-labelledby="setting-avScheduleTime"
        class="h-30px rounded-small border border-solid border-line-default bg-surface-control px-8px font-sans text-13px text-ink-primary outline-none focus-visible:shadow-focus-ring"
        @change="onScheduleTime"
      />
    </SettingRow>
  </OnCard>

  <h2 class="m-0 mb-10px mt-24px text-14px font-600 text-ink-primary">{{ t('security.settings.exclusions') }}</h2>
  <OnCard padding="none">
    <SettingRow
      v-for="path in value.avExclusions"
      :key="path"
      :label="displayPath(path)"
      :label-id="`exclusion-${path}`"
    >
      <OnButton
        variant="ghost"
        size="xs"
        @click="
          save(
            'avExclusions',
            value.avExclusions.filter(entry => entry !== path)
          )
        "
      >
        {{ t('security.settings.removeExclusion') }}
      </OnButton>
    </SettingRow>
    <SettingRow
      :label="t('security.settings.addExclusion')"
      :hint="t('security.settings.exclusionsHint')"
      label-id="setting-addExclusion"
    >
      <OnButton size="sm" icon="folder" @click="addExclusion">{{ t('security.settings.choose') }}</OnButton>
    </SettingRow>
  </OnCard>

  <h2 class="m-0 mb-4px mt-24px text-14px font-600 text-ink-primary">{{ t('security.settings.optional.title') }}</h2>
  <p class="m-0 mb-10px text-12.5px text-ink-tertiary">{{ t('security.settings.optional.hint') }}</p>
  <OnCard padding="none">
    <SettingRow
      :label="t('security.settings.paste.title')"
      :hint="t('security.settings.paste.hint')"
      label-id="setting-pasteProtection"
    >
      <OnToggle
        :model-value="value.pasteProtection"
        aria-labelledby="setting-pasteProtection"
        @update:model-value="on => save('pasteProtection', on)"
      />
    </SettingRow>
    <SettingRow
      v-if="value.pasteProtection && security.pasteStatus && !security.pasteStatus.accessibilityGranted"
      :label="t('security.settings.paste.needsAccess')"
      :hint="t('security.settings.paste.needsAccessHint')"
      label-id="setting-pasteAccess"
    >
      <OnButton size="sm" @click="grantAccessibility">{{ t('security.settings.paste.grant') }}</OnButton>
    </SettingRow>
    <SettingRow
      :label="t('security.settings.virusTotal.title')"
      :hint="t('security.settings.virusTotal.hint')"
      label-id="setting-virusTotalEnabled"
    >
      <OnToggle
        :model-value="value.virusTotalEnabled"
        aria-labelledby="setting-virusTotalEnabled"
        @update:model-value="on => save('virusTotalEnabled', on)"
      />
    </SettingRow>
    <SettingRow
      v-if="value.virusTotalEnabled"
      :label="t('security.settings.virusTotal.key')"
      :hint="
        security.virusTotal?.hasKey
          ? t('security.settings.virusTotal.keySaved')
          : t('security.settings.virusTotal.keyHint')
      "
      label-id="setting-virusTotalKey"
    >
      <OnButton v-if="security.virusTotal?.hasKey" variant="ghost" size="xs" @click="saveVirusTotalKey(null)">
        {{ t('security.settings.virusTotal.removeKey') }}
      </OnButton>
      <form v-else class="flex items-center gap-6px" @submit.prevent="saveVirusTotalKey(virusTotalKey.trim())">
        <input
          v-model="virusTotalKey"
          type="password"
          autocomplete="off"
          spellcheck="false"
          :placeholder="t('security.settings.virusTotal.placeholder')"
          aria-labelledby="setting-virusTotalKey"
          class="h-30px w-200px rounded-small border border-solid border-line-default bg-surface-control px-8px font-mono text-12px text-ink-primary outline-none focus-visible:shadow-focus-ring"
        />
        <OnButton size="sm" type="submit" :disabled="!virusTotalKey.trim()">{{
          t('security.settings.virusTotal.saveKey')
        }}</OnButton>
      </form>
    </SettingRow>
  </OnCard>
  <p class="m-0 mt-8px text-12px leading-[1.55] text-ink-tertiary">{{ t('security.settings.virusTotal.terms') }}</p>
</template>
