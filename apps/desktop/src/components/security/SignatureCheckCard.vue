<script setup lang="ts">
import { onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnIcon } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useLocalAppsStore } from '@/stores/localApps';
import { useSecurityStore } from '@/stores/security';
import SignatureIssuesDialog from './SignatureIssuesDialog.vue';
import { useSignatureProblems } from './signatureProblems';

// App signature check-up (store/security plan §6, S2): every installed app's code signature and notarization, and
// whether its developer (Team ID) changed since the last check. The check only reads; the card shows the first few
// problems and opens the whole list, where an app can be looked at in Finder or uninstalled.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const security = useSecurityStore();
const toasts = useToasts();
const { errorText } = usePackageState();

onMounted(() => {
  if (!security.signatures) void security.loadSignatures().catch(() => undefined);
});

const { report, rows: problems } = useSignatureProblems();
const PREVIEW = 5;
const listOpen = ref(false);

// Apps uninstalled since the check drop out of the report Rust returns; read it again when the apps on this Mac change.
const localApps = useLocalAppsStore();
watch(
  () =>
    localApps.items
      .map(app => app.path)
      .sort()
      .join('\n'),
  () => {
    if (security.signatures) void security.loadSignatures().catch(() => undefined);
  }
);

async function check() {
  try {
    await security.checkSignatures();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <OnCard padding="md" as="section" class="flex flex-col gap-6px" aria-labelledby="security-signatures">
    <h2 id="security-signatures" class="m-0 flex items-center gap-7px text-14px font-600 text-ink-primary">
      <OnIcon name="shield-check" :size="15" />{{ t('security.signatures.title') }}
    </h2>
    <p class="m-0 text-12.5px text-ink-secondary">
      {{
        !report
          ? t('security.signatures.never')
          : problems.length
            ? t(
                'security.signatures.summaryIssues',
                { count: report.apps.length, issues: problems.length },
                { plural: problems.length }
              )
            : t('security.signatures.summaryClean', { count: report.apps.length }, { plural: report.apps.length })
      }}
      <span v-if="report" class="text-ink-tertiary">
        · {{ formatRelativeTime(report.checkedAt, { locale: appLocale }) }}</span
      >
    </p>
    <ul v-if="problems.length" class="m-0 flex list-none flex-col gap-3px p-0">
      <li v-for="row in problems.slice(0, PREVIEW)" :key="row.entry.path" class="truncate text-12.5px">
        <span class="font-600 text-ink-primary">{{ row.name }}</span>
        <span :class="row.serious ? 'text-status-danger' : 'text-status-warning'">
          · {{ row.issues.map(issue => t(`security.signatures.issue.${issue}`)).join(' · ') }}
        </span>
      </li>
    </ul>
    <div class="mt-4px flex flex-wrap gap-8px">
      <OnButton v-if="problems.length" size="sm" variant="primary" @click="listOpen = true">{{
        t('security.signatures.viewAll', { count: problems.length })
      }}</OnButton>
      <OnButton size="sm" variant="secondary" :loading="security.checkingSignatures" @click="check">
        {{ report ? t('security.signatures.recheck') : t('security.signatures.check') }}
      </OnButton>
    </div>
    <SignatureIssuesDialog v-model:open="listOpen" />
  </OnCard>
</template>
