<script setup lang="ts">
import { watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore } from '@/stores';
import { useMonitorStore } from '@/stores/monitor';

// Main-window notices for monitoring: the one-time menu bar introduction (decision 2), and fans Rust changed on its own
// (§14): handed back to the system, or raised to full speed while it stays too hot, so neither is a surprise.
const { t } = useI18n();
const router = useRouter();
const toasts = useToasts();
const settings = useSettingsStore();
const store = useMonitorStore();

watch(
  () => [settings.value?.onboardingCompleted, settings.value?.monitorMenuBarIntro, store.menuBarCount] as const,
  async ([onboarded, seen, count]) => {
    if (!onboarded || seen !== false || !count) return;
    // Only introduce items Rust actually put on the menu bar.
    const installed = await store.menuBarStatus().catch(() => []);
    if (!installed.some(item => item.installed)) return;
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
  },
  { immediate: true }
);

watch(
  () => store.fanChange,
  change => {
    if (!change || change.reason === 'user' || change.reason === 'wake_reapplied') return;
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
