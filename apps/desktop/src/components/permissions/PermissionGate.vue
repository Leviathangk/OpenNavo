<script setup lang="ts">
import { onMounted, watch } from 'vue';
import { usePermissionsStore } from '@/stores/permissions';
import { useI18n } from 'vue-i18n';
import { OnButton, OnIcon } from '@opennavo/ui';
import type { GateFeature } from '@/stores/permissionGate';
import { usePermissionGateStore } from '@/stores/permissionGate';
// page: banner and the guide once per run on first entry; section (Settings): banner only, the guide opens on a change;
// rail: neither.
const props = withDefaults(defineProps<{ feature: GateFeature; variant?: 'page' | 'section' | 'rail' }>(), {
  variant: 'page'
});
const gate = usePermissionGateStore();
const { t } = useI18n();
const permissions = usePermissionsStore();
watch(
  () => [permissions.status, props.feature],
  () => {
    if (props.variant === 'page' && permissions.status) gate.show(props.feature);
  },
  { immediate: true }
);
// Startup normally reads permissions already; a page opened before that still learns them.
onMounted(() => {
  if (!permissions.status) void permissions.refresh().catch(() => undefined);
});
// Fields that change their value straight from the keyboard (arrows, digits), unlike text fields that only filter.
const VALUE_FIELDS =
  'select,input[type="time"],input[type="date"],input[type="datetime-local"],input[type="month"],input[type="week"],input[type="number"],input[type="range"],input[type="color"]';
// Custom controls also change values or open menus on arrows, Home/End, or typeahead.
// Only focus traversal, dismissal, and modifier keys are always safe.
const MOVE_KEYS = new Set(['Tab', 'Escape', 'Shift', 'Control', 'Alt', 'Meta']);
function keydown(event: KeyboardEvent) {
  if (!MOVE_KEYS.has(event.key)) intercept(event);
}
// Backstop for a value field changed some other way: its handler must not save it (change cannot be cancelled).
function valueChange(event: Event) {
  if ((event.target as Element).closest(VALUE_FIELDS)) intercept(event);
}
function pointerdown(event: PointerEvent) {
  if ((event.target as Element).closest('[role=slider],input[type=range]')) intercept(event);
}
// A pending or failed permission query is locked too; blocked events are never replayed.
function intercept(event: Event) {
  if (gate.ready) return;
  const target = event.target as HTMLElement;
  if (target.closest('[data-permission-free],[role="tab"],a[href],input[type="search"],input[type="text"],textarea'))
    return;
  if (
    event.type === 'submit' ||
    target.closest(
      'button,[role="button"],[role="switch"],[role="checkbox"],[role="radio"],[role="radiogroup"],[role^="menuitem"],[role="option"],[role="slider"],[role="combobox"],[role="listbox"],input,select,textarea,summary,label,[data-permission-action]'
    )
  ) {
    event.preventDefault();
    event.stopPropagation();
    if (permissions.status) gate.show(props.feature, true);
    else void gate.require({ feature: props.feature });
  }
}
</script>

<template>
  <div
    class="contents"
    @click.capture="intercept"
    @contextmenu.capture="intercept"
    @submit.capture="intercept"
    @dragstart.capture="intercept"
    @keydown.capture="keydown"
    @change.capture="valueChange"
    @input.capture="valueChange"
    @pointerdown.capture="pointerdown"
  >
    <slot name="header" />
    <div
      v-if="variant !== 'rail' && permissions.status && !gate.ready"
      class="mb-12px flex items-center gap-8px rounded-default bg-status-warning-subtle px-12px py-10px"
      role="status"
    >
      <OnIcon name="lock" :size="15" />
      <span class="flex-1">{{ t('permissionGate.locked', { feature: t(`nav.${feature}`) }) }}</span>
      <OnButton size="xs" variant="secondary" @click="gate.show(feature, true)">{{
        t('permissionGate.unlock')
      }}</OnButton>
    </div>
    <slot />
  </div>
</template>
