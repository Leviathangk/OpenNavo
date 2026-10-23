<script setup lang="ts">
import { computed as computedFormattingLocale } from 'vue';
import { useAppStore as useFormattingAppStore } from '@/store/modules/app';
import { formatCount } from '@opennavo/shared';

const formattingAppStore = useFormattingAppStore();
const formattingLocale = computedFormattingLocale(() => formattingAppStore.locale);

defineOptions({ name: 'StatCard' });

defineProps<{ label: string; value: number; hint?: string }>();
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper h-full">
    <div class="flex-col gap-4px">
      <NText depth="2" class="text-13px">{{ label }}</NText>
      <span class="text-28px font-600">{{ formatCount(value, { locale: formattingLocale }) }}</span>
      <NText depth="3" class="min-h-18px text-12px">{{ hint }}</NText>
    </div>
  </NCard>
</template>
