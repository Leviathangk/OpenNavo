<script setup lang="ts">
import { computed } from 'vue';
import { formatCount } from '@opennavo/shared';
import { useAppStore } from '@/store/modules/app';
import { $t } from '@/locales';

defineOptions({ name: 'TodoCard' });

// Open feedback that still needs a reply. Translation work is Hermes' now (S8), so its counters and the
// LLM usage chart are gone from the dashboard.
defineProps<{ feedbackOpen: number }>();

const appStore = useAppStore();
const formattingLocale = computed(() => appStore.locale);
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper h-full" :title="$t('page.dashboard.todo')">
    <div class="flex items-center justify-between text-13px">
      <span>{{ $t('page.dashboard.feedbackOpen') }}</span>
      <RouterLink to="/ops/feedback" class="flex items-center gap-8px">
        <span class="text-20px font-600 tabular-nums">
          {{ formatCount(feedbackOpen, { locale: formattingLocale }) }}
        </span>
        <span class="text-12px text-primary">{{ $t('page.dashboard.viewFeedback') }} →</span>
      </RouterLink>
    </div>
  </NCard>
</template>
