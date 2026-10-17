<script setup lang="ts">
import { watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { useToasts } from '@/composables/useToasts';
import { usePackageState } from '@/composables/usePackageState';
import { useDownloadsStore } from '@/stores/downloads';
import { useSecurityStore } from '@/stores/security';
import { describeVerdict, fileName } from './downloadVerdict';

// In-app notice for a checked download (store/security plan §4): the app's page shows the verdict as a banner, so
// the toast only appears elsewhere and links back to that page.
const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const toasts = useToasts();
const downloads = useDownloadsStore();
const security = useSecurityStore();
const { errorText } = usePackageState();

watch(
  () => downloads.last,
  entry => {
    if (!entry?.verdict) return;
    if (route.params.kind === 'link' && route.params.token === entry.token) return;
    const { tone, parts } = describeVerdict(entry.verdict, t);
    toasts.pushAction(
      {
        tone,
        title: t('package.link.verdict.title', { file: fileName(entry.path) }),
        description: parts.join(' · '),
        actionLabel: t('package.link.verdict.view')
      },
      () => void router.push(`/package/link/${entry.token}`)
    );
  }
);

// Any other finished download with a threat (Downloads watch, S1): a custom scan of just that file lists it with the
// usual quarantine and delete actions.
watch(
  () => downloads.threat,
  found => {
    if (!found) return;
    toasts.pushAction(
      {
        tone: 'danger',
        title: t('security.downloadThreat.title', { file: fileName(found.path) }),
        description: t('security.downloadThreat.body', { signature: found.signature }),
        actionLabel: t('security.downloadThreat.action')
      },
      () =>
        void router
          .push('/security/scan')
          .then(() => security.startScan('custom', [found.path]))
          .catch(error =>
            toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) })
          )
    );
  }
);
</script>

<template>
  <span hidden></span>
</template>
