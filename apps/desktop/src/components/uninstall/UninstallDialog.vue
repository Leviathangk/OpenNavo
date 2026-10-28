<script setup lang="ts">
import { computed, watch } from 'vue';
import { Translation, useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import {
  OnAppIcon,
  OnButton,
  OnChip,
  OnCodeText,
  OnModal,
  OnProgressMeter,
  OnSegmented,
  OnSkeleton
} from '@opennavo/ui';
import type { DeleteMode, LocalApp, PlanItem } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import PermissionChecklist from '@/components/permissions/PermissionChecklist.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useUninstallUndo } from '@/composables/useUninstallUndo';
import { useUninstallStore } from '@/stores/uninstall';
import { displayPath } from '@/utils/paths';
import BatchPlanList from './BatchPlanList.vue';
import PlanGroups from './PlanGroups.vue';
import PlanNotices from './PlanNotices.vue';
import UninstallProgress from './UninstallProgress.vue';
import UninstallResult from './UninstallResult.vue';

// Uninstall dialog for every source (proposal §03–§04), mounted once in the shell and driven by the uninstall store.
// If permissions change during a session it shows the shared checklist. Closing while tasks run keeps them going; each background
// run then announces its result as a toast with Undo, even when another uninstall started meanwhile.
const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useUninstallStore();
const toasts = useToasts();
const { undo, purge } = useUninstallUndo();
const { errorText } = usePackageState();
const { localApp } = usePackageIdentity();

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });

// What the apps looked like when the session started: after uninstalling they leave local_apps_list.
const targets = computed(() => store.targets);
const single = computed(() => (targets.value.length === 1 ? targets.value[0] : undefined));
const nameOf = (app: LocalApp) => localApp(app).name;

const open = computed({
  get: () => store.open,
  set: value => {
    if (!value) void store.close();
  }
});

const title = computed(() => {
  const count = targets.value.length;
  const name = single.value ? nameOf(single.value) : '';
  switch (store.stage) {
    case 'failed':
      return t('uninstall.failedTitle');
    case 'submitting':
    case 'running':
      return single.value
        ? t('uninstall.runningTitle', { name })
        : t('uninstall.runningTitleMany', { count }, { plural: count });
    case 'done':
      return t('uninstall.doneTitle');
    default:
      return single.value ? t('uninstall.title', { name }) : t('uninstall.titleMany', { count }, { plural: count });
  }
});

const brewCommand = computed(() => store.plan?.apps.find(app => app.appId === single.value?.id)?.brewCommand ?? null);

const modes = computed(() => [
  { value: 'trash' as const, label: t('uninstall.mode.trash') },
  { value: 'delete' as const, label: t('uninstall.mode.delete') }
]);

const confirmLabel = computed(() => {
  const size = format(store.chosenBytes);
  if (targets.value.length > 1) {
    const count = store.included.length;
    return t('uninstall.confirmMany', { count, size }, { plural: count });
  }
  if (store.onlyRequired) return t('uninstall.confirm');
  return store.mode === 'trash' ? t('uninstall.confirmClean', { size }) : t('uninstall.confirmDelete', { size });
});

const scanPercent = computed(() =>
  store.progress && store.progress.total > 0 ? Math.round((store.progress.checked / store.progress.total) * 100) : null
);

const resultTitle = computed(() => {
  const done = store.result.tasks.filter(task => task.state === 'succeeded').length;
  if (single.value) return t('uninstall.result.titleOne', { name: nameOf(single.value) });
  return t('uninstall.result.titleMany', { count: done }, { plural: done });
});
const wasBrew = computed(() => targets.value.some(app => app.source === 'homebrew'));

async function confirm() {
  try {
    await store.confirm();
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

async function reveal(item: PlanItem) {
  if (!store.plan) return;
  try {
    await unwrap(commands.uninstallReveal(store.plan.planId, item.id));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

/** Uninstall again the apps of failed tasks (an app that did not quit can be retried once it is saved). */
function retry(apps: readonly string[]) {
  void store.start([...apps]);
}

// Background runs announce their result with a toast; Undo restores just those tasks.
watch(
  () => store.announcements,
  announcements => {
    for (const { key, apps, result } of announcements) {
      store.announced(key);
      const ids = result.tasks.filter(task => task.outcome?.restorable).map(task => task.id);
      const names = apps.map(nameOf);
      const count = result.moved + result.deleted;
      const toast = {
        tone: result.failed.length ? ('warning' as const) : ('success' as const),
        title:
          names.length === 1
            ? t('uninstall.toasts.done', { name: names[0] })
            : t('uninstall.toasts.doneMany', { count: names.length }, { plural: names.length }),
        description: t(
          result.moved ? 'uninstall.toasts.doneBody' : 'uninstall.toasts.deletedBody',
          { count, size: format(result.freedBytes) },
          { plural: count }
        ),
        duration: 8000
      };
      if (ids.length) toasts.pushAction({ ...toast, actionLabel: t('uninstall.toasts.undo') }, () => void undo(ids));
      else toasts.push(toast);
    }
  },
  { deep: true }
);
</script>

<template>
  <OnModal v-model:open="open" size="lg" :title="title" :dismissible="store.stage !== 'submitting'">
    <template v-if="targets.length" #leading>
      <OnAppIcon v-if="single" v-bind="localApp(single).icon" :size="44" />
      <span v-else class="flex shrink-0" aria-hidden="true">
        <OnAppIcon
          v-for="(app, index) in targets.slice(0, 3)"
          :key="app.id"
          v-bind="localApp(app).icon"
          :size="40"
          class="relative"
          :class="index ? '-ml-14px' : ''"
          :style="{ zIndex: 3 - index }"
        />
      </span>
    </template>
    <template #subtitle>
      <template v-if="single">
        <span class="inline-flex flex-wrap items-center gap-6px">
          <span>{{ t(`library.sources.${single.source}`) }}</span>
          <template v-if="single.bundleId">
            <span aria-hidden="true">·</span>
            <span class="font-mono text-11.5px">{{ single.bundleId }}</span>
          </template>
          <template v-if="single.version">
            <span aria-hidden="true">·</span>
            <span>{{ single.version }}</span>
          </template>
        </span>
        <span v-if="brewCommand && store.stage === 'review'" class="mt-4px block">
          <Translation keypath="uninstall.command" tag="span">
            <template #command>
              <code class="rounded-tiny bg-surface-inset px-5px py-1px font-mono text-11.5px text-component-mono-text"
                ><OnCodeText :text="brewCommand"
              /></code>
            </template>
          </Translation>
        </span>
      </template>
      <span v-else class="line-clamp-1">{{ targets.map(nameOf).join(' · ') }}</span>
    </template>

    <!-- Full Disk Access first -->
    <div v-if="store.stage === 'permission'" class="pb-8px">
      <PermissionChecklist />
    </div>

    <!-- Scanning -->
    <div v-else-if="store.stage === 'scanning'" class="pb-8px" aria-live="polite">
      <div class="mb-8px flex items-center justify-between text-12.5px text-ink-secondary">
        <span>{{ t('uninstall.scanning') }}</span>
        <span v-if="store.progress" class="tabular-nums">{{
          t('uninstall.checked', { checked: store.progress.checked, total: store.progress.total })
        }}</span>
      </div>
      <OnProgressMeter :value="scanPercent" :label="t('uninstall.scanning')" />
      <p class="m-0 mb-6px mt-8px h-18px truncate font-mono text-11.5px text-ink-tertiary">
        {{ store.progress ? displayPath(store.progress.location) : '' }}
      </p>
      <div class="flex flex-col gap-8px">
        <OnSkeleton v-for="row in 5" :key="row" height="40px" radius="default" />
      </div>
    </div>

    <!-- Failed scan -->
    <div v-else-if="store.stage === 'failed'" class="pb-8px">
      <p class="m-0 text-13px text-ink-secondary">
        {{ t('uninstall.scanFailed', { reason: store.error ? errorText(store.error) : '' }) }}
      </p>
    </div>

    <!-- Review -->
    <div v-else-if="store.stage === 'review' && store.plan" class="pb-12px">
      <PlanNotices />
      <div class="mb-8px flex flex-wrap items-center gap-8px text-12.5px text-ink-tertiary">
        <span>{{
          t('uninstall.selection', {
            selected: store.chosen.length,
            total: store.totalItems,
            size: format(store.chosenBytes)
          })
        }}</span>
        <OnChip v-if="store.adminCount" icon="key-round" size="sm">{{
          t('uninstall.admin', { count: store.adminCount }, { plural: store.adminCount })
        }}</OnChip>
        <OnChip v-if="store.sharedKept" tone="warning" icon="users" size="sm">{{
          t('uninstall.sharedKept', { count: store.sharedKept }, { plural: store.sharedKept })
        }}</OnChip>
        <span class="ml-auto flex gap-4px">
          <OnButton variant="ghost" size="xs" @click="store.selectAll()">{{ t('uninstall.selectAll') }}</OnButton>
          <OnButton variant="ghost" size="xs" @click="store.selectRequired()">{{ t('uninstall.onlyApp') }}</OnButton>
          <OnButton variant="ghost" size="xs" @click="store.recommended()">{{ t('uninstall.recommended') }}</OnButton>
        </span>
      </div>
      <PlanGroups v-if="store.apps.length === 1 && store.apps[0]" :app="store.apps[0]" @reveal="reveal" />
      <BatchPlanList v-else :apps="store.apps" @reveal="reveal" />
    </div>

    <!-- Running -->
    <UninstallProgress
      v-else-if="store.stage === 'submitting' || store.stage === 'running'"
      :tasks="store.sessionTasks"
    />

    <!-- Done -->
    <UninstallResult
      v-else-if="store.stage === 'done'"
      :result="store.result"
      :title="resultTitle"
      :brew="wasBrew"
      :apps="targets"
      @retry="retry"
    />

    <template #footer>
      <template v-if="store.stage === 'review'">
        <span class="mr-auto flex items-center gap-10px">
          <OnSegmented
            :model-value="store.mode"
            :options="modes"
            size="sm"
            :aria-label="t('uninstall.mode.label')"
            @update:model-value="(value: DeleteMode) => (store.mode = value)"
          />
          <span v-if="store.mode === 'delete'" class="text-12px text-status-danger">{{
            t('uninstall.mode.deleteWarning')
          }}</span>
        </span>
        <OnButton variant="secondary" @click="store.close()">{{ t('uninstall.cancel') }}</OnButton>
        <OnButton variant="danger" icon="trash" :disabled="!store.included.length || store.blocked" @click="confirm">{{
          confirmLabel
        }}</OnButton>
      </template>
      <template v-else-if="store.stage === 'permission'">
        <OnButton variant="secondary" @click="store.close()">{{ t('uninstall.cancel') }}</OnButton>
      </template>
      <template v-else-if="store.stage === 'scanning'">
        <OnButton variant="secondary" @click="store.close()">{{ t('uninstall.cancel') }}</OnButton>
        <OnButton variant="danger" icon="trash" disabled>{{ t('uninstall.confirm') }}</OnButton>
      </template>
      <template v-else-if="store.stage === 'failed'">
        <OnButton variant="secondary" @click="store.close()">{{ t('uninstall.cancel') }}</OnButton>
        <OnButton variant="primary" icon="refresh-cw" @click="store.scan()">{{ t('uninstall.retry') }}</OnButton>
      </template>
      <template v-else-if="store.stage === 'submitting' || store.stage === 'running'">
        <span class="mr-auto self-center text-12px text-ink-tertiary">{{ t('uninstall.backgroundHint') }}</span>
        <OnButton variant="secondary" @click="store.close()">{{ t('uninstall.background') }}</OnButton>
      </template>
      <template v-else-if="store.stage === 'done'">
        <template v-if="store.result.restorable">
          <OnButton variant="secondary" icon="undo-2" @click="undo()">{{ t('uninstall.result.undo') }}</OnButton>
          <OnButton variant="ghost" class="mr-auto" @click="purge()">{{ t('uninstall.result.purge') }}</OnButton>
        </template>
        <OnButton variant="primary" @click="store.reset()">{{ t('uninstall.result.done') }}</OnButton>
      </template>
    </template>
  </OnModal>
</template>
