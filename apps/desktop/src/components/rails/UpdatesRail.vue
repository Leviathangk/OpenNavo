<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatDate, formatPercent, formatTime } from '@opennavo/shared';
import { OnButton, OnCard, OnRailPanel, OnRailSection, OnStatTile, OnToggle } from '@opennavo/ui';
import { commands, unwrap } from '@/ipc/client';
import type { AppSettings } from '@/ipc/client';
import type { UpdateSource } from '@/ipc/bindings';
import { loadHistory } from '@/utils/history';
import { useAppLocale } from '@/composables/useAppLocale';
import { fetchClientConfig } from '@/composables/useClientConfig';
import { useClock } from '@/composables/useClock';
import { useLoader } from '@/composables/useLoader';
import { usePackageState } from '@/composables/usePackageState';
import { useToasts } from '@/composables/useToasts';
import { useSettingsStore, useTasksStore, useUpdatesStore } from '@/stores';

// Updates rail (mockup 05): monthly stats, automatic-update setting, current source/latency, next check.
const { t, te } = useI18n();
const { appLocale } = useAppLocale();
const settings = useSettingsStore();
const tasks = useTasksStore();
const updates = useUpdatesStore();
const toasts = useToasts();
const { errorText } = usePackageState();

const clock = useClock();
const monthStart = computed(() => {
  const now = new Date(clock.value);
  return new Date(now.getFullYear(), now.getMonth(), 1).getTime();
});

const { data: month } = useLoader(
  () =>
    loadHistory({
      q: null,
      ops: ['upgrade'],
      states: ['succeeded', 'failed'],
      target: null,
      includeScheduledUpdates: false,
      from: monthStart.value,
      to: null,
      limit: Number.MAX_SAFE_INTEGER,
      offset: 0
    }),
  [() => tasks.finished, monthStart],
  undefined,
  [monthStart]
);

const stats = computed(() => {
  const items = month.value?.items ?? [];
  const succeeded = items.filter(task => task.state === 'succeeded');
  const failed = items.length - succeeded.length;
  return {
    updated: succeeded.length,
    rate: items.length ? Math.round((succeeded.length / items.length) * 100) : null,
    failed
  };
});

const toggles = computed(() => {
  const value = settings.value;
  if (!value) return [];
  return [
    {
      key: 'autoCheck' as const,
      label: t('updates.auto.check', { time: value.checkTime }),
      hint: t('updates.auto.checkHint'),
      value: value.autoCheck
    },
    {
      key: 'autoUpgradeCasks' as const,
      label: t('updates.auto.casks'),
      hint: t('updates.auto.casksHint'),
      value: value.autoUpgradeCasks
    },
    {
      key: 'includeGreedy' as const,
      label: t('updates.auto.greedy'),
      hint: t('updates.auto.greedyHint'),
      value: value.includeGreedy
    }
  ];
});

function toggle(key: keyof Pick<AppSettings, 'autoCheck' | 'autoUpgradeCasks' | 'includeGreedy'>, value: boolean) {
  void settings.update(key, value);
}

// Sources the checks cover (store/security plan §7, §11.4): a source missing from settings is on. Microsoft
// AutoUpdate joins when its check ships (P3).
const SOURCES = [
  'homebrew',
  'mas',
  'sparkle',
  'electron',
  'github',
  'vendor'
] as const satisfies readonly UpdateSource[];
const sources = computed(() => {
  const value = settings.value;
  if (!value) return [];
  return SOURCES.map(source => ({
    source,
    label: t(`updates.sources.${source}`),
    hint: t(`updates.sources.${source}Hint`),
    value: value.updateSources[source] ?? true
  }));
});

async function toggleSource(source: UpdateSource, on: boolean) {
  try {
    await settings.setUpdateSource(source, on);
  } catch (error) {
    toasts.push({ tone: 'danger', title: t('errors.actionFailed'), description: errorText(error) });
  }
}

// Current source: name from remote configuration; probe only this source.
const mirrorName = computed(() => {
  const key = settings.value?.mirror.key;
  if (!key) return '';
  return te(`env.mirrors.${key}`) ? t(`env.mirrors.${key}`) : key;
});
const { data: probe } = useLoader(async () => {
  const key = settings.value?.mirror.key;
  const config = await fetchClientConfig();
  const mirror = config?.mirrors.find(item => item.key === key);
  if (!mirror) return;
  const [result] = await unwrap(
    commands.mirrorProbe([
      {
        key: mirror.key,
        name: mirror.name,
        probeUrl: mirror.probeUrl,
        apiDomain: mirror.apiDomain ?? null,
        bottleDomain: mirror.bottleDomain ?? null,
        brewGitRemote: mirror.brewGitRemote ?? null,
        coreGitRemote: mirror.coreGitRemote ?? null
      }
    ])
  );
  return result ?? null;
}, [() => settings.value?.mirror.key, appLocale]);

// Next check (updates_status): today, tomorrow, or a later date.
const nextLabel = computed(() => {
  const next = updates.nextCheckAt;
  if (!next) return '';
  const time = formatTime(next);
  const day = new Date(next).toDateString();
  const now = new Date(clock.value);
  if (day === now.toDateString()) return t('updates.next.today', { time });
  const tomorrow = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).toDateString();
  if (day === tomorrow) return t('updates.next.tomorrow', { time });
  return `${formatDate(next, { locale: appLocale.value })} ${time}`;
});
</script>

<template>
  <OnRailPanel>
    <OnRailSection
      :title="t('updates.month.title')"
      :subtitle="t('updates.month.since', { date: formatDate(monthStart, { locale: appLocale }) })"
    >
      <div class="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-8px">
        <OnStatTile
          :label="t('updates.month.updated')"
          :value="month ? String(stats.updated) : '—'"
          :unit="t('updates.month.updatedUnit')"
        />
        <OnStatTile
          :label="t('updates.month.successRate')"
          :value="stats.rate === null ? '—' : formatPercent(stats.rate / 100, { locale: appLocale })"
          :hint="month ? t('updates.month.successHint', { count: stats.failed }, { plural: stats.failed }) : undefined"
        />
      </div>
    </OnRailSection>

    <OnRailSection v-if="toggles.length" :title="t('updates.auto.title')" :subtitle="t('updates.auto.subtitle')">
      <OnCard padding="none">
        <ul class="m-0 list-none p-0">
          <li
            v-for="item in toggles"
            :key="item.key"
            class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-16px py-12px first:border-t-0"
          >
            <span class="min-w-0 flex-1">
              <span :id="`auto-${item.key}`" class="block text-13.5px text-ink-primary">{{ item.label }}</span>
              <span class="block text-12px text-ink-tertiary">{{ item.hint }}</span>
            </span>
            <OnToggle
              :model-value="item.value"
              :aria-labelledby="`auto-${item.key}`"
              @update:model-value="value => toggle(item.key, value)"
            />
          </li>
        </ul>
      </OnCard>
    </OnRailSection>

    <OnRailSection v-if="sources.length" :title="t('updates.sources.title')" :subtitle="t('updates.sources.subtitle')">
      <OnCard padding="none">
        <ul class="m-0 list-none p-0">
          <li
            v-for="item in sources"
            :key="item.source"
            class="flex items-center gap-12px border-t border-t-solid border-line-subtle px-16px py-12px first:border-t-0"
          >
            <span class="min-w-0 flex-1">
              <span :id="`source-${item.source}`" class="block text-13.5px text-ink-primary">{{ item.label }}</span>
              <span class="block text-12px text-ink-tertiary">{{ item.hint }}</span>
            </span>
            <OnToggle
              :model-value="item.value"
              :aria-labelledby="`source-${item.source}`"
              @update:model-value="value => toggleSource(item.source, value)"
            />
          </li>
        </ul>
      </OnCard>
    </OnRailSection>

    <OnCard v-if="mirrorName" padding="md" class="flex items-center gap-12px">
      <span class="min-w-0 flex-1">
        <span class="block text-14px font-600 text-ink-primary">{{ mirrorName }}</span>
        <span v-if="probe?.latencyMs" class="block text-12px text-ink-tertiary">{{
          t('updates.mirror.latency', { ms: probe.latencyMs })
        }}</span>
      </span>
      <OnButton variant="secondary" size="sm" href="/settings/mirrors" :link-as="RouterLink">{{
        t('updates.mirror.switch')
      }}</OnButton>
    </OnCard>

    <OnCard padding="md" class="flex flex-col gap-6px">
      <h2 class="m-0 text-14px font-600 text-ink-primary">{{ t('updates.next.title') }}</h2>
      <p class="m-0 text-12.5px leading-[1.6] text-ink-tertiary">
        {{ nextLabel ? t('updates.next.text', { time: nextLabel }) : t('updates.next.off') }}
      </p>
    </OnCard>
  </OnRailPanel>
</template>
