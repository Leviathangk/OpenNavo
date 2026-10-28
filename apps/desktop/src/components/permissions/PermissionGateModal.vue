<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnAppIcon, OnButton, OnModal } from '@opennavo/ui';
import PermissionChecklist from './PermissionChecklist.vue';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
const gate = usePermissionGateStore();
const { t } = useI18n();
const { displayName, icon } = usePackageIdentity();
const open = computed({
  get: () => Boolean(gate.current),
  set: value => {
    if (!value) gate.dismiss();
  }
});
const description = computed(() =>
  gate.current?.targets?.length
    ? t('permissionGate.bodyApps')
    : gate.current?.feature === 'install'
      ? t('permissionGate.bodyInstall')
      : t('permissionGate.body', { feature: t(`nav.${gate.current?.feature ?? 'installed'}`) })
);
</script>

<template>
  <OnModal v-model:open="open" size="sm" :title="t('permissionGate.title')" :description="description">
    <ul v-if="gate.current?.targets?.length" class="m-0 mb-16px flex list-none flex-col gap-8px p-0">
      <li
        v-for="target in gate.current.targets"
        :key="`${target.kind}:${target.token}`"
        class="flex items-center gap-10px"
      >
        <OnAppIcon v-bind="icon(target.kind, target.token)" :size="28" />
        <span class="min-w-0 flex-1 truncate text-13px text-ink-primary">{{
          displayName(target.kind, target.token)
        }}</span>
      </li>
    </ul>
    <PermissionChecklist />
    <template #footer>
      <OnButton variant="ghost" @click="gate.dismiss">{{ t('permissionGate.browse') }}</OnButton>
      <OnButton variant="primary" :disabled="!gate.ready" @click="gate.proceed">{{
        gate.current?.targets?.length || gate.current?.feature === 'install'
          ? t('permissionGate.continue')
          : t('welcome.done')
      }}</OnButton>
    </template>
  </OnModal>
</template>
