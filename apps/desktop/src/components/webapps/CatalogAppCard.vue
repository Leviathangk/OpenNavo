<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnChip, OnIcon } from '@opennavo/ui';
import type { WebAppCatalogEntry } from './catalog';
import { webappIcon } from './icons';

const props = defineProps<{
  entry: WebAppCatalogEntry;
  installed: boolean;
  adding: boolean;
  disabled: boolean;
}>();
const emit = defineEmits<{ add: [] }>();
const { t } = useI18n();

const initial = computed(() => Array.from(props.entry.name.trim())[0]?.toUpperCase() ?? '•');
const icon = computed(() => webappIcon(props.entry.id));
const host = computed(() => {
  try {
    return new URL(props.entry.url).hostname.replace(/^www\./, '');
  } catch {
    return props.entry.url;
  }
});
</script>

<template>
  <OnCard padding="sm" class="flex h-full flex-col gap-10px">
    <div class="flex min-w-0 items-start gap-11px">
      <img
        v-if="icon"
        :src="icon"
        class="h-40px w-40px flex-none rounded-[10px] object-cover"
        alt=""
        aria-hidden="true"
      />
      <div
        v-else
        class="grid h-40px w-40px flex-none place-items-center rounded-[10px] text-16px font-650 text-white"
        :style="{ backgroundColor: entry.color }"
        aria-hidden="true"
      >
        {{ initial }}
      </div>
      <div class="min-w-0 flex-1">
        <p class="m-0 truncate text-13.5px font-600 text-ink-primary">{{ entry.name }}</p>
        <p class="m-0 mt-3px flex min-w-0 items-center gap-4px text-11.5px text-ink-tertiary">
          <OnIcon name="lucide:globe" :size="11" class="shrink-0" />
          <span class="truncate">{{ host }}</span>
        </p>
      </div>
    </div>

    <div class="mt-auto flex items-center gap-6px">
      <OnChip v-if="entry.warning === 'drm'" tone="warning" size="sm">
        {{ t('webapps.catalog.drm') }}
      </OnChip>
      <OnButton v-if="installed" class="ml-auto" size="sm" variant="ghost" icon="check" disabled>
        {{ t('webapps.catalog.added') }}
      </OnButton>
      <OnButton
        v-else
        class="ml-auto"
        size="sm"
        variant="secondary"
        :loading="adding"
        :disabled="disabled"
        @click="emit('add')"
      >
        {{ t('webapps.catalog.add') }}
      </OnButton>
    </div>
  </OnCard>
</template>
