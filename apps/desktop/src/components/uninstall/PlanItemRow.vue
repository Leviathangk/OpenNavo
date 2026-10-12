<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatList } from '@opennavo/shared';
import { OnButton, OnCheckbox, OnChip, OnIcon } from '@opennavo/ui';
import type { PlanItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { splitPath } from '@/utils/paths';

// One planned path (proposal §07): why it was matched, whether it needs the admin password or is shared.
const props = defineProps<{ item: PlanItem; checked: boolean }>();
const emit = defineEmits<{ toggle: [value: boolean]; reveal: [] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();

const parts = computed(() => splitPath(props.item.path));
const size = computed(() => {
  if (props.item.kind === 'symlink') return t('uninstall.link');
  return props.item.sizeBytes === null ? '—' : formatBytes(props.item.sizeBytes, { locale: appLocale.value });
});
const shared = computed(() =>
  props.item.sharedWith.length
    ? t('uninstall.sharedWith', {
        names: formatList(props.item.sharedWith, { locale: appLocale.value })
      })
    : ''
);
</script>

<template>
  <div
    class="group flex min-h-36px items-center gap-10px border-t border-t-solid border-line-subtle py-6px pl-33px pr-12px hover:bg-component-item-active"
  >
    <OnCheckbox
      :model-value="checked"
      :locked="item.required"
      :aria-label="parts.base"
      :title="item.required ? t('uninstall.required') : undefined"
      @update:model-value="value => emit('toggle', value)"
    />
    <div class="min-w-0 flex-1">
      <div class="flex min-w-0 whitespace-nowrap font-mono text-11.5px" :title="item.path">
        <span class="min-w-0 shrink truncate text-ink-tertiary">{{ parts.dir }}</span>
        <span class="max-w-[74%] shrink-0 truncate" :class="checked ? 'text-ink-primary' : 'text-ink-tertiary'">{{
          parts.base
        }}</span>
      </div>
      <div v-if="shared || item.note || item.count > 1" class="mt-2px flex min-w-0 flex-wrap gap-x-8px text-11.5px">
        <span v-if="shared" class="text-status-warning">{{ shared }}</span>
        <span v-if="item.count > 1" class="text-ink-tertiary">{{
          t('uninstall.files', { count: item.count }, { plural: item.count })
        }}</span>
        <span v-if="item.note" class="truncate font-mono text-ink-tertiary">{{ item.note }}</span>
      </div>
    </div>
    <span class="flex shrink-0 items-center gap-4px">
      <OnChip
        :tone="item.confidence === 'low' ? 'warning' : 'neutral'"
        size="sm"
        :title="t(`uninstall.reasonHints.${item.reason}`)"
        >{{ t(`uninstall.reasons.${item.reason}`) }}</OnChip
      >
      <OnChip v-if="item.sharedWith.length" tone="warning" icon="users" size="sm">{{ t('uninstall.shared') }}</OnChip>
      <span
        v-if="item.needsAdmin"
        class="inline-flex text-ink-tertiary"
        role="img"
        :aria-label="t('uninstall.adminHint')"
        :title="t('uninstall.adminHint')"
      >
        <OnIcon name="lock" :size="13" />
      </span>
    </span>
    <OnButton
      class="opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
      variant="ghost"
      size="xs"
      icon="folder"
      icon-only
      :aria-label="t('uninstall.reveal')"
      @click="emit('reveal')"
    />
    <span class="w-62px shrink-0 text-right text-12px tabular-nums text-ink-secondary">{{ size }}</span>
  </div>
</template>
