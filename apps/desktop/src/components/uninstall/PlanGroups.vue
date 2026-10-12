<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes } from '@opennavo/shared';
import { OnCheckbox, OnIcon } from '@opennavo/ui';
import type { AppPlan, PlanItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { planGroups, useUninstallStore } from '@/stores/uninstall';
import PlanItemRow from './PlanItemRow.vue';
import type { GroupKey, PlanGroup } from '@/stores/uninstall';

// Plan of one app grouped by category (proposal §03): small groups start collapsed; shared, required,
// low-confidence and large groups stay open so the user sees what matters.
const props = defineProps<{ app: AppPlan }>();
const emit = defineEmits<{ reveal: [item: PlanItem] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const store = useUninstallStore();

const groups = computed(() => planGroups(props.app));
const sizeOf = (items: readonly PlanItem[]) => items.reduce((sum, item) => sum + (item.sizeBytes ?? 0), 0);
const OPEN_FROM = 100_000_000;

function startsOpen(group: PlanGroup) {
  if (group.key === 'app' || group.key === 'maybe') return true;
  return sizeOf(group.items) >= OPEN_FROM || group.items.some(item => item.required || item.sharedWith.length > 0);
}
const collapsed = ref(new Set<GroupKey>(groups.value.filter(group => !startsOpen(group)).map(group => group.key)));

function toggleOpen(key: GroupKey) {
  const next = new Set(collapsed.value);
  if (next.has(key)) next.delete(key);
  else next.add(key);
  collapsed.value = next;
}

function state(group: PlanGroup) {
  const optional = group.items.filter(item => !item.required);
  const checked = optional.filter(item => store.isChecked(item)).length;
  return {
    locked: optional.length === 0,
    all: optional.length > 0 && checked === optional.length,
    some: checked > 0 && checked < optional.length
  };
}

function size(group: PlanGroup) {
  const format = (value: number) => formatBytes(value, { locale: appLocale.value });
  const total = sizeOf(group.items);
  const chosen = sizeOf(group.items.filter(item => store.isChecked(item)));
  if (chosen === total) return format(total);
  if (chosen === 0) return t('uninstall.unselected', { size: format(total) });
  return `${format(chosen)} / ${format(total)}`;
}
</script>

<template>
  <div class="overflow-hidden rounded-panel border border-solid border-line-subtle bg-surface-card">
    <section
      v-for="(group, index) in groups"
      :key="group.key"
      :class="index > 0 ? 'border-t border-t-solid border-line-subtle' : ''"
    >
      <div class="flex h-40px items-center gap-9px pl-8px pr-12px">
        <button
          type="button"
          class="m-0 flex h-24px w-20px shrink-0 cursor-pointer items-center justify-center rounded-small border-none bg-transparent p-0 text-ink-tertiary outline-none focus-visible:shadow-focus-ring"
          :aria-expanded="!collapsed.has(group.key)"
          :aria-label="t(`uninstall.groups.${group.key}`)"
          @click="toggleOpen(group.key)"
        >
          <OnIcon
            name="chevron-down"
            :size="14"
            class="transition-transform duration-fast"
            :class="collapsed.has(group.key) ? '-rotate-90' : ''"
          />
        </button>
        <OnCheckbox
          :model-value="state(group).all"
          :indeterminate="state(group).some"
          :locked="state(group).locked"
          :title="state(group).locked ? t('uninstall.required') : undefined"
          @update:model-value="value => store.setItems(group.items, value)"
        >
          <span class="text-13px font-600 text-ink-primary">{{ t(`uninstall.groups.${group.key}`) }}</span>
        </OnCheckbox>
        <span class="shrink-0 text-12px text-ink-tertiary">{{
          t('uninstall.itemCount', { count: group.items.length }, { plural: group.items.length })
        }}</span>
        <span class="min-w-0 flex-1 truncate text-11.5px text-ink-disabled">{{
          group.key === 'app' ? '' : t(`uninstall.groupHints.${group.key}`)
        }}</span>
        <span class="shrink-0 text-12.5px tabular-nums text-ink-secondary">{{ size(group) }}</span>
      </div>
      <div v-if="!collapsed.has(group.key)">
        <PlanItemRow
          v-for="item in group.items"
          :key="item.id"
          :item="item"
          :checked="store.isChecked(item)"
          @toggle="value => store.setItem(item.id, value)"
          @reveal="emit('reveal', item)"
        />
      </div>
    </section>
  </div>
</template>
