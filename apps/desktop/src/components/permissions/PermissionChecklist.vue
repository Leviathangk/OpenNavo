<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnChip, OnIcon } from '@opennavo/ui';
import type { GuidePane } from '@/stores/permissions';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { usePermissionsStore } from '@/stores';
import RelaunchNotice from './RelaunchNotice.vue';

// Welcome permissions (06 §12.3, §12.6; owner decision 2026-10-12): App Management and Full Disk Access are both required.
// Enable opens System Settings and the draggable OpenNavo icon overlay; state updates live.
// Installed shows only the Full Disk Access row, with its own explanation.
const props = withDefaults(
  defineProps<{
    panes?: readonly GuidePane[];
    required?: readonly GuidePane[];
    /** Replaces a row's explanation. */
    bodies?: Partial<Record<GuidePane, string>>;
  }>(),
  {
    panes: () => ['app_management', 'full_disk_access'],
    required: () => ['app_management', 'full_disk_access'],
    bodies: () => ({})
  }
);
const { t } = useI18n();
const permissions = usePermissionsStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const rows = computed(() =>
  (
    [
      { pane: 'app_management', icon: 'layout-grid', key: 'appManagement' },
      { pane: 'full_disk_access', icon: 'hard-drive', key: 'fullDiskAccess' }
    ] as const
  )
    .filter(row => props.panes.includes(row.pane))
    .map(row => ({
      ...row,
      required: props.required.includes(row.pane),
      body: props.bodies[row.pane] ?? t(`welcome.permissions.${row.key}.body`),
      state: row.pane === 'app_management' ? permissions.appManagement : permissions.fullDiskAccess,
      // On in System Settings but awaiting a reopen: Enable would only reopen the same switch; the notice below reopens OpenNavo.
      pending: permissions.pending(row.pane),
      waiting: permissions.guiding === row.pane
    }))
);

async function enable(pane: GuidePane) {
  try {
    await permissions.guide(pane);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}
</script>

<template>
  <div class="flex flex-col gap-10px">
    <ul class="m-0 list-none overflow-hidden rounded-big border border-solid border-line-subtle p-0">
      <li
        v-for="row in rows"
        :key="row.pane"
        class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-14px py-12px first:border-t-0"
      >
        <span
          class="grid h-34px w-34px shrink-0 place-items-center rounded-default"
          :class="
            row.pending
              ? 'bg-status-warning-subtle text-status-warning'
              : row.state === 'granted'
                ? 'bg-status-success-subtle text-status-success'
                : 'bg-surface-chip text-ink-secondary'
          "
        >
          <OnIcon :name="row.pending ? 'refresh-cw' : row.state === 'granted' ? 'check' : row.icon" :size="16" />
        </span>
        <span class="min-w-0 flex-1">
          <!-- Wrap long translations (e.g. Russian) without truncating headings. -->
          <span class="flex flex-wrap items-center gap-x-8px gap-y-2px">
            <span class="text-14px font-600 leading-[1.4] text-ink-primary">{{
              t(`welcome.permissions.${row.key}.title`)
            }}</span>
            <OnChip :tone="row.required ? 'coral' : 'neutral'" outline>{{
              row.required ? t('welcome.permissions.required') : t('welcome.permissions.optional')
            }}</OnChip>
          </span>
          <span class="mt-2px block text-12.5px leading-[1.5] text-ink-tertiary">{{ row.body }}</span>
        </span>
        <span v-if="row.pending" class="shrink-0 text-13px text-status-warning">
          {{ t('permission.relaunch.pending') }}
        </span>
        <span v-else-if="row.state === 'granted'" class="shrink-0 text-13px text-status-success">
          {{ t('welcome.permissions.granted') }}
        </span>
        <!-- Show waiting during guidance; restore the button on returning to OpenNavo so guidance can reopen. -->
        <span
          v-else-if="row.waiting"
          class="flex shrink-0 items-center gap-6px text-13px text-ink-secondary"
          role="status"
        >
          <span
            class="block h-12px w-12px animate-spin rounded-full border-2 border-solid border-line-default border-t-ink-secondary motion-reduce:animate-none"
            aria-hidden="true"
          ></span>
          {{ t('welcome.permissions.waiting') }}
        </span>
        <OnButton
          v-else
          :variant="row.required ? 'primary' : 'secondary'"
          size="sm"
          class="shrink-0"
          :aria-label="t(`welcome.permissions.${row.key}.enable`)"
          @click="enable(row.pane)"
          >{{ t('welcome.permissions.enable') }}</OnButton
        >
      </li>
    </ul>
    <RelaunchNotice v-if="permissions.relaunchRequired" />
  </div>
</template>
