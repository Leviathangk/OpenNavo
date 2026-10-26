<script setup lang="ts">
import { computed } from 'vue';
import { formatCount } from '@opennavo/shared';
import type { Schemas } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useRouterPush } from '@/hooks/common/router';
import { $t } from '@/locales';

defineOptions({ name: 'AgentCard' });

// Dashboard Agent changes card (11 §4): today's UTC successful non-preview writes, successful translations, failures; links to logs.
// Agents may have read-only dashboard access; cards show statistics only, never call contents.
const props = defineProps<{ hermes: Schemas['HermesDaily'] }>();

const appStore = useAppStore();
const { routerPushByKey } = useRouterPush();
const locale = computed(() => appStore.locale);
const items = computed(() => [
  { key: 'writes' as const, tab: 'calls', value: props.hermes.writes },
  { key: 'translations' as const, tab: 'translations', value: props.hermes.translations },
  { key: 'failures' as const, tab: 'calls', value: props.hermes.failures }
]);

function open(tab: string) {
  void routerPushByKey('ops_agent-log', { query: { tab } });
}
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper h-full">
    <div class="flex-col gap-4px">
      <div class="flex items-center justify-between">
        <NText depth="2" class="text-13px">{{ $t('page.dashboard.agent.title') }}</NText>
        <NButton text type="primary" size="small" @click="open('calls')">{{ $t('page.dashboard.agent.view') }}</NButton>
      </div>
      <div class="flex items-end gap-16px">
        <button
          v-for="item in items"
          :key="item.key"
          type="button"
          class="flex-col cursor-pointer items-start border-none bg-transparent p-0 text-left"
          @click="open(item.tab)"
        >
          <span
            class="text-22px font-600 tabular-nums"
            :class="item.key === 'failures' && item.value > 0 ? 'text-error' : ''"
          >
            {{ formatCount(item.value, { locale }) }}
          </span>
          <NText depth="3" class="text-12px">{{ $t(`page.dashboard.agent.${item.key}`) }}</NText>
        </button>
      </div>
      <NText depth="3" class="min-h-18px text-12px">{{ $t('page.dashboard.agent.date', { date: hermes.date }) }}</NText>
    </div>
  </NCard>
</template>
