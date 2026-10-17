<script setup lang="ts">
import { computed, useTemplateRef } from 'vue';
import { useI18n } from 'vue-i18n';
import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import { emitTo } from '@tauri-apps/api/event';
import { getAllWindows, getCurrentWindow } from '@tauri-apps/api/window';
import { formatPercent, formatVersionChange } from '@opennavo/shared';
import { OnAppIcon, OnButton, OnIcon, OnLogo, OnProgressMeter } from '@opennavo/ui';
import type { DeepLinkEvent, OutdatedItem, TaskKind } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useTrayWindowSize } from '@/composables/useTrayWindowSize';
import { useLibraryStore, useTasksStore, useUpdatesStore } from '@/stores';
import { rowKey, updateRequest } from '@/stores/updates';

// Menu-bar popover (06 §4.2, mockup 07): update count, running tasks, update list, individual updates, and Update All.
// Open OpenNavo and Settings show the main window and navigate through deeplink:received. Tray capability permits only installed, update, and task commands.
// Names come from installed entries; icons are extracted locally by Rust, falling back to letter tiles.
const VISIBLE = 3;

const content = useTemplateRef<HTMLDivElement>('content');
useTrayWindowSize(content);

const { t } = useI18n();
const { appLocale } = useAppLocale();
const library = useLibraryStore();
const updates = useUpdatesStore();
const tasks = useTasksStore();
const toasts = useToasts();
const { errorText } = usePackageState();

// Local apps outside Homebrew (`app`) and App Store apps (`mas`) are not in the library; use the name recorded on the task.
const inLibrary = (kind: TaskKind): kind is Exclude<TaskKind, 'app' | 'mas'> => kind !== 'app' && kind !== 'mas';
const nameOf = (kind: TaskKind, token: string, fallback?: string | null) =>
  (inLibrary(kind) ? library.find(kind, token)?.name : undefined) ?? fallback ?? token;

function iconOf(kind: TaskKind, token: string) {
  const path = inLibrary(kind) ? library.find(kind, token)?.iconPath : null;
  return path && isTauri() ? convertFileSrc(path) : null;
}

const running = computed(() => {
  const task = tasks.running;
  if (!task?.target) return null;
  // Clean Up targets its report, not a package: show the action with an eraser tile.
  if (task.op === 'clean') return { clean: true as const, text: t('tasks.running.clean'), percent: task.percent };
  const { kind, token } = task.target;
  const key = task.op === 'upgrade' || task.op === 'install' || task.op === 'uninstall' ? task.op : 'other';
  const name = nameOf(kind, token, task.targetName);
  return {
    clean: false as const,
    kind,
    iconKind: kind === 'app' ? ('cask' as const) : kind,
    token,
    name,
    text: t(`tray.running.${key}`, { name }),
    percent: task.percent
  };
});

const rows = computed(() => updates.actionable.slice(0, VISIBLE));
const hidden = computed(() => Math.max(0, updates.actionable.length - VISIBLE));
const busy = (item: OutdatedItem) => {
  const { target } = updateRequest(item);
  return Boolean(tasks.forPackage(target.kind, target.token));
};

async function upgrade(items: readonly OutdatedItem[]) {
  try {
    // Routed targets (App Store by adamId, other sources by local app); the main window matches them to rows.
    const targets = items.map(item => updateRequest(item).target);
    await openMain('/updates');
    await emitTo('main', 'updates:requested', targets);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Show and navigate the main window, then hide the popover.
async function openMain(route: string) {
  const main = (await getAllWindows()).find(window => window.label === 'main');
  await main?.unminimize();
  await main?.show();
  await main?.setFocus();
  const payload: DeepLinkEvent = {
    url: `opennavo://${route.replace(/^\//, '')}`,
    route,
    action: null
  };
  await emitTo('main', 'deeplink:received', payload);
  await getCurrentWindow().hide();
}
</script>

<template>
  <div ref="content" class="max-h-520px w-330px max-w-full bg-transparent p-6px">
    <section
      class="tray-panel max-h-508px flex flex-col overflow-hidden rounded-panel text-ink-primary"
      :aria-label="t('tray.available', { count: updates.actionable.length }, { plural: updates.actionable.length })"
    >
      <header class="flex shrink-0 items-center gap-8px border-b border-b-solid border-line-subtle px-16px py-12px">
        <OnLogo :size="18" inline />
        <span class="text-14px font-600 text-brand-coral">OpenNavo</span>
        <span class="ml-auto text-12px text-ink-tertiary">
          {{
            updates.actionable.length
              ? t('tray.available', { count: updates.actionable.length }, { plural: updates.actionable.length })
              : t('tray.upToDate')
          }}
        </span>
      </header>

      <div
        v-if="running"
        class="flex shrink-0 flex-col gap-8px border-b border-b-solid border-line-subtle px-16px py-12px"
      >
        <div class="flex items-center gap-10px">
          <span
            v-if="running.clean"
            class="grid h-24px w-24px shrink-0 place-items-center rounded-[6px] bg-surface-raised text-ink-secondary"
            aria-hidden="true"
          >
            <OnIcon name="eraser" :size="14" />
          </span>
          <OnAppIcon
            v-else
            :kind="running.iconKind"
            :token="running.token"
            :name="running.name"
            :src="iconOf(running.kind, running.token)"
            :size="24"
          />
          <span class="min-w-0 flex-1 truncate text-13px">{{ running.text }}</span>
          <span v-if="running.percent !== null" class="text-12px text-ink-tertiary tabular-nums">{{
            formatPercent(running.percent / 100, { locale: appLocale })
          }}</span>
        </div>
        <OnProgressMeter :value="running.percent" size="sm" :label="running.text" />
        <span v-if="tasks.queued.length" class="text-11.5px text-ink-tertiary">
          {{ t('tray.queued', { count: tasks.queued.length }, { plural: tasks.queued.length }) }}
        </span>
      </div>

      <ul v-if="rows.length" class="m-0 min-h-0 flex list-none flex-col gap-2px overflow-y-auto p-0 px-8px py-8px">
        <li
          v-for="item in rows"
          :key="rowKey(item)"
          class="flex shrink-0 items-center gap-10px rounded-default px-8px py-8px"
        >
          <OnAppIcon
            :kind="item.kind"
            :token="item.token"
            :name="nameOf(item.kind, item.token, item.name)"
            :src="iconOf(item.kind, item.token)"
            :size="32"
          />
          <span class="min-w-0 flex-1">
            <span class="block truncate text-13.5px font-600">{{ nameOf(item.kind, item.token, item.name) }}</span>
            <span class="block truncate text-12px text-ink-tertiary">
              {{ formatVersionChange(item.installedVersion, item.currentVersion) }}
            </span>
          </span>
          <OnButton variant="secondary" size="sm" :disabled="busy(item)" @click="upgrade([item])">
            {{ t('tray.update') }}
          </OnButton>
        </li>
        <li v-if="hidden" class="shrink-0 px-8px py-4px">
          <button
            type="button"
            class="cursor-pointer border-0 bg-transparent p-0 text-12px text-ink-tertiary hover:text-ink-secondary"
            @click="openMain('/updates')"
          >
            {{ t('tray.more', { count: hidden }, { plural: hidden }) }}
          </button>
        </li>
      </ul>
      <div v-else class="flex flex-col items-center justify-center gap-4px px-16px py-32px text-center">
        <span class="text-14px font-600">{{ t('tray.upToDate') }}</span>
        <span class="text-12px text-ink-tertiary">{{ t('tray.upToDateHint') }}</span>
      </div>

      <div v-if="updates.actionable.length" class="shrink-0 px-16px pb-12px">
        <OnButton variant="primary" class="w-full" @click="upgrade(updates.actionable)">
          {{ t('tray.updateAll', { count: updates.actionable.length }, { plural: updates.actionable.length }) }}
        </OnButton>
      </div>

      <footer
        class="flex shrink-0 items-center justify-between border-t border-t-solid border-line-subtle px-16px py-10px text-12.5px"
      >
        <button
          type="button"
          class="cursor-pointer border-0 bg-transparent p-0 text-ink-secondary hover:text-ink-primary"
          @click="openMain('/discover')"
        >
          {{ t('tray.open') }}
        </button>
        <button
          type="button"
          class="cursor-pointer border-0 bg-transparent p-0 text-ink-secondary hover:text-ink-primary"
          @click="openMain('/settings')"
        >
          {{ t('tray.settings') }}
        </button>
      </footer>
    </section>
  </div>
</template>

<style>
/* Reset only the tray document so the dark native canvas cannot fill transparent corners and margins. */
html[data-opennavo-window='tray'] {
  background: transparent;
  color-scheme: normal;
}

html[data-opennavo-window='tray'] body {
  overflow: hidden;
  background: transparent;
}

/* Keep the content opaque for readability over other windows; transparency is only for outer rounded corners. */
.tray-panel {
  background-color: var(--on-surface-card);
}
</style>
