<script setup lang="ts">
import { computed, onMounted } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCard, OnIcon } from '@opennavo/ui';
import type { AppSignatureIssue } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';

// App signature check-up (store/security plan §6, S2): every installed app's code signature and notarization, and
// whether its developer (Team ID) changed since the last check. Read-only: it lists problems and changes nothing.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const security = useSecurityStore();
const toasts = useToasts();
const { errorText } = usePackageState();

onMounted(() => {
  if (!security.signatures) void security.loadSignatures().catch(() => undefined);
});

const report = computed(() => security.signatures);
// Most serious first: another developer or a broken signature, then changed contents, then what is merely missing.
const RANK: Record<AppSignatureIssue, number> = {
  team_changed: 0,
  invalid: 1,
  modified: 2,
  unreadable: 3,
  unsigned: 4,
  adhoc: 5,
  not_notarized: 6
};
const rank = (issues: readonly AppSignatureIssue[]) => Math.min(...issues.map(issue => RANK[issue]));
const problems = computed(() =>
  (report.value?.apps ?? []).filter(app => app.issues.length).sort((a, b) => rank(a.issues) - rank(b.issues))
);
// Another developer signing an app, or a broken signature, is worth more attention than a missing notarization.
const SERIOUS: readonly AppSignatureIssue[] = ['team_changed', 'invalid'];
const serious = (issues: readonly AppSignatureIssue[]) => issues.some(issue => SERIOUS.includes(issue));

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
      <li v-for="app in problems.slice(0, 5)" :key="app.path" class="truncate text-12.5px">
        <span class="font-600 text-ink-primary">{{ app.name }}</span>
        <span :class="serious(app.issues) ? 'text-status-danger' : 'text-status-warning'">
          · {{ app.issues.map(issue => t(`security.signatures.issue.${issue}`)).join(' · ') }}
        </span>
      </li>
    </ul>
    <OnButton
      size="sm"
      variant="secondary"
      class="mt-4px self-start"
      :loading="security.checkingSignatures"
      @click="check"
    >
      {{ report ? t('security.signatures.recheck') : t('security.signatures.check') }}
    </OnButton>
  </OnCard>
</template>
