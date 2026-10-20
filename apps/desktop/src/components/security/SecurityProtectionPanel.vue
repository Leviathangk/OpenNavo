<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnChip, OnToggle } from '@opennavo/ui';
import type { ProtectionEvent } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';
import { useTasksStore } from '@/stores/tasks';
import { usePasteAccess } from './usePasteAccess';

// Protection (plan §6.4): what runs today (S1 and S2, no Apple entitlement) is switchable; the rest says it waits for
// Endpoint Security approval (S3) instead of pretending to be available. The app firewall (S4) has its own page.
const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const toasts = useToasts();
const { errorText } = usePackageState();
const { taskName } = usePackageIdentity();
const store = useSecurityStore();
const tasks = useTasksStore();
const pasteNeedsAccess = usePasteAccess();

onMounted(() => {
  void store.loadEvents().catch(() => undefined);
});

type Switch = 'startupAlerts' | 'watchDownloads' | 'avScanBeforeInstall' | 'pasteProtection';
// Stage codes (S1–S4) are the plan's phase names, shown as-is in every language.
type Live = {
  key: Switch;
  title: string;
  body: string;
  disabled: boolean;
  hint?: string;
  /** Paste protection is on but macOS has not granted Accessibility yet. */
  grant?: boolean;
  stage: string;
};
const live = computed<Live[]>(() => {
  const db = Boolean(store.overview?.engine.db);
  return [
    {
      key: 'startupAlerts',
      title: t('security.protection.startupAlerts.title'),
      body: t('security.protection.startupAlerts.body'),
      disabled: false,
      stage: 'S1'
    },
    {
      key: 'watchDownloads',
      title: t('security.protection.watchDownloads.title'),
      body: t('security.protection.watchDownloads.body'),
      // Scanning downloads needs the virus database.
      disabled: !db,
      hint: db ? undefined : t('security.protection.needsDb'),
      stage: 'S1'
    },
    {
      key: 'avScanBeforeInstall',
      title: t('security.settings.beforeInstall'),
      // The description already says it needs the database.
      body: t('security.settings.beforeInstallHint'),
      disabled: !db,
      stage: 'S2'
    },
    {
      key: 'pasteProtection',
      title: t('security.settings.paste.title'),
      body: t('security.settings.paste.hint'),
      disabled: false,
      hint: pasteNeedsAccess.value ? t('security.settings.paste.needsAccessHint') : undefined,
      grant: pasteNeedsAccess.value,
      stage: 'S2'
    }
  ];
});
const later = [
  { key: 'realtime', stage: 'S3' },
  { key: 'exec', stage: 'S3' },
  { key: 'ransomware', stage: 'S3' }
];

async function toggle(key: Switch, value: boolean) {
  try {
    await store.setSecuritySetting(key, value);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Accessibility is requested only on this click.
async function grantAccessibility() {
  try {
    await store.requestPasteAccess();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

const TASK_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
// Rust names a skipped scheduled scan "scheduled_scan:<reason>", and a stopped install by its task id: the task's name
// is known while it is still in this session's task list.
function eventText(event: ProtectionEvent): string {
  if (event.kind === 'scheduled_scan_skipped') {
    const reason = event.name.replace(/^scheduled_scan:/, '');
    const key = `security.settings.schedule.reason.${reason}`;
    return t('security.protection.events.scheduled_scan_skipped', { name: te(key) ? t(key) : reason });
  }
  if (event.kind === 'install_threat') {
    const task = [...tasks.active, ...tasks.finished].find(item => item.id === event.name);
    const name = task ? taskName(task) : TASK_ID.test(event.name) ? '' : event.name;
    return name
      ? t('security.protection.events.install_threat', { name })
      : t('security.protection.installThreatUnnamed');
  }
  return t(`security.protection.events.${event.kind}`, { name: event.name });
}
</script>

<template>
  <div class="grid grid-cols-1 gap-16px lg:grid-cols-[minmax(0,1fr)_320px]">
    <OnCard padding="none" as="section" aria-labelledby="protection-title">
      <h2 id="protection-title" class="m-0 px-16px pt-14px text-14px font-600 text-ink-primary">
        {{ t('security.protection.title') }}
      </h2>
      <ul class="m-0 mt-8px list-none p-0">
        <li
          v-for="item in live"
          :key="item.key"
          class="flex items-start gap-12px border-t border-t-solid border-line-subtle px-16px py-12px"
        >
          <OnToggle
            class="mt-2px"
            :model-value="Boolean(store.securitySettings[item.key]) && !item.disabled"
            :disabled="item.disabled"
            :aria-label="item.title"
            @update:model-value="(on: boolean) => toggle(item.key, on)"
          />
          <div class="min-w-0 flex-1">
            <p class="m-0 text-13px font-600 text-ink-primary">{{ item.title }}</p>
            <p class="m-0 mt-2px text-12.5px leading-[1.55] text-ink-secondary">{{ item.body }}</p>
            <p v-if="item.hint" class="m-0 mt-2px text-12px text-ink-tertiary">{{ item.hint }}</p>
            <OnButton v-if="item.grant" size="xs" class="mt-6px" @click="grantAccessibility">
              {{ t('security.settings.paste.grant') }}
            </OnButton>
          </div>
          <OnChip tone="success" size="sm">{{ item.stage }}</OnChip>
        </li>
        <li
          v-for="item in later"
          :key="item.key"
          class="flex items-start gap-12px border-t border-t-solid border-line-subtle px-16px py-12px"
        >
          <OnToggle
            class="mt-2px"
            :model-value="false"
            disabled
            :aria-label="t(`security.protection.later.${item.key}.title`)"
          />
          <div class="min-w-0 flex-1">
            <p class="m-0 text-13px font-600 text-ink-secondary">
              {{ t(`security.protection.later.${item.key}.title`) }}
            </p>
            <p class="m-0 mt-2px text-12.5px leading-[1.55] text-ink-tertiary">
              {{ t(`security.protection.later.${item.key}.body`) }}
            </p>
          </div>
          <OnChip tone="warning" size="sm">{{ t('security.protection.waiting', { stage: item.stage }) }}</OnChip>
        </li>
      </ul>
    </OnCard>

    <OnCard padding="none" as="section" aria-labelledby="protection-events">
      <h2 id="protection-events" class="m-0 px-16px pt-14px text-14px font-600 text-ink-primary">
        {{ t('security.protection.eventsTitle') }}
      </h2>
      <ul v-if="store.events.length" class="m-0 mt-8px list-none p-0">
        <li
          v-for="event in store.events"
          :key="event.id"
          class="border-t border-t-solid border-line-subtle px-16px py-10px"
        >
          <p class="m-0 text-12.5px text-ink-primary">{{ eventText(event) }}</p>
          <p class="m-0 text-11.5px text-ink-tertiary">{{ formatRelativeTime(event.at, { locale: appLocale }) }}</p>
        </li>
      </ul>
      <p v-else class="m-0 px-16px py-14px text-12.5px text-ink-tertiary">{{ t('security.protection.noEvents') }}</p>
    </OnCard>
  </div>
</template>
