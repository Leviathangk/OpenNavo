<script setup lang="ts">
import { isTauri } from '@tauri-apps/api/core';
import { getCurrentWindow } from '@tauri-apps/api/window';
import { onBeforeUnmount, onMounted, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useMonitorStore } from '@/stores/monitor';

// Main-window notices for monitoring: the one-time menu bar introduction (decision 2), the resident monitor notice
// after its login item is registered (#130), and fans Rust changed on its own (§14): handed back to the system, or
// raised to full speed while it stays too hot, so neither is a surprise.
const { t } = useI18n();
const router = useRouter();
const toasts = useToasts();
const settings = useSettingsStore();
const store = useMonitorStore();

let menuBarNoticeShown = false;
let residentNoticeShown = false;
let mainVisible = false;

/** The hidden resident loads the main window too; do not consume its notices before it is shown. */
async function windowVisible() {
  if (!isTauri()) return document.visibilityState === 'visible';
  try {
    return await getCurrentWindow().isVisible();
  } catch {
    return document.visibilityState === 'visible';
  }
}

async function refreshVisibility() {
  if (!(await windowVisible())) return;
  mainVisible = true;
  showResidentNotice();
  showMenuBarIntro(store.menuBarCount);
}

function showMenuBarIntro(count: number | undefined) {
  if (!mainVisible || menuBarNoticeShown || !settings.value?.onboardingCompleted) return;
  if (settings.value.monitorMenuBarIntro !== false || !count) return;
  // The resident notice supersedes the older menu bar introduction; both say the items stay after quitting.
  if (store.residentStatus?.noticePending) return;
  menuBarNoticeShown = true;
  void store
    .menuBarStatus()
    .then(installed => {
      if (!installed.some(item => item.installed)) {
        menuBarNoticeShown = false;
        return;
      }
      void store.dismissIntro().catch(() => undefined);
      toasts.pushAction(
        {
          tone: 'info',
          title: t('monitor.intro.title'),
          description: t('monitor.intro.body'),
          actionLabel: t('monitor.intro.view')
        },
        () => void router.push('/monitor')
      );
    })
    .catch(() => {
      menuBarNoticeShown = false;
    });
}

function showResidentNotice() {
  const status = store.residentStatus;
  if (!mainVisible || residentNoticeShown || !settings.value?.onboardingCompleted || !status?.noticePending) return;
  residentNoticeShown = true;
  const loginItemsPane = status.loginItemsPaneNew
    ? t('monitor.settings.resident.loginItemsPane')
    : t('monitor.settings.resident.loginItemsPaneOld');
  void store.dismissResidentIntro().catch(() => undefined);
  // Do not follow the resident notice with the older menu bar introduction.
  if (settings.value.monitorMenuBarIntro === false) void store.dismissIntro().catch(() => undefined);
  toasts.pushAction(
    {
      id: 'monitor-resident-intro',
      tone: 'info',
      duration: 0,
      title: t('monitor.settings.resident.noticeTitle', { monitorName: status.monitorName }),
      description: t('monitor.settings.resident.noticeBody', {
        loginItemsPane,
        systemMonitor: t('settings.sections.monitor')
      }),
      actionLabel: t('monitor.settings.resident.noticeAction')
    },
    () => void router.push('/settings/monitor')
  );
}

onMounted(async () => {
  await store.loadResidentStatus().catch(() => undefined);
  await refreshVisibility();
  window.addEventListener('focus', refreshVisibility);
});
onBeforeUnmount(() => window.removeEventListener('focus', refreshVisibility));

watch(
  () => [settings.value?.onboardingCompleted, settings.value?.monitorMenuBarIntro, store.menuBarCount] as const,
  ([, , count]) => showMenuBarIntro(count)
);

watch(
  () => store.residentStatus?.noticePending,
  pending => {
    if (pending) showResidentNotice();
  }
);

watch(
  () => settings.value?.onboardingCompleted,
  done => {
    if (done) showResidentNotice();
  }
);

watch(
  () => store.fanChange,
  change => {
    if (!mainVisible || !change || change.reason === 'user' || change.reason === 'wake_reapplied') return;
    toasts.push({
      tone: change.reason === 'overheat' || change.reason === 'write_failed' ? 'warning' : 'info',
      title: change.reason === 'overheat' ? t('monitor.fans.toasts.full') : t('monitor.fans.toasts.reverted'),
      description: t(`monitor.fans.reasons.${change.reason}`)
    });
  }
);
</script>

<template>
  <span hidden></span>
</template>
