<script setup lang="ts">
import { OnRailPanel, OnRailSection } from '@opennavo/ui';
import { useDeveloperMap } from '~/composables/useDeveloperMap';

const { t, locale } = useI18n();
const { data } = useDeveloperMap();

const number = computed(() => new Intl.NumberFormat(locale.value));
const regionNames = computed(() => new Intl.DisplayNames([locale.value], { type: 'region' }));

function countryName(code: string): string {
  try {
    return regionNames.value.of(code) ?? code;
  } catch {
    return code;
  }
}

const topCountries = computed(() => (data.value?.countries ?? []).slice(0, 6));
const recent = computed(() => data.value?.recentDevelopers ?? []);
</script>

<template>
  <OnRailPanel responsive>
    <OnRailSection :title="t('developerMap.rail.dataTitle')">
      <div class="rounded-big border border-solid border-line-subtle bg-surface-card px-13px py-11px">
        <p class="m-0 text-11.5px leading-[1.65] text-ink-secondary">
          {{ t('developerMap.rail.dataBody') }}
        </p>
        <div
          v-if="data"
          class="mt-10px flex flex-col gap-6px border-t border-solid border-line-subtle pt-9px text-11px text-ink-tertiary"
        >
          <div class="flex items-center justify-between gap-8px">
            <span>{{ t('developerMap.rail.located') }}</span>
            <b class="font-600 tabular-nums text-ink-primary"
              >{{ number.format(data.totals.located) }} / {{ number.format(data.totals.developers) }}</b
            >
          </div>
          <div class="flex items-center justify-between gap-8px">
            <span>{{ t('developerMap.rail.countries') }}</span>
            <b class="font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.countries) }}</b>
          </div>
          <div class="flex items-center justify-between gap-8px">
            <span>{{ t('developerMap.rail.updated') }}</span>
            <b class="font-500 text-ink-primary">{{ t('developerMap.rail.weekly') }}</b>
          </div>
        </div>
      </div>
    </OnRailSection>

    <OnRailSection v-if="topCountries.length" :title="t('developerMap.rail.topCountries')">
      <ol class="m-0 flex list-none flex-col gap-6px p-0">
        <li
          v-for="(country, index) in topCountries"
          :key="country.code"
          class="flex items-center gap-9px rounded-default px-6px py-5px"
        >
          <span class="w-14px shrink-0 text-10px tabular-nums text-ink-tertiary">{{
            String(index + 1).padStart(2, '0')
          }}</span>
          <span
            class="grid h-24px min-w-24px shrink-0 place-items-center rounded-[6px] bg-surface-inset px-4px text-9.5px font-700 uppercase tracking-[0.03em] text-ink-secondary"
            >{{ country.code }}</span
          >
          <span class="min-w-0 flex-1">
            <span class="block truncate text-12px font-500 text-ink-primary">{{ countryName(country.code) }}</span>
            <span class="block text-10px text-ink-tertiary">{{
              t('developerMap.map.appsAt', { count: number.format(country.apps) })
            }}</span>
          </span>
          <span class="shrink-0 text-11.5px font-600 tabular-nums text-ink-primary">{{
            number.format(country.developers)
          }}</span>
        </li>
      </ol>
    </OnRailSection>

    <OnRailSection v-if="recent.length" :title="t('developerMap.rail.recent')">
      <ul class="m-0 flex list-none flex-col gap-7px p-0">
        <li
          v-for="person in recent"
          :key="`${person.name}-${person.countryCode}-${person.city}`"
          class="flex min-w-0 items-center gap-8px"
        >
          <span
            class="grid h-24px w-24px shrink-0 place-items-center rounded-[6px] bg-surface-inset text-9.5px font-700 text-ink-secondary"
            >{{ person.name.slice(0, 1) }}</span
          >
          <span class="min-w-0 flex-1">
            <span class="block truncate text-11.5px font-500 text-ink-primary">{{ person.name }}</span>
            <span class="block truncate text-10px text-ink-tertiary">{{
              person.city || countryName(person.countryCode)
            }}</span>
          </span>
          <span class="shrink-0 text-9.5px text-ink-tertiary">{{ t('developerMap.rail.weekly') }}</span>
        </li>
      </ul>
    </OnRailSection>
  </OnRailPanel>
</template>
