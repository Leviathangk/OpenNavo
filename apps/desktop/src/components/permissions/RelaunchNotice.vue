<script setup lang="ts">
import { ref } from 'vue';
import { useRoute } from 'vue-router';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { useI18n } from 'vue-i18n';
import { relaunch } from '@tauri-apps/plugin-process';
import { OnButton, OnIcon } from '@opennavo/ui';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';

// Enabled in System Settings but not this process (06 §12.3, §12.6): macOS applies both permissions only after relaunch.
const { t } = useI18n();
const toasts = useToasts();
const { errorText } = usePackageState();
const restarting = ref(false);
const route = useRoute();
const gate = usePermissionGateStore();

async function restart() {
  restarting.value = true;
  try {
    // Rust refuses while tasks run or wait (active_tasks) instead of interrupting them; it also refuses during another quit.
    gate.rememberForRelaunch(route.fullPath);
    await relaunch();
  } catch (error) {
    const reason = String(error);
    if (reason.includes('active_tasks')) toasts.push({ tone: 'warning', title: t('permission.relaunch.busy') });
    else if (!reason.includes('exit_in_progress'))
      toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  } finally {
    restarting.value = false;
  }
}
</script>

<template>
  <div class="flex items-center gap-10px rounded-default bg-status-info-subtle px-12px py-10px" role="status">
    <OnIcon name="info" :size="15" class="shrink-0 text-status-info" />
    <p class="m-0 min-w-0 flex-1 text-12.5px leading-[1.5] text-ink-secondary">
      {{ t('permission.relaunch.body') }}
    </p>
    <OnButton variant="secondary" size="xs" :loading="restarting" @click="restart">{{
      t('permission.relaunch.action')
    }}</OnButton>
  </div>
</template>
