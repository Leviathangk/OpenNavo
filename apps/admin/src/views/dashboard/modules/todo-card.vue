<script setup lang="ts">
import { computed } from 'vue';
import { formatCount } from '@opennavo/shared';
import { useAppStore } from '@/store/modules/app';
import { $t } from '@/locales';

defineOptions({ name: 'TodoCard' });

// Open feedback that still needs a reply, shown as a headline card. Translation work is Hermes' now (S8), so its
// counters and the LLM usage chart are gone from the dashboard.
defineProps<{ feedbackOpen: number }>();

const appStore = useAppStore();
const formattingLocale = computed(() => appStore.locale);
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper h-full">
    <div class="flex-col gap-4px">
      <div class="flex items-center justify-between gap-8px">
        <NText depth="2" class="text-13px">{{ $t('page.dashboard.feedbackOpen') }}</NText>
        <RouterLink to="/ops/feedback" class="text-12px text-primary">{{ $t('page.dashboard.viewFeedback') }} →</RouterLink>
      </div>
      <span class="text-28px font-600">
        {{ formatCount(feedbackOpen, { locale: formattingLocale }) }}
      </span>
      <span class="min-h-18px" />
    </div>
  </NCard>
</template>
