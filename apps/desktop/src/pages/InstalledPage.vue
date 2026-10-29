<script setup lang="ts">
import PermissionGate from '@/components/permissions/PermissionGate.vue';
import { useToasts } from '@/composables/useToasts';
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatDate, formatPercent, formatVersion } from '@opennavo/shared';
import {
  OnAppIcon,
  OnButton,
  OnChip,
  OnEmpty,
  OnIcon,
  OnMenu,
  OnPageHeader,
  OnSearchField,
  OnSegmented,
  OnSkeleton,
  OnTable,
  OnTooltip
} from '@opennavo/ui';
import type { OnChipTone, OnSegmentedOption, OnTableColumn, OnTableSort } from '@opennavo/ui';
import FootprintScanProgress from '@/components/installed/FootprintScanProgress.vue';
import AppInfoSheet from '@/components/installed/AppInfoSheet.vue';
import PreviewNotice from '@/components/common/PreviewNotice.vue';
import FullDiskAccessGate from '@/components/permissions/FullDiskAccessGate.vue';
import RunningTask from '@/components/updates/RunningTask.vue';
import type { LocalApp } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLocalAppMenu } from '@/composables/useLocalAppMenu';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { useLibraryStore, usePermissionsStore, useTasksStore, useUpdatesStore } from '@/stores';
import { UNUSED_DAYS, isUnused, matchesSource, useLocalAppsStore } from '@/stores/localApps';
import { useInstalledFilterStore } from '@/stores/installedFilter';
import { useUninstallStore } from '@/stores/uninstall';
import { matchLocalApps, NO_PREVIEW, uninstallPreview } from '@/utils/deepLinkPreview';

// Installed (proposal §03, §05): every removable app on this Mac — Homebrew, App Store, drag-installed and .pkg —
// with source filter, unused filter, multi-select uninstall and an info sheet. System apps are not listed.
// The installed list is measured only after both permissions are ready.
const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const router = useRouter();
const route = useRoute();
const localApps = useLocalAppsStore();
const library = useLibraryStore();
const updates = useUpdatesStore();
const tasks = useTasksStore();
const uninstaller = useUninstallStore();
const { localApp } = usePackageIdentity();
const menu = useLocalAppMenu();
const toasts = useToasts();
const permissions = usePermissionsStore();
const gate = usePermissionGateStore();
const ready = computed(() => gate.ready);

const footprintError = (id: string) => {
  const error = localApps.footprintErrors[id];
  if (!error) return '';
  if (error.code === 'E_FULL_DISK_ACCESS') return t('appInfo.footprintIncomplete');
  return te(`errors.${error.code}`) ? t(`errors.${error.code}`) : error.message;
};
const incomplete = (id: string) => localApps.footprints[id]?.complete === false;
/** Why a value is only approximate: no Full Disk Access, or files that could not be read. */
const partialNote = computed(() =>
  permissions.fullDiskAccess === 'granted' ? t('appInfo.footprintPartial') : t('appInfo.footprintIncomplete')
);
async function retryFootprint(id: string) {
  try {
    await localApps.retryFootprint(id);
  } catch {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
  }
}

const filters = useInstalledFilterStore();
const sort = ref<OnTableSort>({ key: 'size', order: 'desc' });
const selected = ref<string[]>([]);
const sheetId = ref<string | null>(null);

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const nameOf = (app: LocalApp) => localApp(app).name;
// Same measure as the size column: app, data and caches once known.
const totalBytes = computed(() => localApps.items.reduce((total, app) => total + localApps.spaceOf(app), 0));

const sources = computed<OnSegmentedOption<typeof filters.source>[]>(() =>
  (['all', 'homebrew', 'app_store', 'other'] as const).map(value => ({
    value,
    label: t(`library.sources.${value}`),
    count: localApps.items.filter(app => matchesSource(app, value)).length
  }))
);
const unusedCount = computed(() => localApps.items.filter(app => isUnused(app)).length);

interface Status {
  label: string;
  tone: OnChipTone;
  icon?: string;
  title?: string;
}

function taskFor(app: LocalApp) {
  const installed = app.cask ? library.find('cask', app.cask) : undefined;
  const token = app.catalogToken ?? installed?.token ?? app.cask;
  return (
    tasks.forPackage('app', app.id) ??
    (token && app.source === 'homebrew'
      ? (tasks.forPackage('cask', token) ?? (app.cask ? tasks.forPackage('cask', app.cask) : undefined))
      : undefined)
  );
}

function statusOf(app: LocalApp): Status | null {
  const task = taskFor(app);
  if (task?.state === 'running') {
    const key = task.op === 'uninstall' ? 'uninstalling' : 'running';
    return {
      label:
        task.percent === null
          ? t('library.status.runningUnknown')
          : t(`library.status.${key}`, {
              percent: formatPercent(task.percent / 100, {
                locale: appLocale.value
              })
            }),
      tone: 'coral'
    };
  }
  if (task?.state === 'queued') return { label: t('library.status.queued'), tone: 'neutral' };
  if (!app.removable)
    return {
      label: t(app.protectedReason === 'managed' ? 'library.status.managed' : 'library.status.protected'),
      tone: 'neutral',
      icon: 'lock'
    };
  if (app.source === 'homebrew') {
    const item = library.find('cask', app.catalogToken ?? app.cask ?? '');
    if (item?.pinned)
      return {
        label: t('library.status.pinned'),
        tone: 'neutral',
        icon: 'pin'
      };
    const updateToken = app.catalogToken ?? item?.token;
    if (updateToken && updates.find('cask', updateToken))
      return { label: t('library.status.outdated'), tone: 'accent' };
    if (item?.status === 'unknown') return { label: t('library.status.unknown'), tone: 'neutral' };
    return { label: t('library.status.latest'), tone: 'success' };
  }
  // App Store copies stay with the App Store: adopting would let Homebrew replace a licensed build.
  if (app.catalogToken && app.source !== 'app_store')
    return {
      label: t('library.status.adoptable'),
      tone: 'info',
      title: t('library.adoptableHint')
    };
  return null;
}

const sorted = computed(() => {
  const q = filters.query.trim().toLowerCase();
  const list = localApps.items.filter(
    app =>
      matchesSource(app, filters.source) &&
      (!filters.unusedOnly || isUnused(app)) &&
      (!q || [nameOf(app), app.name, app.bundleId, app.cask].some(value => value?.toLowerCase().includes(q)))
  );
  const direction = sort.value.order === 'asc' ? 1 : -1;
  const collator = new Intl.Collator(appLocale.value);
  const compare: Record<string, (a: LocalApp, b: LocalApp) => number> = {
    name: (a, b) => collator.compare(nameOf(a), nameOf(b)),
    size: (a, b) => (localApps.occupiedBytes(a.id) ?? -1) - (localApps.occupiedBytes(b.id) ?? -1),
    // Unknown last-used times sort as the oldest, so "longest unused first" stays useful.
    lastUsed: (a, b) => (a.lastUsedAt ?? 0) - (b.lastUsedAt ?? 0)
  };
  const by = compare[sort.value.key] ?? compare.name;
  return [...list].sort((a, b) => (by ? by(a, b) * direction : 0));
});

// While footprints are measured, values arrive one app at a time; keep the order the user saw instead of
// reshuffling the table under the pointer, and sort once when the measurement ends (or the user sorts).
const measuring = computed(() => localApps.scanProgress?.running === true);
const frozen = ref<string[] | null>(null);
watch(
  measuring,
  now => {
    frozen.value = now && sort.value.key === 'size' ? sorted.value.map(app => app.id) : null;
  },
  { immediate: true }
);
watch(sort, () => (frozen.value = null), { deep: true });
const rows = computed(() => {
  if (!frozen.value) return sorted.value;
  const rank = new Map(frozen.value.map((id, index) => [id, index]));
  const last = rank.size;
  return [...sorted.value].sort((a, b) => (rank.get(a.id) ?? last) - (rank.get(b.id) ?? last));
});

// What is selected is what is shown: a filter change drops selections it hides, so a batch never includes
// apps that are out of sight.
watch(
  () => [filters.source, filters.unusedOnly, filters.query],
  () => {
    const visible = new Set(rows.value.map(app => app.id));
    if (selected.value.some(id => !visible.has(id))) selected.value = selected.value.filter(id => visible.has(id));
  }
);

// The progress card is for an ongoing full measurement or failures; a finished one gets out of the way.
const showProgress = ref(false);
let hideProgress: ReturnType<typeof setTimeout> | undefined;
watch(
  () => localApps.scanProgress,
  progress => {
    clearTimeout(hideProgress);
    if (!progress) showProgress.value = false;
    else if (progress.running) showProgress.value = showProgress.value || progress.total > 1;
    else if (progress.failed) showProgress.value = true;
    else if (showProgress.value) hideProgress = setTimeout(() => (showProgress.value = false), 2500);
  },
  { immediate: true }
);
onBeforeUnmount(() => clearTimeout(hideProgress));

// Widths fit the default 1280 px window with the rail docked; the name column takes the rest and truncates.
// The source sits in the name's second line (and the filter above), leaving room for names.
const columns = computed<OnTableColumn[]>(() => [
  { key: 'name', label: t('library.columns.name'), sortable: true },
  {
    key: 'version',
    label: t('library.columns.version'),
    mono: true,
    width: '96px'
  },
  {
    key: 'size',
    label: t('library.columns.size'),
    numeric: true,
    sortable: true,
    width: '104px'
  },
  {
    key: 'lastUsed',
    label: t('library.columns.lastUsed'),
    sortable: true,
    width: '96px'
  },
  { key: 'status', label: t('library.columns.status'), width: '112px' },
  {
    key: 'actions',
    label: t('library.columns.actions'),
    hideLabel: true,
    width: '48px'
  }
]);

const SOURCE_DOT: Record<LocalApp['source'], string> = {
  homebrew: 'bg-chart-series1',
  app_store: 'bg-status-info',
  direct: 'bg-chart-series3',
  pkg: 'bg-chart-series3'
};

const DAY = 86_400_000;
/** Coarse relative time (今天 / 3 天前 / 4 个月前 / 去年): staleness matters more here than the exact date. */
function lastUsed(app: LocalApp) {
  if (app.lastUsedAt === null) return '—';
  const days = Math.max(0, Math.floor((Date.now() - app.lastUsedAt) / DAY));
  const relative = new Intl.RelativeTimeFormat(appLocale.value, {
    numeric: 'auto'
  });
  if (days < 7) return relative.format(-days, 'day');
  if (days < 30) return relative.format(-Math.floor(days / 7), 'week');
  if (days < 365) return relative.format(-Math.floor(days / 30), 'month');
  return relative.format(-Math.floor(days / 365), 'year');
}

const chosen = computed(() =>
  localApps.items.filter(app => selected.value.includes(app.id) && app.removable && !taskFor(app))
);
const chosenBytes = computed(() => chosen.value.reduce((total, app) => total + (app.sizeBytes ?? 0), 0));

async function uninstallSelected() {
  const ids = chosen.value.map(app => app.id);
  selected.value = [];
  await uninstall(ids);
}

async function uninstall(ids: string[]) {
  try {
    await uninstaller.start(ids);
  } catch {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
  }
}

const sheetApp = computed(() => (sheetId.value ? (localApps.find(sheetId.value) ?? null) : null));
const sheetOpen = computed({
  get: () => sheetApp.value !== null,
  set: value => {
    if (!value) sheetId.value = null;
  }
});

function select(app: LocalApp, key: string) {
  if (key === 'uninstall') sheetId.value = app.id;
  else void menu.select(app, nameOf(app), key);
}

async function openDetail(token: string) {
  sheetId.value = null;
  await router.push(`/package/cask/${token}`);
}

async function openAppStore(adamId: number) {
  sheetId.value = null;
  await router.push({ name: 'appstore', params: { id: String(adamId) } });
}

function uninstallFromSheet(app: LocalApp) {
  sheetId.value = null;
  void uninstall([app.id]);
}

// Uninstall preview (F1): opennavo://uninstall/{token}?preview=1 opens /uninstall/{token}?preview=1 here. The
// app on this Mac is found and its info sheet opened; uninstalling still takes the sheet's button and the
// dialog's review and confirmation. A link for an app that is not here, or a malformed one, is only said so.
const preview = computed(() =>
  route.name === 'uninstall-preview' ? uninstallPreview(route.params.token, route.query) : NO_PREVIEW
);
const previewToken = computed(() => (preview.value.state === 'ok' ? preview.value.value : null));
const previewApps = computed(() => (previewToken.value ? matchLocalApps(localApps.items, previewToken.value) : []));
const previewName = computed(() => (previewApps.value[0] ? nameOf(previewApps.value[0]) : ''));
const previewState = computed(() => {
  if (preview.value.state === 'none') return null;
  if (preview.value.state === 'invalid') return 'invalid';
  if (permissions.status && !ready.value) return 'permission';
  if (!localApps.loaded) return null;
  return previewApps.value.length ? 'found' : 'notFound';
});
let previewed: string | null = null;
watch(
  [previewToken, previewApps, ready],
  ([token, apps]) => {
    if (!token) {
      previewed = null;
      return;
    }
    if (!ready.value) {
      sheetId.value = null;
      previewed = null;
      return;
    }
    const only = apps.length === 1 ? apps[0] : undefined;
    if (previewed === token || !only) return;
    previewed = token;
    sheetId.value = only.id;
  },
  { immediate: true }
);

if (!permissions.status) void permissions.refresh().catch(() => undefined);
// Measuring starts only once both permissions are ready.
watch(
  ready,
  now => {
    if (now)
      void localApps.enterInstalled().catch(() => toasts.push({ tone: 'danger', title: t('errors.actionFailed') }));
  },
  { immediate: true }
);
async function refreshInstalled() {
  try {
    await localApps.scan(true);
  } catch {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed') });
  }
}
</script>

<template>
  <div class="relative">
    <PermissionGate feature="installed">
      <template #header>
        <OnPageHeader
          :title="t('library.title')"
          :subtitle="
            ready && localApps.loaded
              ? t(
                  'library.summary',
                  { count: localApps.items.length, size: format(totalBytes) },
                  { plural: localApps.items.length }
                )
              : undefined
          "
        >
          <template v-if="ready" #actions>
            <OnButton variant="secondary" icon="refresh-cw" :loading="localApps.refreshing" @click="refreshInstalled">
              {{ t('library.recalculate') }}
            </OnButton>
          </template>
        </OnPageHeader>
      </template>

      <!-- F1: what an uninstall preview link asked for; the info sheet and the uninstall dialog do the rest. -->
      <PreviewNotice
        v-if="previewState === 'invalid'"
        class="mb-16px"
        tone="warning"
        :title="t('deeplink.preview.invalid')"
        :body="t('deeplink.preview.invalidBody')"
      />
      <PreviewNotice
        v-else-if="previewState === 'permission'"
        class="mb-16px"
        :title="t('deeplink.preview.uninstall.permission', { token: previewToken ?? '' })"
      />
      <PreviewNotice
        v-else-if="previewState === 'found'"
        class="mb-16px"
        :title="t('deeplink.preview.uninstall.title', { name: previewName })"
        :body="
          previewApps.length > 1
            ? t('deeplink.preview.uninstall.several', { count: previewApps.length, token: previewToken ?? '' })
            : t('deeplink.preview.uninstall.body')
        "
      >
        <div v-if="previewApps.length > 1" class="flex flex-wrap gap-6px">
          <OnButton v-for="app in previewApps" :key="app.id" size="xs" variant="secondary" @click="sheetId = app.id">{{
            t('deeplink.preview.uninstall.view', { name: `${nameOf(app)} (${app.path})` })
          }}</OnButton>
        </div>
      </PreviewNotice>
      <PreviewNotice
        v-else-if="previewState === 'notFound'"
        class="mb-16px"
        :title="t('deeplink.preview.uninstall.notFound', { token: previewToken ?? '' })"
        :body="t('deeplink.preview.uninstall.notFoundBody')"
      />

      <!-- Homebrew's cache cleanup reports here whatever the permission. -->
      <div v-if="tasks.running?.op === 'cleanup'" data-permission-free class="mb-20px">
        <RunningTask :task="tasks.running" />
      </div>

      <FullDiskAccessGate v-if="permissions.status && !ready" />
      <template v-else-if="ready">
        <div class="mb-14px flex flex-wrap items-center gap-10px">
          <OnSegmented v-model="filters.source" :options="sources" :aria-label="t('library.sourceFilter')" />
          <OnButton
            variant="ghost"
            size="sm"
            icon="hourglass"
            :class="filters.unusedOnly ? '!border-brand-coral !bg-accent-coral-subtle !text-ink-accent' : ''"
            :aria-pressed="filters.unusedOnly"
            :title="t('library.unusedHint', { days: UNUSED_DAYS })"
            @click="filters.showUnused(!filters.unusedOnly)"
          >
            {{ t('library.unused') }}
            <span class="tabular-nums opacity-80">{{ unusedCount }}</span>
          </OnButton>
          <OnSearchField
            v-model="filters.query"
            class="ml-auto w-220px"
            :placeholder="t('library.filterPlaceholder')"
            clearable
          />
        </div>

        <FootprintScanProgress v-if="showProgress && localApps.scanProgress" :progress="localApps.scanProgress" />

        <OnTable
          v-if="rows.length"
          v-model:sort="sort"
          v-model:selected="selected"
          layout="fixed"
          selectable
          :columns="columns"
          :rows="rows"
          :row-key="app => app.id"
          :can-select="app => app.removable && !taskFor(app)"
          :select-all-label="t('library.selectAll')"
          :select-row-label="app => t('library.selectRow', { name: nameOf(app) })"
          :dimmed="app => !app.removable"
          :caption="t('library.title')"
          @row-click="(_, app) => (sheetId = app.id)"
        >
          <template #cell-name="{ row }">
            <span class="flex min-w-0 items-center gap-10px">
              <OnAppIcon v-bind="localApp(row).icon" :size="32" />
              <span class="min-w-0">
                <button
                  type="button"
                  class="m-0 block max-w-full cursor-pointer truncate rounded-tiny border-none bg-transparent p-0 text-left font-sans text-13px font-600 text-ink-primary outline-none transition-colors duration-fast ease-standard underline-offset-3 hover:text-ink-accent hover:underline focus-visible:text-ink-accent focus-visible:shadow-focus-ring"
                  @click.stop="sheetId = row.id"
                >
                  {{ nameOf(row) }}
                </button>
                <span class="flex min-w-0 items-center gap-4px text-11.5px text-ink-tertiary">
                  <template v-if="row.running">
                    <span class="inline-flex shrink-0 items-center gap-4px text-status-success">
                      <span class="h-6px w-6px rounded-full bg-status-success" aria-hidden="true"></span>
                      {{ t('library.running') }}
                    </span>
                    <span aria-hidden="true">·</span>
                  </template>
                  <span class="inline-flex shrink-0 items-center gap-5px">
                    <span class="h-6px w-6px rounded-[2px]" :class="SOURCE_DOT[row.source]" aria-hidden="true"></span>
                    {{ t(`library.sources.${row.source}`) }}
                  </span>
                  <span aria-hidden="true">·</span>
                  <span class="truncate">{{
                    row.protectedReason === 'self' ? t('library.self') : (row.cask ?? row.bundleId ?? row.path)
                  }}</span>
                </span>
              </span>
            </span>
          </template>
          <template #cell-version="{ row }">
            <span class="block truncate" :title="row.version ?? undefined">{{
              row.version ? formatVersion(row.version) : '—'
            }}</span>
          </template>
          <template #cell-size="{ row }">
            <span class="inline-flex max-w-full items-center justify-end gap-4px">
              <!-- A failed refresh keeps the last value; the warning offers to measure again. -->
              <span
                v-if="localApps.occupiedBytes(row.id) !== null"
                class="truncate"
                :title="incomplete(row.id) ? partialNote : undefined"
                >{{ incomplete(row.id) ? '≈ ' : '' }}{{ format(localApps.occupiedBytes(row.id)!) }}</span
              >
              <span v-else-if="!localApps.footprintErrors[row.id]">—</span>
              <OnTooltip
                v-if="localApps.footprintErrors[row.id]"
                :content="`${footprintError(row.id)} · ${t('library.retryFootprint')}`"
              >
                <button
                  type="button"
                  class="inline-flex max-w-104px cursor-pointer items-center truncate border-none bg-transparent p-0 text-11px text-status-warning hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-brand-coral"
                  :aria-label="`${footprintError(row.id)} · ${t('library.retryFootprint')}`"
                  data-testid="retry-footprint"
                  @click.stop="retryFootprint(row.id)"
                >
                  <template v-if="localApps.occupiedBytes(row.id) === null">{{ footprintError(row.id) }}</template>
                  <OnIcon v-else name="triangle-alert" :size="12" />
                </button>
              </OnTooltip>
            </span>
          </template>
          <template #cell-lastUsed="{ row }">
            <span
              :class="isUnused(row) ? 'text-status-warning' : ''"
              :title="row.lastUsedAt === null ? undefined : formatDate(row.lastUsedAt, { locale: appLocale })"
              >{{ lastUsed(row) }}</span
            >
          </template>
          <template #cell-status="{ row }">
            <!-- Long labels in some languages clip inside the cell instead of covering the menu column. -->
            <span class="block overflow-hidden">
              <OnChip
                v-if="statusOf(row)"
                :tone="statusOf(row)!.tone"
                :icon="statusOf(row)!.icon"
                :dot="!statusOf(row)!.icon && statusOf(row)!.tone !== 'info'"
                :title="statusOf(row)!.title ?? statusOf(row)!.label"
                >{{ statusOf(row)!.label }}</OnChip
              >
              <span v-else class="text-ink-disabled">—</span>
            </span>
          </template>
          <template #cell-actions="{ row }">
            <span @click.stop>
              <OnMenu
                :items="menu.itemsFor(row)"
                :label="t('package.actions.label')"
                variant="ghost"
                size="sm"
                @select="key => select(row, key)"
              />
            </span>
          </template>
        </OnTable>
        <OnEmpty
          v-else-if="localApps.loaded"
          :title="localApps.items.length ? t('library.emptyFiltered') : t('library.empty')"
          icon="lucide:package"
        />
        <div v-else class="flex flex-col gap-8px" role="status" :aria-label="t('common.loading')">
          <OnSkeleton v-for="line in 8" :key="line" height="48px" radius="default" />
        </div>

        <p class="m-0 mt-12px flex items-center gap-6px text-12px text-ink-disabled">
          <OnIcon name="lock" :size="12" />{{ t('library.systemNote') }}
        </p>

        <div v-if="chosen.length" class="sticky bottom-12px z-sticky mt-14px">
          <div
            class="flex items-center gap-12px rounded-big border border-solid border-line-default bg-component-toast-bg px-12px py-10px shadow-toast backdrop-blur-[20px]"
            role="region"
            :aria-label="t('library.batch.selected', { count: chosen.length }, { plural: chosen.length })"
          >
            <span
              class="grid h-26px min-w-26px place-items-center rounded-small bg-brand-coral px-6px text-13px font-700 text-ink-on-accent"
              >{{ chosen.length }}</span
            >
            <span class="min-w-0 flex-1">
              <span class="block text-13px font-600 text-ink-primary">{{
                t('library.batch.selected', { count: chosen.length }, { plural: chosen.length })
              }}</span>
              <span class="block truncate text-12px text-ink-tertiary">{{
                t('library.batch.size', { size: format(chosenBytes) })
              }}</span>
            </span>
            <OnButton variant="ghost" size="sm" @click="selected = []">{{ t('library.batch.clear') }}</OnButton>
            <OnButton variant="danger" size="sm" icon="trash" @click="uninstallSelected">{{
              t('library.batch.uninstall', { count: chosen.length }, { plural: chosen.length })
            }}</OnButton>
          </div>
        </div>
      </template>

      <AppInfoSheet
        v-model:open="sheetOpen"
        :app="sheetApp"
        @uninstall="uninstallFromSheet"
        @detail="openDetail"
        @app-store="openAppStore"
      />
    </PermissionGate>
  </div>
</template>
