<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnIcon } from '@opennavo/ui';
import { usePermissionsStore } from '@/stores';
import PermissionChecklist from './PermissionChecklist.vue';

// Installed and uninstalling start with Full Disk Access (user decision 2026-10-12), guided like Welcome: Enable opens
// System Settings with the draggable OpenNavo overlay and the row updates live. Without it other apps' containers can be
// neither measured nor found, and Undo cannot read the Trash back. `compact` fits the uninstall dialog.
withDefaults(defineProps<{ compact?: boolean }>(), { compact: false });

const { t } = useI18n();
const permissions = usePermissionsStore();

const points = computed(() => [
  { key: 'footprint', icon: 'hard-drive', text: t('fullDiskGate.points.footprint') },
  { key: 'leftovers', icon: 'scan-search', text: t('fullDiskGate.points.leftovers') },
  { key: 'undo', icon: 'undo-2', text: t('fullDiskGate.points.undo') }
]);
</script>

<template>
  <section
    class="flex w-full flex-col"
    :class="compact ? 'gap-12px' : 'mx-auto max-w-560px gap-16px pt-8px'"
    aria-labelledby="full-disk-gate-title"
    data-testid="full-disk-gate"
  >
    <header class="flex items-start gap-14px">
      <span
        class="grid shrink-0 place-items-center rounded-big bg-status-info-subtle text-status-info"
        :class="compact ? 'h-36px w-36px' : 'h-44px w-44px'"
        aria-hidden="true"
      >
        <OnIcon name="hard-drive" :size="compact ? 18 : 22" />
      </span>
      <div class="min-w-0">
        <h2
          id="full-disk-gate-title"
          class="m-0 font-600 leading-[1.35] text-ink-primary"
          :class="compact ? 'text-14px' : 'text-17px'"
        >
          {{ t('fullDiskGate.title') }}
        </h2>
        <p class="m-0 mt-4px text-13px leading-[1.55] text-ink-secondary">{{ t('fullDiskGate.body') }}</p>
      </div>
    </header>
    <ul class="m-0 flex list-none flex-col gap-8px p-0">
      <li
        v-for="point in points"
        :key="point.key"
        class="flex items-start gap-9px text-13px leading-[1.5] text-ink-secondary"
      >
        <OnIcon :name="point.icon" :size="14" class="mt-3px shrink-0 text-ink-tertiary" />
        <span class="min-w-0">{{ point.text }}</span>
      </li>
    </ul>
    <PermissionChecklist
      :panes="['full_disk_access']"
      :required="['full_disk_access']"
      :bodies="{ full_disk_access: t('fullDiskGate.row') }"
    />
    <!-- macOS could not report the state: let the user continue once it is on, as Welcome does. -->
    <p
      v-if="permissions.fullDiskAccess === 'unknown'"
      class="m-0 flex flex-wrap items-center gap-6px text-12.5px text-ink-tertiary"
    >
      {{ t('fullDiskGate.unknown') }}
      <button
        type="button"
        class="m-0 cursor-pointer border-none bg-transparent p-0 font-sans text-12.5px text-ink-secondary underline underline-offset-2 hover:text-ink-primary"
        @click="permissions.fullDiskConfirmed = true"
      >
        {{ t('welcome.permissions.confirm') }}
      </button>
    </p>
    <p class="m-0 flex items-center gap-6px text-12px text-ink-disabled">
      <OnIcon name="lock" :size="12" class="shrink-0" />{{ t('fullDiskGate.privacy') }}
    </p>
  </section>
</template>
