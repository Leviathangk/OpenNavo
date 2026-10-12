<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { formatBytes, formatRelativeTime } from '@opennavo/shared';
import { OnButton, OnCheckbox, OnChip, OnIcon } from '@opennavo/ui';
import type { CleanupItem } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { displayPath, splitPath } from '@/utils/paths';

// One candidate (cleanup proposal §03 ④): path or fixed command, why it is listed, size and age.
const props = defineProps<{ item: CleanupItem; checked: boolean }>();
const emit = defineEmits<{ toggle: [value: boolean]; reveal: []; protect: [] }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();

const command = computed(() => props.item.kind === 'command');
/** Docker's prunes read as what they remove; the fixed command is shown underneath. */
const DOCKER: readonly CleanupItem['reason'][] = ['docker_images', 'docker_build_cache', 'docker_containers'];
const named = computed(() =>
  DOCKER.includes(props.item.reason)
    ? { text: t(`cleanup.reasons.${props.item.reason}`), hint: t(`cleanup.reasons.${props.item.reason}Hint`) }
    : null
);
const parts = computed(() => splitPath(displayPath(props.item.path)));
const label = computed(() =>
  named.value ? named.value.text : command.value ? (props.item.command ?? '') : parts.value.base
);
const size = computed(() =>
  props.item.sizeBytes === null ? '—' : formatBytes(props.item.sizeBytes, { locale: appLocale.value })
);
const age = computed(() =>
  command.value || props.item.modifiedAt === null
    ? ''
    : formatRelativeTime(props.item.modifiedAt, { locale: appLocale.value })
);
/** Only reasons that tell the user something beyond the category name get a chip. */
const chip = computed(() => {
  switch (props.item.reason) {
    case 'orphan_service':
      return { tone: 'warning' as const, text: t('cleanup.reasons.orphan_service') };
    case 'installer_installed':
      return { tone: 'success' as const, text: t('cleanup.reasons.installer_installed') };
    case 'tool_command':
    case 'brew_cleanup':
      return { tone: 'neutral' as const, text: t('cleanup.item.command') };
    // Rebuildable, but the next build downloads it again: unchecked by default, and the chip says why.
    case 'tool_download_cache':
      return {
        tone: 'neutral' as const,
        text: t('cleanup.reasons.tool_download_cache'),
        hint: t('cleanup.reasons.tool_download_cacheHint')
      };
    // The newest device support version per device is kept by default; Xcode would rebuild it on the next connection.
    case 'device_support_latest':
      return {
        tone: 'info' as const,
        text: t('cleanup.reasons.device_support_latest'),
        hint: t('cleanup.reasons.device_support_latestHint')
      };
    default:
      return null;
  }
});
</script>

<template>
  <div
    class="group flex min-h-36px items-center gap-10px border-t border-t-solid border-line-subtle py-6px pl-76px pr-12px hover:bg-component-item-active"
  >
    <OnCheckbox :model-value="checked" :aria-label="label" @update:model-value="value => emit('toggle', value)" />
    <div class="min-w-0 flex-1">
      <template v-if="named">
        <div
          class="truncate text-12.5px"
          :class="checked ? 'text-ink-primary' : 'text-ink-secondary'"
          :title="named.hint"
        >
          {{ named.text }}
        </div>
        <div class="mt-2px truncate font-mono text-11.5px text-ink-tertiary">{{ item.command }}</div>
      </template>
      <div
        v-else
        class="flex min-w-0 whitespace-nowrap font-mono text-11.5px"
        :title="command ? (item.command ?? undefined) : item.path"
      >
        <template v-if="command">
          <span class="min-w-0 truncate" :class="checked ? 'text-ink-primary' : 'text-ink-tertiary'">{{ label }}</span>
        </template>
        <template v-else>
          <span class="min-w-0 shrink truncate text-ink-tertiary">{{ parts.dir }}</span>
          <span class="max-w-[60%] shrink-0 truncate" :class="checked ? 'text-ink-primary' : 'text-ink-tertiary'">{{
            parts.base
          }}</span>
        </template>
      </div>
      <div v-if="item.note" class="mt-2px truncate font-mono text-11.5px text-ink-tertiary">
        {{ item.note }}
      </div>
      <!-- Tool caches keep the few files a running process has open (a `go run` binary) and clean the rest. -->
      <div v-if="item.keptOpenFiles" class="mt-2px truncate text-11.5px text-ink-tertiary">
        {{ t('cleanup.item.keptOpen', { count: item.keptOpenFiles }, { plural: item.keptOpenFiles }) }}
      </div>
    </div>
    <span class="flex shrink-0 items-center gap-4px">
      <OnChip v-if="chip" :tone="chip.tone" size="sm" :title="'hint' in chip ? chip.hint : undefined">{{
        chip.text
      }}</OnChip>
      <span
        v-if="item.needsAdmin"
        class="inline-flex text-ink-tertiary"
        role="img"
        :aria-label="t('cleanup.item.admin')"
        :title="t('cleanup.item.admin')"
      >
        <OnIcon name="lock" :size="13" />
      </span>
    </span>
    <span class="w-72px shrink-0 truncate text-right text-11.5px text-ink-tertiary">{{ age }}</span>
    <span class="w-62px shrink-0 text-right text-12px tabular-nums text-ink-secondary">{{ size }}</span>
    <span class="flex w-50px shrink-0 justify-end gap-2px opacity-0 group-hover:opacity-100 focus-within:opacity-100">
      <OnButton
        v-if="!command"
        variant="ghost"
        size="xs"
        icon="folder"
        icon-only
        :aria-label="t('cleanup.item.reveal')"
        :title="t('cleanup.item.reveal')"
        @click="emit('reveal')"
      />
      <OnButton
        variant="ghost"
        size="xs"
        icon="shield"
        icon-only
        :aria-label="t('cleanup.item.protect')"
        :title="t('cleanup.item.protect')"
        @click="emit('protect')"
      />
    </span>
  </div>
</template>
