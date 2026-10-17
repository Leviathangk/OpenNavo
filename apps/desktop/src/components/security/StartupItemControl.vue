<script setup lang="ts">
import { useI18n } from 'vue-i18n';
import { OnButton, OnToggle } from '@opennavo/ui';
import type { StartupAction, StartupItem } from '@/ipc/bindings';

// The one thing a startup item row can do in place: switch it, restore it, or open the System Settings pane that owns
// it. Everything else (remove, scan, reveal) is in the details sheet.
// `label` names the switch: the app, on a row that stands for the app.
const props = defineProps<{ item: StartupItem; busy: boolean; label?: string }>();
const emit = defineEmits<{ act: [action: StartupAction] }>();

const { t } = useI18n();
const can = (action: StartupAction) => props.item.actions.includes(action);
</script>

<template>
  <OnToggle
    v-if="can('disable') || can('enable')"
    :model-value="item.state !== 'disabled'"
    :disabled="busy"
    :aria-label="t('security.startup.toggle', { name: label ?? item.name })"
    @update:model-value="(on: boolean) => emit('act', on ? 'enable' : 'disable')"
  />
  <OnButton v-else-if="can('restore')" variant="secondary" size="xs" :disabled="busy" @click="emit('act', 'restore')">
    {{ t('security.startup.restore') }}
  </OnButton>
  <OnButton v-else-if="can('open_settings')" variant="ghost" size="xs" @click="emit('act', 'open_settings')">
    {{ t('security.startup.openSettings') }}
  </OnButton>
</template>
