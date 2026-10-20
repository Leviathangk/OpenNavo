<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatRelativeTime } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnCheckbox, OnChip, OnEmpty, OnModal } from '@opennavo/ui';
import { commands, unwrap } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSecurityStore } from '@/stores/security';
import { useUninstallStore } from '@/stores/uninstall';
import { displayPath } from '@/utils/paths';
import { isSerious, useSignatureProblems } from './signatureProblems';
import type { SignatureRow } from './signatureProblems';

// Every app the signature check-up has something to say about, with what it found and the way out: look at the app in
// Finder, or uninstall it (one, or several chosen here). Uninstalling goes through the same review dialog as Installed.
defineProps<{ open: boolean }>();
const emit = defineEmits<{ 'update:open': [open: boolean] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const security = useSecurityStore();
const uninstaller = useUninstallStore();
const toasts = useToasts();
const { errorText } = usePackageState();
const { report, rows } = useSignatureProblems();

const selected = ref(new Set<string>());
const canUninstall = (row: SignatureRow) => Boolean(row.app?.removable);
const chosen = computed(() => rows.value.filter(row => selected.value.has(row.entry.path) && canUninstall(row)));
// What is no longer listed (uninstalled, or no longer a problem) is no longer chosen.
watch(rows, list => {
  const paths = new Set(list.map(row => row.entry.path));
  if ([...selected.value].some(path => !paths.has(path)))
    selected.value = new Set([...selected.value].filter(path => paths.has(path)));
});
watch(
  () => rows.value.length === 0,
  empty => {
    if (empty) selected.value = new Set();
  }
);

function toggle(row: SignatureRow, on: boolean) {
  const next = new Set(selected.value);
  if (on) next.add(row.entry.path);
  else next.delete(row.entry.path);
  selected.value = next;
}

// Rows are grouped by their most serious problem, so what a problem means is said once for the group, not on every row.
const groups = computed(() => {
  const list: { issue: SignatureRow['issues'][number]; rows: SignatureRow[] }[] = [];
  for (const row of rows.value) {
    const issue = row.issues[0]!;
    const group = list.find(item => item.issue === issue);
    if (group) group.rows.push(row);
    else list.push({ issue, rows: [row] });
  }
  return list;
});
const whyNot = (row: SignatureRow) =>
  !row.app
    ? t('security.signatures.dialog.missing')
    : row.app.removable
      ? undefined
      : t(`appInfo.protected.${row.app.protectedReason ?? 'system'}`);

async function reveal(row: SignatureRow) {
  if (!row.app) return;
  try {
    await unwrap(commands.localAppReveal(row.app.id));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// The review and the confirmation are the uninstall dialog's; this one steps aside for it, as the info sheet does.
async function uninstall(list: readonly SignatureRow[]) {
  const ids = list.flatMap(row => (row.app?.removable ? [row.app.id] : []));
  if (!ids.length) return;
  selected.value = new Set();
  emit('update:open', false);
  try {
    await uninstaller.start(ids);
  } catch {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
  }
}

async function recheck() {
  try {
    await security.checkSignatures();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <OnModal
    :open="open"
    size="lg"
    :title="t('security.signatures.dialog.title')"
    @update:open="value => emit('update:open', value)"
  >
    <template v-if="report" #subtitle>
      {{
        t(
          'security.signatures.summaryIssues',
          { count: report.apps.length, issues: rows.length },
          { plural: rows.length }
        )
      }}
      · {{ formatRelativeTime(report.checkedAt, { locale: appLocale }) }}
    </template>
    <div class="flex flex-col gap-12px pb-4px">
      <p class="m-0 text-12.5px leading-[1.55] text-ink-tertiary">{{ t('security.signatures.dialog.hint') }}</p>
      <OnEmpty
        v-if="!rows.length"
        :title="t('security.signatures.dialog.empty')"
        icon="lucide:shield-check"
        data-testid="signature-issues-empty"
      />
      <template v-else>
        <section v-for="group in groups" :key="group.issue" class="flex flex-col" :data-issue="group.issue">
          <header class="flex flex-col gap-3px pb-6px pt-4px">
            <h3 class="m-0 flex items-center gap-8px text-12.5px font-600 text-ink-primary">
              <OnChip :tone="isSerious([group.issue]) ? 'danger' : 'warning'" size="sm">{{
                t(`security.signatures.issue.${group.issue}`)
              }}</OnChip>
              <span class="text-ink-tertiary">{{ group.rows.length }}</span>
            </h3>
            <p class="m-0 text-12px leading-[1.5] text-ink-tertiary">
              {{ t(`security.signatures.hint.${group.issue}`) }}
            </p>
          </header>
          <ul class="m-0 list-none p-0" :aria-label="t(`security.signatures.issue.${group.issue}`)">
            <li
              v-for="row in group.rows"
              :key="row.entry.path"
              class="flex items-center gap-12px border-t border-t-solid border-line-subtle py-8px"
            >
              <OnCheckbox
                class="shrink-0"
                :model-value="selected.has(row.entry.path)"
                :disabled="!canUninstall(row)"
                :aria-label="row.name"
                :title="whyNot(row)"
                @update:model-value="value => toggle(row, value)"
              />
              <OnAppIcon v-bind="row.icon" :size="28" />
              <div class="min-w-0 flex-1">
                <p class="m-0 flex flex-wrap items-center gap-x-6px gap-y-3px text-13px font-600 text-ink-primary">
                  <span class="min-w-0 truncate">{{ row.name }}</span>
                  <!-- A second problem of the same app; the first one is the group's. -->
                  <OnChip
                    v-for="issue in row.issues.slice(1)"
                    :key="issue"
                    :tone="isSerious([issue]) ? 'danger' : 'warning'"
                    size="sm"
                    >{{ t(`security.signatures.issue.${issue}`) }}</OnChip
                  >
                </p>
                <p class="m-0 truncate text-11.5px text-ink-tertiary" :title="row.entry.path">
                  {{ displayPath(row.entry.path) }}
                </p>
              </div>
              <div class="flex shrink-0 items-center gap-6px">
                <OnButton size="xs" icon="folder" :disabled="!row.app" @click="reveal(row)">{{
                  t('appInfo.reveal')
                }}</OnButton>
                <OnButton
                  size="xs"
                  variant="danger"
                  icon="trash"
                  :disabled="!canUninstall(row)"
                  :title="whyNot(row)"
                  @click="uninstall([row])"
                  >{{ t('appInfo.uninstall') }}</OnButton
                >
              </div>
            </li>
          </ul>
        </section>
      </template>
    </div>
    <template #footer>
      <OnButton variant="ghost" :loading="security.checkingSignatures" @click="recheck">{{
        t('security.signatures.recheck')
      }}</OnButton>
      <OnButton variant="danger" icon="trash" :disabled="!chosen.length" @click="uninstall(chosen)">{{
        t('security.signatures.dialog.uninstallSelected', { count: chosen.length })
      }}</OnButton>
    </template>
  </OnModal>
</template>
