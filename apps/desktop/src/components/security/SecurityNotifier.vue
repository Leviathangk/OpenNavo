<script setup lang="ts">
import { watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useToasts } from '@/composables/useToasts';
import { useTasksStore } from '@/stores/tasks';
import { useSecurityStore } from '@/stores/security';

// In-app notices for Security (plan §6.4): Rust also posts a system notification for new startup items; inside the app
// a toast links straight to the item. Scans keep running off the page, so a finished scan with threats says so too.
const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const toasts = useToasts();
const store = useSecurityStore();
const tasks = useTasksStore();
const { taskName } = usePackageIdentity();
const { appLocale } = useAppLocale();

// S2: an app now signed by another developer is the change worth interrupting for; other check-up findings wait
// on the overview.
watch(
  () => store.signatureChanges,
  apps => {
    const changed = apps.filter(app => app.issues.includes('team_changed'));
    if (!changed.length) return;
    const shown = changed.slice(0, 3).map(app => app.name);
    const names = new Intl.ListFormat(appLocale.value, { type: 'conjunction' }).format(shown);
    toasts.pushAction(
      {
        tone: 'danger',
        title: t('security.toasts.signatureChanged', { count: changed.length }, { plural: changed.length }),
        description: t('security.toasts.signatureChangedBody', {
          names: changed.length > shown.length ? `${names}…` : names
        }),
        actionLabel: t('security.toasts.view')
      },
      () => void router.push('/security')
    );
  }
);

// A threat in a Homebrew download stopped its install before anything was written to /Applications.
watch(
  () => store.installScan,
  result => {
    if (result?.state !== 'threat') return;
    const task = [...tasks.active, ...tasks.finished].find(item => item.id === result.taskId);
    toasts.push({
      tone: 'danger',
      title: t('security.toasts.installThreat'),
      description: [task ? taskName(task) : '', ...result.threats].filter(Boolean).join(' · ')
    });
  }
);

watch(
  () => store.added,
  items => {
    const first = items[0];
    if (!first) return;
    toasts.pushAction(
      {
        tone: first.trust === 'untrusted' ? 'warning' : 'info',
        title: t('security.toasts.startupAdded', { count: items.length }, { plural: items.length }),
        description: t('security.toasts.startupAddedBody', {
          name: first.name,
          signer: first.signature.signer ?? t(`security.startup.trust.${first.trust}`)
        }),
        actionLabel: t('security.toasts.review')
      },
      () => void router.push('/security/startup')
    );
  }
);

watch(
  () => store.scan?.state,
  (state, previous) => {
    const scan = store.scan;
    if (!scan || state !== 'done' || previous !== 'running' || route.name === 'security') return;
    const threats = scan.findings.length;
    toasts.pushAction(
      {
        tone: threats ? 'danger' : 'success',
        title: threats
          ? t('security.toasts.threats', { count: threats }, { plural: threats })
          : t('security.toasts.clean'),
        actionLabel: t('security.toasts.view')
      },
      () => void router.push('/security/scan')
    );
  }
);
</script>

<template>
  <span hidden></span>
</template>
