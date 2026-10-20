<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, shownChip } from '@opennavo/shared';
import { OnButton, OnChip } from '@opennavo/ui';
import type { ReleaseAsset } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';

// One release file of a link app: its name, size and chip, and the button that opens it at the developer's address.
const props = defineProps<{ asset: ReleaseAsset; recommended: boolean; primary: boolean }>();
const emit = defineEmits<{ open: [] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();
const chip = computed(() => shownChip(props.asset));
</script>

<template>
  <li class="flex items-center gap-8px border-t border-t-solid border-line-subtle py-8px first:border-t-0">
    <div class="min-w-0 flex-1">
      <p class="m-0 truncate font-mono text-12px text-ink-primary" :title="asset.name">
        {{ asset.name }}
      </p>
      <p class="m-0 flex flex-wrap items-center gap-6px text-11.5px text-ink-tertiary">
        <span>{{ formatBytes(asset.size, { locale: appLocale }) }}</span>
        <span v-if="chip">· {{ t(`package.link.arch.${chip}`) }}</span>
        <OnChip v-if="recommended" tone="success" size="sm">{{ t('package.link.recommended') }}</OnChip>
      </p>
    </div>
    <OnButton :variant="primary ? 'primary' : 'secondary'" size="xs" icon="arrow-up-right" @click="emit('open')">
      {{ t('package.link.download') }}
    </OnButton>
  </li>
</template>
