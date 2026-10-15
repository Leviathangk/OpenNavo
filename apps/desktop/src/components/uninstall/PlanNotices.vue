<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatList } from '@opennavo/shared';
import { OnButton, OnIcon } from '@opennavo/ui';
import RelaunchNotice from '@/components/permissions/RelaunchNotice.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { usePermissionsStore } from '@/stores';
import { useLocalAppsStore } from '@/stores/localApps';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import type { GuidePane } from '@/stores/permissions';
import { useUninstallStore } from '@/stores/uninstall';
import { displayPath } from '@/utils/paths';

// Review notices (proposal §03): permissions first (they change what can be done), then what will happen.
const PERSONAL = new Set(['chat_history', 'documents', 'media', 'browser_profile', 'vm_disk']);

interface Notice {
  key: string;
  tone: 'warning' | 'info' | 'neutral';
  icon: string;
  text: string;
  pane?: GuidePane;
}

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useUninstallStore();
const permissions = usePermissionsStore();
const localApps = useLocalAppsStore();
const { localApp } = usePackageIdentity();
// Same name as the dialog title: the catalog's localized name when the app is in the catalog.
const nameOf = (appId: string, fallback: string) => {
  const app = localApps.find(appId);
  return app ? localApp(app).name : fallback;
};
const toasts = useToasts();
const { errorText } = usePackageState();

const names = (values: string[]) => formatList(values, { locale: appLocale.value });

const notices = computed<Notice[]>(() => {
  const list: Notice[] = [];
  const apps = store.included;
  if (store.rescanned)
    list.push({
      key: 'rescanned',
      tone: 'info',
      icon: 'refresh-cw',
      text: t('uninstall.notices.rescanned')
    });
  const blocked = apps.filter(app => app.blockedReason === 'app_management');
  if (blocked.length)
    list.push({
      key: 'app-management',
      tone: 'warning',
      icon: 'shield-check',
      text: t('uninstall.notices.appManagement'),
      pane: 'app_management'
    });
  const incomplete = store.plan?.incomplete ?? [];
  if (incomplete.length)
    list.push({
      key: 'incomplete',
      tone: 'warning',
      icon: 'hard-drive',
      text: t(
        'uninstall.notices.incomplete',
        {
          count: incomplete.length,
          paths: names(incomplete.map(location => displayPath(location.path)))
        },
        { plural: incomplete.length }
      ),
      pane: 'full_disk_access'
    });
  const running = apps.filter(app => app.running);
  if (running.length)
    list.push({
      key: 'running',
      tone: 'warning',
      icon: 'power',
      text: t(
        'uninstall.notices.running',
        { names: names(running.map(app => nameOf(app.appId, app.name))) },
        { plural: running.length }
      )
    });
  // A cask with several apps is removed as a whole by brew.
  for (const app of apps) {
    const cask = localApps.find(app.appId)?.cask;
    const siblings = cask ? localApps.items.filter(other => other.cask === cask && other.id !== app.appId) : [];
    if (siblings.length)
      list.push({
        key: `siblings-${app.appId}`,
        tone: 'info',
        icon: 'package',
        text: t('uninstall.notices.siblings', {
          cask,
          names: names(siblings.map(other => localApp(other).name))
        })
      });
  }
  // Homebrew's own uninstall steps run their own sudo; OpenNavo's batch cannot share that authorization.
  if (
    apps.some(
      app => app.removal === 'brew' && app.items.some(item => item.reason === 'brew_uninstall' && item.needsAdmin)
    )
  )
    list.push({
      key: 'brew-sudo',
      tone: 'neutral',
      icon: 'key-round',
      text: t('uninstall.notices.brewSudo')
    });
  for (const app of apps) {
    if (!app.personalData) continue;
    const data = app.items.filter(item => item.category === 'app_data');
    const size = formatBytes(
      data.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0),
      { locale: appLocale.value }
    );
    const tag = PERSONAL.has(app.personalData) ? app.personalData : 'other';
    list.push({
      key: `personal-${app.appId}`,
      tone: 'info',
      icon: 'info',
      text: t(`uninstall.notices.personal.${tag}`, { name: nameOf(app.appId, app.name), size })
    });
  }
  for (const app of apps)
    if (app.uninstaller)
      list.push({
        key: `uninstaller-${app.appId}`,
        tone: 'info',
        icon: 'info',
        text: t('uninstall.notices.uninstaller', { name: nameOf(app.appId, app.name) })
      });
  return list;
});

const TONE: Record<Notice['tone'], string> = {
  warning: 'bg-status-warning-subtle',
  info: 'bg-surface-card-alt',
  neutral: 'bg-surface-card-alt'
};
const ICON: Record<Notice['tone'], string> = {
  warning: 'text-status-warning',
  info: 'text-status-info',
  neutral: 'text-ink-tertiary'
};

async function enable(pane: GuidePane) {
  try {
    await permissions.guide(pane);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div v-if="notices.length" class="mb-12px flex flex-col gap-8px">
    <div
      v-for="notice in notices"
      :key="notice.key"
      class="flex items-start gap-10px rounded-default px-12px py-10px text-12.5px leading-[1.55] text-ink-secondary"
      :class="TONE[notice.tone]"
      role="status"
    >
      <OnIcon :name="notice.icon" :size="15" class="mt-2px shrink-0" :class="ICON[notice.tone]" />
      <span class="min-w-0 flex-1">{{ notice.text }}</span>
      <OnButton
        v-if="notice.pane && !permissions.pending(notice.pane)"
        variant="secondary"
        size="xs"
        class="shrink-0 self-center"
        :loading="permissions.guiding === notice.pane"
        @click="enable(notice.pane)"
      >
        {{ permissions.guiding === notice.pane ? t('permission.prompt.waiting') : t('permission.prompt.enable') }}
      </OnButton>
    </div>
    <RelaunchNotice v-if="permissions.relaunchRequired && notices.some(notice => notice.pane)" />
  </div>
</template>
