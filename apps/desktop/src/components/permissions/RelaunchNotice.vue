<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { OnButton, OnIcon } from '@opennavo/ui';
import { useRelaunch } from '@/composables/useRelaunch';
import { useAppUpdaterStore } from '@/stores/appUpdater';
import { usePermissionsStore } from '@/stores/permissions';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { commands, unwrap } from '@/ipc/client';

const { t } = useI18n();
const route = useRoute();
const updater = useAppUpdaterStore();
const { restarting, restart } = useRelaunch(() => route.fullPath);
const permissions = usePermissionsStore();
const { errorText } = usePackageState();
const toasts = useToasts();
const opening = ref(false);
const body = computed(() =>
  permissions.relaunchIneffective
    ? 'permission.relaunch.ineffective'
    : updater.state === 'installed'
      ? 'permission.relaunch.updated'
      : 'permission.relaunch.body'
);

async function act() {
  if (!permissions.relaunchIneffective) return restart();
  if (opening.value) return;
  opening.value = true;
  try {
    await unwrap(commands.permissionSettingsOpen('full_disk_access'));
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    opening.value = false;
  }
}
</script>

<template>
  <div class="flex items-center gap-10px rounded-default bg-status-info-subtle px-12px py-10px" role="status">
    <OnIcon name="info" :size="15" class="shrink-0 text-status-info" />
    <p class="m-0 min-w-0 flex-1 text-12.5px leading-[1.5] text-ink-secondary">
      {{ t(body) }}
    </p>
    <OnButton variant="secondary" size="xs" :loading="restarting || opening" @click="act">{{
      t(permissions.relaunchIneffective ? 'permission.relaunch.openSettings' : 'permission.relaunch.action')
    }}</OnButton>
  </div>
</template>
