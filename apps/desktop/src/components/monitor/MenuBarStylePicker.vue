<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnMenu } from '@opennavo/ui';
import type { MenuBarStyle } from '@/ipc/bindings';
import type { DisplayModule } from '@/stores/monitor';
import { shownStyle, useMenuBarStyles } from './meta';

// Style of one menu bar item (§12): two lines and value only for all; mini line and bars where the value moves (M3).
const props = defineProps<{ module: DisplayModule; modelValue: MenuBarStyle }>();
const emit = defineEmits<{ 'update:modelValue': [style: MenuBarStyle] }>();

const { t } = useI18n();
const styles = useMenuBarStyles();
const items = computed(() => styles(props.module));
const current = computed(() => items.value.find(item => item.key === shownStyle(props.module, props.modelValue)));

function select(key: string) {
  const item = items.value.find(entry => entry.key === key);
  if (item && item.key !== props.modelValue) emit('update:modelValue', item.key);
}
</script>

<template>
  <OnMenu :label="t('monitor.menuBar.style')" :items="items" @select="select">
    <template #trigger="{ attrs }">
      <OnButton v-bind="attrs" size="sm" icon-right="chevron-down" data-testid="menubar-style">{{
        current?.label
      }}</OnButton>
    </template>
  </OnMenu>
</template>
