<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnButton, OnCard, OnIcon } from '@opennavo/ui';
import type { WebApp } from '@/ipc/bindings';

const props = defineProps<{ app: WebApp }>();
const emit = defineEmits<{ open: []; reveal: []; remove: [] }>();
const { t, locale } = useI18n();

const initial = computed(() => props.app.name.trim().charAt(0).toUpperCase() || '•');
const host = computed(() => {
  try {
    return new URL(props.app.url).hostname.replace(/^www\./, '');
  } catch {
    return props.app.url;
  }
});
const created = computed(() => {
  const date = new Date(props.app.createdAt);
  return Number.isNaN(date.getTime())
    ? props.app.createdAt
    : new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium' }).format(date);
});
</script>

<template>
  <OnCard padding="sm" class="flex h-full flex-col gap-12px">
    <div class="flex min-w-0 items-start gap-11px">
      <img
        v-if="app.icon"
        :src="app.icon"
        alt=""
        class="h-40px w-40px flex-none rounded-[10px] bg-surface-inset object-cover"
      />
      <div
        v-else
        class="grid h-40px w-40px flex-none place-items-center rounded-[10px] border border-solid border-line-subtle bg-surface-inset text-16px font-600 text-ink-primary"
        aria-hidden="true"
      >
        {{ initial }}
      </div>
      <div class="min-w-0 flex-1">
        <p class="m-0 truncate text-13.5px font-600 text-ink-primary">{{ app.name }}</p>
        <p class="m-0 mt-3px flex min-w-0 items-center gap-4px text-11.5px text-ink-tertiary">
          <OnIcon name="lucide:globe" :size="11" class="shrink-0" />
          <span class="truncate">{{ host }}</span>
        </p>
      </div>
    </div>

    <p class="m-0 text-11px text-ink-tertiary">{{ t('webapps.createdAt', { date: created }) }}</p>

    <div class="mt-auto flex flex-wrap items-center gap-6px">
      <OnButton size="sm" variant="secondary" @click="emit('open')">{{ t('webapps.open') }}</OnButton>
      <OnButton size="sm" variant="ghost" @click="emit('reveal')">{{ t('webapps.reveal') }}</OnButton>
      <OnButton size="sm" variant="ghost" class="ml-auto" @click="emit('remove')">{{ t('webapps.delete') }}</OnButton>
    </div>
  </OnCard>
</template>
