<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnConfirm, OnEmpty, OnIcon, OnSkeleton } from '@opennavo/ui';
import type { LocalApp, StartupAction, StartupItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { usePermissionsStore } from '@/stores';
import { useSecurityStore } from '@/stores/security';
import StartupAddSheet from './StartupAddSheet.vue';
import StartupAppRow from './StartupAppRow.vue';
import StartupItemSheet from './StartupItemSheet.vue';
import { running, startupApps } from './meta';

// Startup items (plan §6.3, mockup E), by app: what people recognize, with its icon, new and broken ones first, each
// with a switch where OpenNavo can switch it. macOS's own items are never listed; command-line tools, scripts,
// extensions and plugins wait under "Other" unless they are new or broken. The last list shows at once while a scan
// runs. Remove first disables, then moves the plist into quarantine, so it can be restored.
const { t, te } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const store = useSecurityStore();
const permissions = usePermissionsStore();
const { appLocale } = useAppLocale();

const selectedId = ref<string | null>(null);
const removing = ref<StartupItem | null>(null);
const loadError = ref<unknown>(null);
const othersOpen = ref(false);
const adding = ref(false);
const addingPath = ref<string | null>(null);

onMounted(async () => {
  try {
    await store.loadStartup();
  } catch (error) {
    loadError.value = error;
  }
});

const snapshot = computed(() => store.startup);
// Names sort as on Installed: in the interface language.
const grouped = computed(() => startupApps(snapshot.value?.items ?? [], new Intl.Collator(appLocale.value)));
const selected = computed(() => snapshot.value?.items.find(item => item.id === selectedId.value) ?? null);
const missing = computed(() =>
  (snapshot.value?.missing ?? [])
    .filter(category => category !== 'cron' || !snapshot.value?.cronError)
    .map(category => t(`security.startup.category.${category}`))
    .join('、')
);

// Current-user crontab lines and legacy login items are removed with a backup the same item restores (plan §6 S2);
// launchd items go to Quarantine instead.
const keepsBackup = (item: StartupItem) => item.category === 'cron' || item.category === 'login_items';

async function act(item: StartupItem, action: StartupAction | 'mark_reviewed') {
  if (action === 'remove' && removing.value?.id !== item.id) {
    removing.value = item;
    return;
  }
  removing.value = null;
  try {
    const outcome = await store.actOnStartup([item.id], action);
    const failure = outcome.failed[0];
    if (failure) {
      const key = `errors.${failure.code}`;
      toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: te(key) ? t(key) : failure.code });
      return;
    }
    if (action === 'remove') {
      selectedId.value = null;
      const title = keepsBackup(item) ? 'security.startup.removedBackup' : 'security.startup.removed';
      toasts.push({ tone: 'success', title: t(title, { name: item.name }) });
    } else if (action === 'restore')
      toasts.push({ tone: 'success', title: t('security.startup.restored', { name: item.name }) });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

/** An app's switch: one request, so system items ask for the administrator password once. */
async function actMany(items: StartupItem[], action: 'enable' | 'disable') {
  try {
    const outcome = await store.actOnStartup(
      items.map(item => item.id),
      action
    );
    const failure = outcome.failed[0];
    if (failure) {
      const key = `errors.${failure.code}`;
      toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: te(key) ? t(key) : failure.code });
    }
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Apps that already open at login are not offered again.
const opening = computed(
  () =>
    new Set(
      (snapshot.value?.items ?? [])
        .filter(item => item.category === 'login_items' && running(item))
        .map(item => item.path)
    )
);
async function addApp(app: LocalApp) {
  addingPath.value = app.path;
  try {
    const item = await store.addLoginItem(app.path);
    adding.value = false;
    toasts.push({ tone: 'success', title: t('security.startup.added', { name: item.owner?.name ?? app.name }) });
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    addingPath.value = null;
  }
}

async function grant() {
  try {
    await permissions.guide('full_disk_access');
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div class="flex flex-col gap-14px">
    <div class="flex items-center gap-10px">
      <span v-if="store.startupRefreshing && snapshot" class="flex items-center gap-6px text-12px text-ink-tertiary">
        <OnIcon name="loader-circle" :size="14" class="animate-spin" />{{ t('security.startup.refreshing') }}
      </span>
      <OnButton class="ml-auto" variant="secondary" size="sm" icon="plus" @click="adding = true">{{
        t('security.startup.add')
      }}</OnButton>
    </div>
    <div
      v-if="missing"
      class="flex flex-wrap items-center gap-10px rounded-big bg-status-info-subtle px-14px py-10px text-12.5px text-ink-secondary"
      role="status"
    >
      <span class="min-w-0 flex-1">{{ t('security.startup.missing', { categories: missing }) }}</span>
      <OnButton
        v-if="permissions.fullDiskAccess === 'denied' && !permissions.fullDiskAccessPending"
        variant="secondary"
        size="sm"
        @click="grant"
        >{{ t('security.startup.grant') }}</OnButton
      >
    </div>
    <p v-if="snapshot?.cronError" class="m-0 text-12.5px text-status-warning" role="status">
      {{ t('security.startup.category.cron') }} ·
      {{ te(`errors.${snapshot.cronError}`) ? t(`errors.${snapshot.cronError}`) : snapshot.cronError }}
    </p>
    <p v-if="snapshot?.btmFormat === 'unknown'" class="m-0 text-12.5px text-status-warning" role="status">
      {{ t('security.startup.btmUnknown') }}
    </p>

    <template v-if="snapshot && (grouped.apps.length || grouped.others.length)">
      <ul v-if="grouped.apps.length" class="m-0 list-none rounded-big border border-solid border-line-subtle p-0">
        <StartupAppRow
          v-for="app in grouped.apps"
          :key="app.key"
          :app="app"
          :busy="store.busy"
          @select="item => (selectedId = item.id)"
          @act="act"
          @act-many="actMany"
        />
      </ul>
      <p v-else class="m-0 text-13px text-ink-secondary">{{ t('security.startup.noApps') }}</p>

      <section v-if="grouped.others.length" class="rounded-big border border-solid border-line-subtle">
        <button
          type="button"
          class="flex w-full cursor-pointer items-center gap-10px border-none bg-transparent px-14px py-10px text-left font-sans outline-none focus-visible:underline"
          :aria-expanded="othersOpen"
          @click="othersOpen = !othersOpen"
        >
          <OnIcon :name="othersOpen ? 'chevron-down' : 'chevron-right'" :size="14" class="text-ink-tertiary" />
          <span class="shrink-0 text-13px font-600 text-ink-primary">{{
            t('security.startup.others', { count: grouped.others.length }, { plural: grouped.others.length })
          }}</span>
          <span class="min-w-0 truncate text-12px text-ink-tertiary">{{ t('security.startup.othersHint') }}</span>
        </button>
        <ul v-if="othersOpen" class="m-0 list-none border-t border-t-solid border-line-subtle p-0">
          <StartupAppRow
            v-for="item in grouped.others"
            :key="item.id"
            :app="{ key: item.id, owner: null, items: [item] }"
            :busy="store.busy"
            @select="item => (selectedId = item.id)"
            @act="act"
          />
        </ul>
      </section>
    </template>
    <OnEmpty
      v-else-if="snapshot"
      :title="t('security.startup.none')"
      :description="t('security.startup.noneHint')"
      icon="lucide:shield-check"
    />
    <p v-else-if="loadError" class="m-0 text-13px text-status-danger" role="alert">{{ errorText(loadError) }}</p>
    <div v-else class="flex flex-col gap-8px" aria-hidden="true">
      <OnSkeleton v-for="index in 5" :key="index" height="52px" radius="default" />
    </div>

    <StartupAddSheet :open="adding" :opening="opening" :adding="addingPath" @close="adding = false" @add="addApp" />
    <StartupItemSheet
      :item="selected"
      :busy="selected ? store.busy.has(selected.id) : false"
      @close="selectedId = null"
      @act="action => selected && act(selected, action)"
    />
    <OnConfirm
      :open="Boolean(removing)"
      :title="t('security.startup.removeTitle', { name: removing?.name ?? '' })"
      :description="
        removing && keepsBackup(removing)
          ? t('security.startup.removeBodyBackup')
          : removing?.requiresAdmin
            ? t('security.startup.removeBodyAdmin')
            : t('security.startup.removeBody')
      "
      :confirm-label="t('security.startup.remove')"
      tone="danger"
      @confirm="removing && act(removing, 'remove')"
      @cancel="removing = null"
    />
  </div>
</template>
