<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnButton, OnIcon, OnModal } from '@opennavo/ui';
import { useAppLocale } from '@/composables/useAppLocale';
import { useCleanupStore } from '@/stores/cleanup';

// Confirm (cleanup proposal §06 step 4): how much is deleted for good, how much goes to the Trash, and admin prompts.
const open = defineModel<boolean>('open', { required: true });
const emit = defineEmits<{ confirm: [] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useCleanupStore();

const format = (bytes: number) => formatBytes(bytes, { locale: appLocale.value });
const totals = computed(() => store.totals);
const lines = computed(() => {
  const list: { key: string; icon: string; tone: string; text: string }[] = [];
  if (totals.value.nowBytes)
    list.push({
      key: 'delete',
      icon: 'zap',
      tone: 'bg-surface-card-alt',
      text: t('cleanup.confirm.delete', { size: format(totals.value.nowBytes) })
    });
  if (totals.value.trashBytes)
    list.push({
      key: 'trash',
      icon: 'undo-2',
      tone: 'bg-surface-card-alt',
      text: t('cleanup.confirm.trash', { size: format(totals.value.trashBytes) })
    });
  if (totals.value.emptiesTrash)
    list.push({
      key: 'bin',
      icon: 'triangle-alert',
      tone: 'bg-status-danger-subtle',
      text: t('cleanup.confirm.emptyTrash')
    });
  if (totals.value.admin)
    list.push({
      key: 'admin',
      icon: 'key-round',
      tone: 'bg-status-warning-subtle',
      text: t('cleanup.confirm.admin', { count: totals.value.admin }, { plural: totals.value.admin })
    });
  return list;
});
</script>

<template>
  <OnModal
    v-model:open="open"
    size="sm"
    :title="t('cleanup.confirm.title', { size: format(totals.bytes) })"
    :description="t('cleanup.confirm.count', { count: totals.count }, { plural: totals.count })"
  >
    <div class="flex flex-col gap-8px">
      <p
        v-for="line in lines"
        :key="line.key"
        class="m-0 flex items-start gap-10px rounded-default px-12px py-10px text-12.5px leading-[1.55] text-ink-secondary"
        :class="line.tone"
      >
        <OnIcon :name="line.icon" :size="15" class="mt-2px shrink-0 text-ink-tertiary" />
        <span>{{ line.text }}</span>
      </p>
    </div>
    <template #footer>
      <OnButton variant="secondary" @click="open = false">{{ t('cleanup.confirm.cancel') }}</OnButton>
      <OnButton variant="danger" icon="trash" :loading="store.stage === 'submitting'" @click="emit('confirm')">{{
        t('cleanup.confirm.clean')
      }}</OnButton>
    </template>
  </OnModal>
</template>
