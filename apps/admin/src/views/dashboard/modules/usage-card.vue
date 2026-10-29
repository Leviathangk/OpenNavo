<script setup lang="ts">
import { computed, ref } from 'vue';
import { useThemeVars } from 'naive-ui';
import { formatCount, formatDate, formatTime } from '@opennavo/shared';
import type { Schemas } from '@/typings/api/opennavo';
import { useAppStore } from '@/store/modules/app';
import { useThemeStore } from '@/store/modules/theme';
import { $t } from '@/locales';
import { USAGE_SERIES_COLORS } from './usage-series';
import UsageTrend from './usage-trend.vue';
import UsageCountryTable from './usage-country-table.vue';

defineOptions({ name: 'UsageCard' });

const props = defineProps<{ usage: Schemas['UsageOverview'] }>();
const theme = useThemeVars();
const appStore = useAppStore();
const themeStore = useThemeStore();
const locale = computed(() => appStore.locale);
const colors = computed(() => USAGE_SERIES_COLORS[themeStore.darkMode ? 'dark' : 'light']);
const text = (key: keyof typeof import('@/locales/langs/pages/zh-cn').default.dashboard.usage, values = {}) =>
  $t(`page.dashboard.usage.${key}`, values);
const count = (value: number) => formatCount(value, { locale: locale.value });
const day = (date: string) => formatDate(`${date}T00:00:00Z`, { locale: locale.value, timeZone: 'UTC' });

// Days before the first report are a collection gap, not zero activity, so the trend and the table start there.
const days = computed(() => {
  const first = props.usage.days.findIndex(entry => entry.desktop || entry.web || entry.newDesktop || entry.newWeb);
  return first < 0 ? [] : props.usage.days.slice(first);
});
const today = computed(() => props.usage.days.at(-1));
const yesterday = computed(() => (days.value.length > 1 ? days.value.at(-2) : undefined));
const metrics = computed(() => [
  {
    key: 'desktopTotal' as const,
    color: colors.value.desktop,
    value: count(props.usage.desktopTotal),
    description: text('desktopTotalHint')
  },
  ...(['desktop', 'web', 'newDesktop', 'newWeb'] as const).map(key => ({
    key,
    color: key === 'desktop' || key === 'web' ? colors.value[key] : null,
    value: count(today.value?.[key] ?? 0),
    description: text('yesterday', { value: yesterday.value ? count(yesterday.value[key]) : '—' })
  }))
]);
const trendTitle = computed(() =>
  text('trend', { start: day(days.value[0]?.date ?? ''), end: day(days.value.at(-1)?.date ?? '') })
);
const asOf = computed(() => text('asOf', { time: formatTime(props.usage.asOf, { timeZone: props.usage.timeZone }) }));
const showCountries = ref(false);
const countries = computed(() =>
  [...(props.usage.countries ?? [])].sort(
    (a, b) => b.desktop + b.web - a.desktop - a.web || a.country.localeCompare(b.country)
  )
);
const previewCountries = computed(() => countries.value.slice(0, 5));
const versionCeiling = computed(() => Math.max(1, ...props.usage.versions.map(version => version.count)));
const hasBreakdown = computed(() => props.usage.versions.length > 0 || Boolean(props.usage.countries?.length));
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper" :title="text('title')">
    <template #header-extra>
      <NText depth="3" class="text-12px">{{ asOf }}</NText>
    </template>
    <div class="flex-col gap-16px">
      <dl class="m-0 grid grid-cols-2 gap-12px lg:grid-cols-6">
        <div
          v-for="metric in metrics"
          :key="metric.key"
          :data-metric="metric.key"
          class="rounded-8px px-14px py-12px"
          :class="{ 'col-span-2': metric.key === 'desktopTotal' }"
          :style="{ background: theme.actionColor }"
        >
          <dt class="flex items-center gap-6px text-13px" :style="{ color: theme.textColor2 }">
            <span v-if="metric.color" class="h-2px w-12px rounded-full" :style="{ background: metric.color }" />
            {{ text(metric.key) }}
          </dt>
          <dd class="m-0 mt-4px text-26px font-600 leading-32px" :style="{ color: theme.textColor1 }">
            {{ metric.value }}
          </dd>
          <dd class="m-0 mt-2px text-12px" :style="{ color: theme.textColor3 }">{{ metric.description }}</dd>
        </div>
      </dl>
      <NEmpty v-if="!days.length" :description="text('empty')" />
      <div
        v-else
        class="grid grid-cols-1 gap-x-24px gap-y-16px"
        :class="{ 'xl:grid-cols-[minmax(0,1fr)_384px]': hasBreakdown }"
      >
        <UsageTrend :days="days" :colors="colors" :locale="locale" :title="trendTitle" />
        <div v-if="hasBreakdown" class="flex-col gap-16px">
          <section v-if="usage.versions.length">
            <h3 class="mb-8px mt-0 text-14px font-600">{{ text('versions') }}</h3>
            <NTable size="small" :bordered="false" class="tabular-nums">
              <thead>
                <tr>
                  <th scope="col">{{ text('version') }}</th>
                  <th scope="col" class="!text-right">{{ text('count') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="version in usage.versions" :key="version.version">
                  <th scope="row" class="!bg-transparent !font-normal">{{ version.version }}</th>
                  <td>
                    <div class="flex items-center justify-end gap-8px">
                      <span
                        class="h-6px rounded-r-3px"
                        :style="{ width: `${(version.count / versionCeiling) * 96}px`, background: colors.desktop }"
                      />
                      <span class="min-w-32px text-right">{{ count(version.count) }}</span>
                    </div>
                  </td>
                </tr>
              </tbody>
            </NTable>
          </section>
          <section v-if="countries.length" class="min-w-0" data-testid="usage-countries">
            <div class="mb-8px flex flex-wrap items-center justify-between gap-8px">
              <h3 class="m-0 text-14px font-600">{{ text('countries') }}</h3>
              <NText depth="3" class="text-12px">{{ text('countryOrder') }}</NText>
            </div>
            <UsageCountryTable :countries="previewCountries" :locale="locale" />
            <NButton
              v-if="countries.length > previewCountries.length"
              block
              secondary
              size="small"
              class="mt-8px"
              data-testid="usage-all-countries"
              aria-haspopup="dialog"
              :aria-expanded="showCountries"
              @click="showCountries = true"
            >
              {{ text('allCountries') }}
            </NButton>
          </section>
        </div>
      </div>
      <details v-if="days.length">
        <summary class="cursor-pointer rounded py-4px text-13px focus-visible:outline-solid">
          {{ text('details') }}
        </summary>
        <div class="mt-8px overflow-x-auto">
          <NTable :single-line="false" size="small" class="tabular-nums">
            <thead>
              <tr>
                <th scope="col">{{ text('date') }}</th>
                <th scope="col" class="!text-right">{{ text('desktop') }}</th>
                <th scope="col" class="!text-right">{{ text('web') }}</th>
                <th scope="col" class="!text-right">{{ text('newDesktop') }}</th>
                <th scope="col" class="!text-right">{{ text('newWeb') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="entry in [...days].reverse()" :key="entry.date">
                <th scope="row" class="whitespace-nowrap !bg-transparent !font-normal">{{ entry.date }}</th>
                <td class="text-right">{{ count(entry.desktop) }}</td>
                <td class="text-right">{{ count(entry.web) }}</td>
                <td class="text-right">{{ count(entry.newDesktop) }}</td>
                <td class="text-right">{{ count(entry.newWeb) }}</td>
              </tr>
            </tbody>
          </NTable>
        </div>
      </details>
      <div class="flex-col gap-4px text-12px" :style="{ color: theme.textColor3 }">
        <p class="m-0">{{ text('hint') }}</p>
        <p class="m-0">{{ text('newHint') }}</p>
      </div>
    </div>
    <NDrawer v-model:show="showCountries" width="min(560px, 100vw)">
      <NDrawerContent :title="text('countries')" closable>
        <p class="mb-12px mt-0 text-12px" :style="{ color: theme.textColor3 }">{{ text('countryOrder') }}</p>
        <UsageCountryTable :countries="countries" :locale="locale" />
      </NDrawerContent>
    </NDrawer>
  </NCard>
</template>
