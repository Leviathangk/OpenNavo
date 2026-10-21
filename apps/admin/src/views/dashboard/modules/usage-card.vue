<script setup lang="ts">
import { computed, useTemplateRef } from 'vue';
import { useElementSize } from '@vueuse/core';
import { useThemeVars } from 'naive-ui';
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

const props = defineProps<{ usage: Schemas['UsageOverview'] }>();
const theme = useThemeVars();
const chart = useTemplateRef<HTMLElement>('chart');
const { width } = useElementSize(chart);
const chartWidth = computed(() => Math.max(300, width.value));
const text = (key: keyof typeof import('@/locales/langs/pages/zh-cn').default.dashboard.usage) =>
  $t(`page.dashboard.usage.${key}`);
const today = computed(() => props.usage.days.at(-1));
const hasData = computed(() => props.usage.days.some(day => day.desktop || day.web));
const ceiling = computed(() => Math.max(1, ...props.usage.days.flatMap(day => [day.web, day.desktop])));
const points = (platform: 'web' | 'desktop') =>
  props.usage.days
    .map(
      (day, index) =>
        `${40 + (index * (chartWidth.value - 80)) / Math.max(1, props.usage.days.length - 1)},${180 - (day[platform] / ceiling.value) * 150}`
    )
    .join(' ');
</script>

<template>
  <NCard :bordered="false" size="small" class="card-wrapper" :title="text('title')">
    <div class="flex-col gap-16px">
      <NText depth="3">{{ text('hint') }}</NText>
      <div v-if="today" class="flex flex-wrap gap-x-32px gap-y-8px text-16px tabular-nums">
        <span
          >{{ text('desktop') }}: <strong>{{ today.desktop }}</strong></span
        >
        <span
          >{{ text('web') }}: <strong>{{ today.web }}</strong></span
        >
        <span
          >{{ text('newDesktop') }}: <strong>{{ today.newDesktop }}</strong></span
        >
        <span
          >{{ text('newWeb') }}: <strong>{{ today.newWeb }}</strong></span
        >
      </div>
      <NEmpty v-if="!hasData" :description="text('empty')" />
      <figure v-else ref="chart" class="m-0">
        <figcaption class="mb-8px font-600">{{ text('trend') }}</figcaption>
        <div class="flex flex-wrap gap-16px text-13px">
          <span :style="{ color: theme.primaryColorPressed }">{{ text('desktop') }} —</span>
          <span :style="{ color: theme.textColorBase }">{{ text('web') }} ···</span>
        </div>
        <svg :viewBox="`0 0 ${chartWidth} 220`" class="block w-full" role="img" :aria-label="text('trend')">
          <title>{{ text('trend') }}</title>
          <path :d="`M40 30 V180 H${chartWidth - 40}`" fill="none" :stroke="theme.borderColor" />
          <text x="32" y="35" text-anchor="end" :fill="theme.textColor2" font-size="12">{{ ceiling }}</text>
          <text x="32" y="184" text-anchor="end" :fill="theme.textColor2" font-size="12">0</text>
          <polyline :points="points('desktop')" fill="none" :stroke="theme.primaryColorPressed" stroke-width="2" />
          <polyline
            :points="points('web')"
            fill="none"
            :stroke="theme.textColorBase"
            stroke-width="2"
            stroke-dasharray="5 4"
          />
          <text x="40" y="207" :fill="theme.textColor2" font-size="12">{{ usage.days[0]?.date }}</text>
          <text :x="chartWidth - 40" y="207" text-anchor="end" :fill="theme.textColor2" font-size="12">
            {{ today?.date }}
          </text>
        </svg>
      </figure>
      <details>
        <summary class="cursor-pointer rounded py-8px focus-visible:outline-solid">{{ text('details') }}</summary>
        <div class="overflow-x-auto">
          <NTable :single-line="false" size="small" class="tabular-nums">
            <thead>
              <tr>
                <th scope="col">{{ text('date') }}</th>
                <th scope="col">{{ text('desktop') }}</th>
                <th scope="col">{{ text('web') }}</th>
                <th scope="col">{{ text('newDesktop') }}</th>
                <th scope="col">{{ text('newWeb') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="day in [...usage.days].reverse()" :key="day.date">
                <th scope="row" class="whitespace-nowrap">{{ day.date }}</th>
                <td>{{ day.desktop }}</td>
                <td>{{ day.web }}</td>
                <td>{{ day.newDesktop }}</td>
                <td>{{ day.newWeb }}</td>
              </tr>
            </tbody>
          </NTable>
        </div>
      </details>
      <section v-if="usage.versions.length">
        <h3 class="mb-8px mt-0 text-14px font-600">{{ text('versions') }}</h3>
        <NTable size="small" class="tabular-nums">
          <thead>
            <tr>
              <th scope="col">{{ text('version') }}</th>
              <th scope="col">{{ text('count') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="version in usage.versions" :key="version.version">
              <th scope="row">{{ version.version }}</th>
              <td>{{ version.count }}</td>
            </tr>
          </tbody>
        </NTable>
      </section>
      <NText depth="3" class="text-12px">{{ text('newHint') }}</NText>
    </div>
  </NCard>
</template>
