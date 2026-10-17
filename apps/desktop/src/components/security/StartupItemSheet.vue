<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatDate } from '@opennavo/shared';
import { OnButton, OnChip, OnKeyValueList, OnSheet } from '@opennavo/ui';
import type { OnKeyValueItem } from '@opennavo/ui';
import type { StartupAction, StartupItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { signatureKey, TRUST_TONE } from './meta';

// Startup item details (plan §6.3): everything Rust knows about one item, and only the actions it allows.
const props = defineProps<{ item: StartupItem | null; busy: boolean }>();
const emit = defineEmits<{ close: []; act: [action: StartupAction | 'mark_reviewed'] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();

const rows = computed<OnKeyValueItem[]>(() => {
  const item = props.item;
  if (!item) return [];
  const list: OnKeyValueItem[] = [
    {
      key: 'category',
      label: t('security.startup.fields.category'),
      value: t(`security.startup.category.${item.category}`)
    },
    { key: 'scope', label: t('security.startup.fields.scope'), value: t(`security.startup.scope.${item.scope}`) },
    { key: 'path', label: t('security.startup.fields.path'), value: item.path, mono: true }
  ];
  if (item.program)
    list.push({
      key: 'program',
      label: t('security.startup.fields.program'),
      value: [item.program, ...item.args].join(' '),
      mono: true
    });
  if (item.owner) list.push({ key: 'owner', label: t('security.startup.fields.owner'), value: item.owner.name });
  list.push({
    key: 'signature',
    label: t('security.startup.fields.signature'),
    value: [item.signature.signer, t(signatureKey(item))].filter(Boolean).join(' · ')
  });
  if (item.signature.teamId)
    list.push({ key: 'team', label: t('security.startup.fields.team'), value: item.signature.teamId, mono: true });
  list.push({
    key: 'state',
    label: t('security.startup.fields.state'),
    value: t(`security.startup.state.${item.state}`)
  });
  if (item.triggers.length)
    list.push({
      key: 'triggers',
      label: t('security.startup.fields.triggers'),
      value: item.triggers.map(trigger => t(`security.startup.trigger.${trigger}`)).join(' · ')
    });
  list.push({
    key: 'seen',
    label: t('security.startup.fields.firstSeen'),
    value: formatDate(item.firstSeenAt, { locale: appLocale.value })
  });
  list.push({
    key: 'av',
    label: t('security.startup.fields.av'),
    value: item.av
      ? item.av.clean
        ? t('security.startup.avClean')
        : t('security.startup.avThreat', { name: item.av.signature ?? '' })
      : t('security.startup.avNotScanned')
  });
  return list;
});

const can = (action: StartupAction) => Boolean(props.item?.actions.includes(action));
</script>

<template>
  <OnSheet :open="Boolean(item)" :title="item?.name ?? ''" @close="emit('close')">
    <div v-if="item" class="flex flex-col gap-14px">
      <div class="flex flex-wrap gap-6px">
        <OnChip v-if="item.isNew" tone="warning" size="sm">{{ t('security.startup.new') }}</OnChip>
        <OnChip :tone="TRUST_TONE[item.trust]" size="sm">{{ t(`security.startup.trust.${item.trust}`) }}</OnChip>
        <OnChip v-if="item.risk === 'high'" tone="danger" size="sm">{{ t('security.startup.riskHigh') }}</OnChip>
      </div>
      <p v-if="item.requiresAdmin && (can('disable') || can('remove'))" class="m-0 text-12.5px text-ink-tertiary">
        {{ t('security.startup.adminHint') }}
      </p>
      <OnKeyValueList :items="rows" />
    </div>
    <template #footer>
      <div v-if="item" class="flex flex-wrap justify-end gap-8px">
        <OnButton v-if="item.isNew" variant="ghost" :disabled="busy" @click="emit('act', 'mark_reviewed')">
          {{ t('security.startup.keep') }}
        </OnButton>
        <OnButton v-if="can('reveal')" variant="secondary" :disabled="busy" @click="emit('act', 'reveal')">
          {{ t('security.findings.reveal') }}
        </OnButton>
        <OnButton v-if="can('scan')" variant="secondary" :disabled="busy" @click="emit('act', 'scan')">
          {{ t('security.startup.scan') }}
        </OnButton>
        <OnButton
          v-if="can('open_settings')"
          variant="secondary"
          :disabled="busy"
          @click="emit('act', 'open_settings')"
        >
          {{ t('security.startup.openSettings') }}
        </OnButton>
        <OnButton v-if="can('enable')" variant="secondary" :disabled="busy" @click="emit('act', 'enable')">
          {{ t('security.startup.enable') }}
        </OnButton>
        <OnButton v-if="can('disable')" variant="secondary" :disabled="busy" @click="emit('act', 'disable')">
          {{ t('security.startup.disable') }}
        </OnButton>
        <OnButton v-if="can('restore')" variant="secondary" :disabled="busy" @click="emit('act', 'restore')">
          {{ t('security.startup.restore') }}
        </OnButton>
        <OnButton v-if="can('remove')" variant="danger" :disabled="busy" @click="emit('act', 'remove')">
          {{ t('security.startup.remove') }}
        </OnButton>
      </div>
    </template>
  </OnSheet>
</template>
