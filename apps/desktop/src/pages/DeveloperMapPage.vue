<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { OnIcon } from '@opennavo/ui';
import DeveloperMapWorld from '@/components/developer/DeveloperMapWorld.vue';
import DeveloperSubmitForm from '@/components/developer/DeveloperSubmitForm.vue';
import DeveloperClaimForm from '@/components/developer/DeveloperClaimForm.vue';
import DeveloperStatusPanel from '@/components/developer/DeveloperStatusPanel.vue';
import { useAppLocale } from '@/composables/useAppLocale';
import { useLoader } from '@/composables/useLoader';
import { developerPlaceName, fetchDeveloperMap, type DeveloperMapPlace } from '@/composables/useDeveloperMap';

const { t, locale } = useI18n();
const { appLocale } = useAppLocale();
const { data, error } = useLoader(() => fetchDeveloperMap(appLocale.value), [appLocale]);

const tab = ref<'map' | 'submit' | 'claim' | 'docs'>('map');
const metric = ref<'developers' | 'installs'>('developers');
const region = ref<'all' | 'americas' | 'europe' | 'apac' | 'other'>('all');
const selected = ref<string | null>(null);
const REGIONS = ['all', 'americas', 'europe', 'apac', 'other'] as const;
function setRegion(key: string) {
  region.value = key as typeof region.value;
}

const number = computed(() => new Intl.NumberFormat(locale.value));
const regionNames = computed(() => new Intl.DisplayNames([locale.value], { type: 'region' }));
const dateFormat = computed(() => new Intl.DateTimeFormat(locale.value, { month: 'short', day: 'numeric' }));

function countryName(code: string): string {
  try {
    return regionNames.value.of(code) ?? code;
  } catch {
    return code;
  }
}

function placeName(place: DeveloperMapPlace): string {
  return developerPlaceName(place, locale.value);
}

function metricValue(place: DeveloperMapPlace): number {
  return metric.value === 'installs' ? place.installs30d : place.developers;
}
const places = computed(() => [...(data.value?.places ?? [])].sort((a, b) => metricValue(b) - metricValue(a)));
const topPlaces = computed(() => places.value.slice(0, 6));
const selectedPlace = computed(() => places.value.find(place => place.key === selected.value) ?? null);
const visiblePlaces = computed(() =>
  region.value === 'all' ? places.value : places.value.filter(place => place.region === region.value)
);
const countries = computed(() => (data.value?.countries ?? []).slice(0, 6));
const recent = computed(() => data.value?.recentDevelopers ?? []);
const webBase = computed(() =>
  appLocale.value.startsWith('zh') ? 'https://opennavo.com/zh/' : 'https://opennavo.com/'
);
const docsURL = computed(() => `${webBase.value}developers`);
const statusToken = ref('');

const tabs = computed(() => [
  { key: 'map' as const, label: t('developerMap.tabs.map'), icon: 'globe' },
  { key: 'submit' as const, label: t('developerMap.tabs.submit'), icon: 'package' },
  { key: 'claim' as const, label: t('developerMap.tabs.claim'), icon: 'shield-check' },
  { key: 'docs' as const, label: t('developerMap.tabs.docs'), icon: 'file-text' }
]);
</script>

<template>
  <div class="mx-auto flex w-full max-w-1160 flex-col gap-20px pb-32px">
    <header class="flex flex-wrap items-end justify-between gap-12px">
      <div class="min-w-0">
        <h1 class="m-0 text-24px font-700 leading-[1.15] text-ink-primary">{{ t('developerMap.title') }}</h1>
        <p class="m-0 mt-6px max-w-720px text-13px leading-[1.7] text-ink-secondary">
          <template v-if="data">
            <b class="font-600 text-ink-primary">{{ number.format(data.totals.apps) }}</b>
            {{ t('developerMap.apps') }} ·
            <b class="font-600 text-ink-primary">{{ number.format(data.totals.developers) }}</b>
            {{ t('developerMap.developers') }}
          </template>
          <template v-else>{{ t('developerMap.lede') }}</template>
        </p>
        <div v-if="data" class="mt-10px flex flex-wrap items-baseline gap-x-16px gap-y-5px text-11px text-ink-tertiary">
          <span
            ><b class="text-13px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.developers) }}</b>
            {{ t('developerMap.stats.developers') }}</span
          >
          <span
            ><b class="text-13px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.located) }}</b>
            {{ t('developerMap.stats.located') }}</span
          >
          <span
            ><b class="text-13px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.places) }}</b>
            {{ t('developerMap.stats.cities') }}</span
          >
          <span
            ><b class="text-13px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.countries) }}</b>
            {{ t('developerMap.stats.countries') }}</span
          >
        </div>
      </div>
      <a
        :href="docsURL"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex h-28px items-center rounded-default border border-solid border-line-subtle px-11px text-11.5px font-500 text-ink-secondary no-underline hover:bg-surface-raised hover:text-ink-primary"
      >
        {{ t('developerMap.actions.docs') }}
      </a>
    </header>

    <nav
      class="flex w-fit max-w-full gap-2px overflow-x-auto rounded-default border border-solid border-line-subtle bg-surface-inset p-3px"
      role="tablist"
    >
      <button
        v-for="item in tabs"
        :key="item.key"
        type="button"
        role="tab"
        :aria-selected="tab === item.key"
        class="inline-flex h-27px flex-none items-center gap-5px rounded-[7px] px-10px text-11.5px font-500 outline-none"
        :class="
          tab === item.key
            ? 'bg-surface-card text-ink-primary shadow-[0_1px_2px_rgba(0,0,0,0.18)]'
            : 'text-ink-secondary hover:text-ink-primary'
        "
        @click="tab = item.key"
      >
        <OnIcon :name="item.icon" :size="13" />{{ item.label }}
      </button>
    </nav>

    <div
      v-if="error"
      class="rounded-big border border-solid border-line-subtle bg-surface-card p-16px text-12.5px text-ink-secondary"
    >
      {{ t('developerMap.empty') }}
    </div>

    <template v-else-if="tab === 'map'">
      <section class="overflow-hidden rounded-big border border-solid border-line-subtle bg-surface-card">
        <div
          class="flex flex-wrap items-center justify-between gap-8px border-b border-solid border-line-subtle px-11px py-8px"
        >
          <div class="flex items-center gap-2px rounded-[8px] bg-surface-inset p-2px">
            <button
              type="button"
              class="h-23px rounded-[6px] px-8px text-11px font-500"
              :class="metric === 'developers' ? 'bg-surface-card text-ink-primary' : 'text-ink-secondary'"
              @click="metric = 'developers'"
            >
              {{ t('developerMap.map.developers') }}
            </button>
            <button
              type="button"
              class="h-23px rounded-[6px] px-8px text-11px font-500"
              :class="metric === 'installs' ? 'bg-surface-card text-ink-primary' : 'text-ink-secondary'"
              @click="metric = 'installs'"
            >
              {{ t('developerMap.map.installs') }}
            </button>
          </div>
          <div class="flex items-center gap-1px overflow-x-auto">
            <button
              v-for="key in REGIONS"
              :key="key"
              type="button"
              class="h-23px flex-none rounded-[6px] px-7px text-11px font-500"
              :class="
                region === key ? 'bg-surface-raised text-ink-primary' : 'text-ink-secondary hover:text-ink-primary'
              "
              @click="setRegion(key)"
            >
              {{ t(`developerMap.map.${key}`) }}
            </button>
          </div>
        </div>
        <div v-if="data" class="relative">
          <DeveloperMapWorld
            :places="data.places"
            :selected="selected"
            :metric="metric"
            :region="region"
            @select="key => (selected = key)"
          />
          <div class="pointer-events-none absolute bottom-9px left-11px text-10px text-ink-tertiary">
            {{
              t('developerMap.map.legend', {
                metric: metric === 'developers' ? t('developerMap.map.developers') : t('developerMap.map.installs')
              })
            }}
          </div>
          <div class="absolute right-11px bottom-9px text-10px text-ink-tertiary">
            {{
              t('developerMap.map.updated', {
                date: data.generatedAt ? dateFormat.format(new Date(data.generatedAt)) : ''
              })
            }}
          </div>
        </div>
        <div class="border-t border-solid border-line-subtle px-11px py-10px">
          <template v-if="selectedPlace">
            <div class="mb-7px flex items-center gap-7px">
              <button
                type="button"
                class="grid h-22px w-22px place-items-center rounded-[6px] text-ink-secondary hover:bg-surface-raised hover:text-ink-primary"
                @click="selected = null"
              >
                <OnIcon name="arrow-left" :size="13" />
              </button>
              <b class="text-12.5px font-600 text-ink-primary">{{ placeName(selectedPlace) }}</b>
              <span class="text-10.5px text-ink-tertiary"
                >{{ selectedPlace.countryCode }} ·
                {{
                  metric === 'installs'
                    ? `${number.format(selectedPlace.installs30d)} ${t('developerMap.map.installs')}`
                    : t('developerMap.map.developersAt', { count: number.format(selectedPlace.developers) })
                }}
                · {{ t('developerMap.map.appsAt', { count: number.format(selectedPlace.apps) }) }}</span
              >
            </div>
            <div class="grid grid-cols-1 gap-5px sm:grid-cols-2">
              <div
                v-for="developer in selectedPlace.topDevelopers"
                :key="developer.name"
                class="flex min-w-0 items-center gap-7px rounded-default border border-solid border-line-subtle px-8px py-6px"
              >
                <span
                  class="grid h-22px w-22px shrink-0 place-items-center rounded-[6px] bg-surface-inset text-9px font-700 text-ink-secondary"
                  >{{ developer.name.slice(0, 1) }}</span
                >
                <span class="min-w-0 flex-1 truncate text-11.5px text-ink-primary">{{ developer.name }}</span>
                <span class="shrink-0 text-10px text-ink-tertiary">{{
                  t('developerMap.map.appsAt', { count: developer.apps })
                }}</span>
              </div>
            </div>
          </template>
          <template v-else>
            <div class="mb-7px flex items-baseline justify-between gap-8px">
              <b class="text-12.5px font-600 text-ink-primary">{{ t('developerMap.map.topCities') }}</b
              ><span class="text-10px text-ink-tertiary">{{ t('developerMap.map.citiesHint') }}</span>
            </div>
            <div class="grid grid-cols-1 gap-x-12px gap-y-1px sm:grid-cols-2 lg:grid-cols-3">
              <button
                v-for="(place, index) in topPlaces"
                :key="place.key"
                type="button"
                class="flex min-w-0 items-center gap-7px rounded-[6px] px-4px py-5px text-left hover:bg-surface-raised"
                @click="selected = place.key"
              >
                <span class="w-14px shrink-0 text-10px tabular-nums text-ink-tertiary">{{
                  String(index + 1).padStart(2, '0')
                }}</span>
                <span class="min-w-0 flex-1"
                  ><span class="block truncate text-11.5px font-500 text-ink-primary">{{ placeName(place) }}</span
                  ><span class="block truncate text-10px text-ink-tertiary"
                    >{{ countryName(place.countryCode) }} ·
                    {{ t('developerMap.map.appsAt', { count: place.apps }) }}</span
                  ></span
                >
                <span class="shrink-0 text-11.5px font-600 tabular-nums text-ink-primary">{{
                  number.format(metricValue(place))
                }}</span>
              </button>
            </div>
          </template>
        </div>
      </section>

      <section>
        <div class="mb-9px">
          <h2 class="m-0 text-15px font-600 text-ink-primary">{{ t('developerMap.map.cities') }}</h2>
          <p class="m-0 mt-2px text-11px text-ink-tertiary">
            {{ t('developerMap.map.citiesHint') }} · {{ number.format(visiblePlaces.length) }}
          </p>
        </div>
        <div class="grid grid-cols-[repeat(auto-fill,minmax(210px,1fr))] gap-8px">
          <button
            v-for="(place, index) in visiblePlaces"
            :key="place.key"
            type="button"
            class="flex min-w-0 flex-col gap-6px rounded-big border border-solid border-line-subtle bg-surface-card px-11px py-9px text-left hover:bg-surface-raised"
            @click="selected = place.key"
          >
            <span class="flex items-baseline justify-between gap-7px"
              ><span class="text-10px tabular-nums text-ink-tertiary">{{ String(index + 1).padStart(2, '0') }}</span
              ><span class="text-11.5px font-600 tabular-nums text-ink-primary">{{
                number.format(metricValue(place))
              }}</span></span
            >
            <span class="min-w-0"
              ><span class="block truncate text-12.5px font-600 text-ink-primary">{{ placeName(place) }}</span
              ><span class="block truncate text-10px text-ink-tertiary"
                >{{ countryName(place.countryCode) }} · {{ t('developerMap.map.appsAt', { count: place.apps }) }}</span
              ></span
            >
          </button>
        </div>
      </section>

      <div class="grid grid-cols-1 gap-10px md:grid-cols-2">
        <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-12px">
          <h3 class="m-0 text-12.5px font-600 text-ink-primary">{{ t('developerMap.rail.topCountries') }}</h3>
          <ul class="m-0 mt-7px flex list-none flex-col gap-5px p-0">
            <li v-for="(country, index) in countries" :key="country.code" class="flex items-center gap-7px">
              <span class="w-13px text-9.5px tabular-nums text-ink-tertiary">{{
                String(index + 1).padStart(2, '0')
              }}</span>
              <span
                class="grid h-22px min-w-22px place-items-center rounded-[5px] bg-surface-inset px-3px text-9px font-700 text-ink-secondary"
                >{{ country.code }}</span
              >
              <span class="min-w-0 flex-1 truncate text-11.5px text-ink-primary">{{ countryName(country.code) }}</span>
              <span class="text-11px font-600 tabular-nums text-ink-primary">{{
                number.format(country.developers)
              }}</span>
            </li>
          </ul>
        </section>
        <section class="rounded-big border border-solid border-line-subtle bg-surface-card p-12px">
          <h3 class="m-0 text-12.5px font-600 text-ink-primary">{{ t('developerMap.rail.recent') }}</h3>
          <ul class="m-0 mt-7px flex list-none flex-col gap-5px p-0">
            <li
              v-for="person in recent"
              :key="`${person.name}-${person.countryCode}-${person.city}`"
              class="flex min-w-0 items-center gap-7px"
            >
              <span
                class="grid h-22px w-22px shrink-0 place-items-center rounded-[5px] bg-surface-inset text-9px font-700 text-ink-secondary"
                >{{ person.name.slice(0, 1) }}</span
              >
              <span class="min-w-0 flex-1"
                ><span class="block truncate text-11.5px text-ink-primary">{{ person.name }}</span
                ><span class="block truncate text-10px text-ink-tertiary">{{
                  person.city || countryName(person.countryCode)
                }}</span></span
              >
            </li>
          </ul>
        </section>
      </div>
    </template>

    <template v-else-if="tab === 'submit'">
      <DeveloperSubmitForm @submitted="statusToken = $event" />
      <DeveloperStatusPanel :initial-token="statusToken" />
    </template>
    <template v-else-if="tab === 'claim'">
      <DeveloperClaimForm @submitted="statusToken = $event" />
      <DeveloperStatusPanel :initial-token="statusToken" />
    </template>

    <section v-else class="rounded-big border border-solid border-line-subtle bg-surface-card p-18px">
      <h2 class="m-0 text-16px font-600 text-ink-primary">{{ t('developerMap.docs.title') }}</h2>
      <p class="m-0 mt-5px max-w-680px text-12px leading-[1.7] text-ink-secondary">{{ t('developerMap.docs.body') }}</p>
      <a
        :href="docsURL"
        target="_blank"
        rel="noopener noreferrer"
        class="mt-12px inline-flex items-center gap-4px text-12px font-500 text-ink-primary underline-offset-3 hover:underline"
        >{{ t('developerMap.actions.docs') }} <OnIcon name="arrow-up-right" :size="13"
      /></a>
    </section>
  </div>
</template>
