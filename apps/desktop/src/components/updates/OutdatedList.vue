<script setup lang="ts">
import { useAppLocale as useFormattingLocale } from '@/composables/useAppLocale';
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatDate, formatVersionChange } from '@opennavo/shared';
import { OnAppRow, OnButton, OnChip, OnMenu } from '@opennavo/ui';
import type { OnMenuItem } from '@opennavo/ui';
import { RouterLink } from 'vue-router';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import SourceUpdateButton from './SourceUpdateButton.vue';
import UpdateNotesSheet from './UpdateNotesSheet.vue';
import type { OutdatedItem } from '@/ipc/bindings';
import { packageKey } from '@/ipc/client';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useLocalAppsStore, useUpdatesStore } from '@/stores';
import { rowHref, rowKey } from '@/stores/updates';
import { openExternal } from '@/utils/external';

const { appLocale: formattingLocale } = useFormattingLocale();

// Available updates (mockup 05) from every source (store/security plan §7, §11.4): version change, download size,
// release date; the second line is the release summary for Homebrew rows and what to expect for the others (admin
// password, running app, relaunch). Rows other sources deliver show where the update comes from, open their own
// notes, and can skip a version, follow the beta channel or be ignored.
const props = defineProps<{
  items: readonly OutdatedItem[];
  summaries: ReadonlyMap<string, { summary: string | null; publishedAt: string | null }>;
}>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const { icon } = usePackageIdentity();
const { errorText } = usePackageState();
const updates = useUpdatesStore();
const localApps = useLocalAppsStore();
const toasts = useToasts();

const notesFor = ref<OutdatedItem | null>(null);
const notesOpen = computed({
  get: () => notesFor.value !== null,
  set: open => {
    if (!open) notesFor.value = null;
  }
});

/** App Store rows by adamId; other sources by the local app, else the catalog entry they matched. */
function identity(item: OutdatedItem) {
  if (item.source === 'mas') return icon('mas', item.token, item.name);
  if (item.source !== 'homebrew' && (!item.token || localApps.find(item.appId ?? '')))
    return icon('app', item.appId ?? '', item.name);
  return icon(item.kind, item.token, item.name);
}

function meta(item: OutdatedItem): string {
  const info = item.source === 'homebrew' ? props.summaries.get(packageKey(item.kind, item.token)) : undefined;
  return [
    formatVersionChange(item.installedVersion, item.currentVersion),
    item.downloadSize ? formatBytes(item.downloadSize, { locale: formattingLocale.value }) : null,
    info?.publishedAt ? formatDate(info.publishedAt, { locale: appLocale.value }) : null,
    item.downloadHost ? t('updates.meta.from', { host: item.downloadHost }) : null,
    item.expectedTeamMatch ? t('updates.meta.teamMatch') : null
  ]
    .filter(Boolean)
    .join(' · ');
}

/** What to expect before updating: shown where Homebrew rows show their release summary. */
function hint(item: OutdatedItem): string | null {
  const parts: string[] = [];
  // The new version is already on disk; the running copy is still the old one.
  if (item.expectedTeamMatch === false) parts.push(t('updates.hint.teamMismatch'));
  else if (item.relaunchPending) parts.push(t('updates.hint.relaunch', { version: item.currentVersion }));
  else if (item.informationalUrl) parts.push(t('updates.hint.informational'));
  else {
    if (item.running) parts.push(t('updates.hint.running'));
    if (item.requiresAdmin) parts.push(t('updates.hint.admin'));
  }
  if (item.skippedVersion && item.skippedVersion !== item.currentVersion)
    parts.push(t('updates.hint.skipped', { version: item.skippedVersion }));
  return parts.join(' · ') || null;
}

const hasInlineNotes = (item: OutdatedItem) => Boolean(item.notes && 'body' in item.notes && item.notes.body);
const notesUrl = (item: OutdatedItem) => (item.notes && 'url' in item.notes ? (item.notes.url ?? null) : null);

// One menu for every source (Rust keeps skips and ignores per app, Homebrew included); channels only where a feed has one.
function menu(item: OutdatedItem): OnMenuItem[] {
  const items: OnMenuItem[] = [];
  if (item.skippedVersion)
    items.push({
      key: 'unskip',
      label: t('updates.actions.unskip', { version: item.skippedVersion })
    });
  if (!item.relaunchPending) items.push({ key: 'skip', label: t('updates.actions.skip') });
  if (item.channel)
    items.push(
      item.channel === 'beta'
        ? { key: 'stable', label: t('updates.actions.stable') }
        : { key: 'beta', label: t('updates.actions.beta') }
    );
  items.push(
    item.ignored
      ? { key: 'unignoreApp', label: t('updates.actions.unignoreApp') }
      : { key: 'ignoreApp', label: t('updates.actions.ignoreApp'), icon: 'x' }
  );
  return items;
}

async function run(action: () => Promise<unknown>, done?: string) {
  try {
    if ((await action()) === false) return;
    if (done) toasts.push({ tone: 'info', title: done });
  } catch (error) {
    toasts.push({
      tone: 'danger',
      title: t('errors.actionFailed'),
      description: errorText(error)
    });
  }
}

function select(item: OutdatedItem, key: string) {
  const name = item.name;
  switch (key) {
    case 'skip':
      return run(
        () => updates.skip(item, item.currentVersion),
        t('updates.toast.skipped', { name, version: item.currentVersion })
      );
    case 'unskip':
      return run(() => updates.skip(item, null), t('updates.toast.unskipped', { name }));
    case 'beta':
      return run(() => updates.setChannel(item, 'beta'), t('updates.toast.beta', { name }));
    case 'stable':
      return run(() => updates.setChannel(item, 'stable'), t('updates.toast.stable', { name }));
    case 'unignoreApp':
      return run(() => updates.ignoreApp(item, false), t('updates.toast.unignoredApp', { name }));
    case 'ignoreApp':
      return run(() => updates.ignoreApp(item), t('updates.toast.ignoredApp', { name }));
  }
}

const rows = computed(() =>
  props.items.map(item => ({
    item,
    key: rowKey(item),
    description:
      item.source === 'homebrew'
        ? (props.summaries.get(packageKey(item.kind, item.token))?.summary ?? null)
        : hint(item)
  }))
);
</script>

<template>
  <ul class="m-0 list-none rounded-big border border-solid border-line-subtle bg-surface-card p-0">
    <li
      v-for="{ item, key, description } in rows"
      :key="key"
      class="border-t border-t-solid border-line-subtle px-16px first:border-t-0"
    >
      <OnAppRow
        v-bind="identity(item)"
        :meta="meta(item)"
        :description="description"
        :href="rowHref(item)"
        :link-as="RouterLink"
      >
        <template #badges>
          <OnChip v-if="item.source !== 'homebrew'" tone="info">{{ t(`updates.source.${item.source}`) }}</OnChip>
          <OnChip v-if="item.expectedTeamMatch === false" tone="danger">{{ t('updates.badge.teamMismatch') }}</OnChip>
          <OnChip v-if="item.criticalUpdate" tone="danger">{{ t('updates.badge.critical') }}</OnChip>
          <OnChip v-if="item.channel === 'beta'" tone="warning">{{ t('updates.badge.beta') }}</OnChip>
          <OnChip v-if="item.major">{{ t('updates.badge.major') }}</OnChip>
          <OnChip v-if="item.source === 'homebrew' && item.autoUpdates" tone="info">{{
            t('package.autoUpdates')
          }}</OnChip>
          <OnChip v-if="item.pinned">{{ t('package.pinned') }}</OnChip>
        </template>
        <template #actions>
          <OnButton
            v-if="item.source === 'homebrew'"
            variant="ghost"
            size="sm"
            :href="`/package/${item.kind}/${item.token}/versions`"
            :link-as="RouterLink"
          >
            {{ t('updates.changelog') }}
          </OnButton>
          <OnButton v-else-if="hasInlineNotes(item)" variant="ghost" size="sm" @click="notesFor = item">
            {{ t('updates.changelog') }}
          </OnButton>
          <OnButton
            v-else-if="notesUrl(item) && !item.informationalUrl"
            variant="ghost"
            size="sm"
            icon="arrow-up-right"
            @click="openExternal(notesUrl(item)!)"
          >
            {{ t('updates.changelog') }}
          </OnButton>
          <template v-if="item.source === 'homebrew'">
            <PackageGetButton v-if="!item.pinned" :kind="item.kind" :token="item.token" />
            <span v-else class="text-12px text-ink-tertiary">{{ t('updates.pinned') }}</span>
          </template>
          <OnButton
            v-else-if="item.relaunchPending"
            variant="secondary"
            size="sm"
            @click="run(() => updates.relaunch(item))"
          >
            {{ t('updates.actions.relaunch') }}
          </OnButton>
          <OnButton
            v-else-if="item.informationalUrl"
            variant="secondary"
            size="sm"
            icon="arrow-up-right"
            @click="openExternal(item.informationalUrl)"
          >
            {{ t('updates.actions.view') }}
          </OnButton>
          <!-- Rust refuses an update whose expected developer is not the installed app's (E_UPDATE_TEAM_MISMATCH). -->
          <SourceUpdateButton v-else-if="item.expectedTeamMatch !== false" :item="item" />
          <OnMenu
            :items="menu(item)"
            :label="t('common.more')"
            variant="ghost"
            size="sm"
            @select="key => select(item, key)"
          />
        </template>
      </OnAppRow>
    </li>
  </ul>
  <UpdateNotesSheet v-model:open="notesOpen" :item="notesFor" />
</template>
