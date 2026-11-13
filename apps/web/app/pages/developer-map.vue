<script setup lang="ts">
import { computed, ref } from 'vue';
import { OnErrorState, OnIcon } from '@opennavo/ui';
import { developerPlaceName, useDeveloperMap, type DeveloperMapPlace } from '~/composables/useDeveloperMap';

definePageMeta({ rail: 'developer-map' });

const { t, locale } = useI18n();
const localePath = useLocalePath();
const route = useRoute();
const router = useRouter();
const NuxtLink = resolveComponent('NuxtLink');

type Tab = 'map' | 'submit' | 'claim' | 'docs';
const normalizeTab = (value: unknown): Tab => {
  if (value === 'submit' || value === 'claim' || value === 'docs') return value;
  if (value === 'location') return 'claim';
  return 'map';
};

const tab = computed<Tab>({
  get: () => normalizeTab(route.query.tab ?? route.query.view),
  set: value => router.replace({ query: { ...route.query, tab: value, view: undefined } })
});

const { data, error, refresh } = await useDeveloperMap();

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

const metric = ref<'developers' | 'installs'>('developers');
const region = ref<'all' | 'americas' | 'europe' | 'apac' | 'other'>('all');
const selectedKey = ref<string | null>(null);

function metricValue(place: DeveloperMapPlace): number {
  return metric.value === 'installs' ? place.installs30d : place.developers;
}
const sortedPlaces = computed(() => [...(data.value?.places ?? [])].sort((a, b) => metricValue(b) - metricValue(a)));
const maxMetric = computed(() => Math.max(1, ...sortedPlaces.value.map(metricValue)));
const topPlaces = computed(() => sortedPlaces.value.slice(0, 6));
const selectedPlace = computed(() => sortedPlaces.value.find(place => place.key === selectedKey.value) ?? null);
const visiblePlaces = computed(() =>
  region.value === 'all' ? sortedPlaces.value : sortedPlaces.value.filter(place => place.region === region.value)
);
const updatedAt = computed(() =>
  data.value?.generatedAt ? dateFormat.value.format(new Date(data.value.generatedAt)) : ''
);

const tabs: readonly { key: Tab; label: string; icon: string }[] = [
  { key: 'map', label: 'developerMap.tabs.map', icon: 'globe' },
  { key: 'submit', label: 'developerMap.tabs.submit', icon: 'package' },
  { key: 'claim', label: 'developerMap.tabs.claim', icon: 'shield-check' },
  { key: 'docs', label: 'developerMap.tabs.docs', icon: 'file-text' }
];

const regions = [
  { key: 'all', label: 'developerMap.map.all' },
  { key: 'americas', label: 'developerMap.map.americas' },
  { key: 'europe', label: 'developerMap.map.europe' },
  { key: 'apac', label: 'developerMap.map.apac' },
  { key: 'other', label: 'developerMap.map.other' }
] as const;

function selectCity(place: DeveloperMapPlace) {
  selectedKey.value = place.key;
}

usePageSeo({
  title: () => t('developerMap.metaTitle'),
  description: () => t('developerMap.description')
});
usePageSchema({ type: 'WebPage' });
</script>

<template>
  <div class="mx-auto box-border flex w-full max-w-1160px flex-col gap-22px pt-10px pb-40px">
    <header class="flex flex-wrap items-end justify-between gap-14px">
      <div class="min-w-0">
        <h1 class="m-0 text-26px font-700 leading-[1.15] tracking-[-0.02em] text-ink-primary">
          {{ t('developerMap.title') }}
        </h1>
        <p class="m-0 mt-7px max-w-720px text-14px leading-[1.7] text-ink-secondary">
          <template v-if="data">
            <b class="font-600 text-ink-primary">{{ number.format(data.totals.apps) }}</b>
            {{ t('developerMap.apps') }} ·
            <b class="font-600 text-ink-primary">{{ number.format(data.totals.developers) }}</b>
            {{ t('developerMap.developers') }}
          </template>
          <template v-else>{{ t('developerMap.lede') }}</template>
        </p>
        <div v-if="data" class="mt-12px flex flex-wrap items-baseline gap-x-18px gap-y-6px text-12px text-ink-tertiary">
          <span
            ><b class="text-14px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.developers) }}</b>
            {{ t('developerMap.stats.developers') }}</span
          >
          <span
            ><b class="text-14px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.located) }}</b>
            {{ t('developerMap.stats.located') }}</span
          >
          <span
            ><b class="text-14px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.places) }}</b>
            {{ t('developerMap.stats.cities') }}</span
          >
          <span
            ><b class="text-14px font-600 tabular-nums text-ink-primary">{{ number.format(data.totals.countries) }}</b>
            {{ t('developerMap.stats.countries') }}</span
          >
        </div>
      </div>
      <div class="flex shrink-0 items-center gap-8px">
        <NuxtLink
          :to="localePath('/developers')"
          class="inline-flex h-30px items-center rounded-default border border-solid border-line-subtle px-12px text-12px font-500 text-ink-secondary no-underline outline-none hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
        >
          {{ t('developerMap.actions.docs') }}
        </NuxtLink>
        <button
          type="button"
          class="inline-flex h-30px items-center gap-5px rounded-default bg-brand-coral px-13px text-12px font-600 text-ink-inverse outline-none hover:opacity-90 focus-visible:shadow-focus-ring"
          @click="tab = 'submit'"
        >
          <OnIcon name="plus" :size="14" />
          {{ t('developerMap.actions.submit') }}
        </button>
      </div>
    </header>

    <nav
      class="flex w-fit max-w-full gap-2px overflow-x-auto rounded-default border border-solid border-line-subtle bg-surface-inset p-3px"
      role="tablist"
      :aria-label="t('developerMap.title')"
    >
      <button
        v-for="item in tabs"
        :key="item.key"
        type="button"
        role="tab"
        :aria-selected="tab === item.key"
        class="inline-flex h-28px flex-none items-center gap-5px rounded-[7px] px-11px text-12px font-500 outline-none transition-colors"
        :class="
          tab === item.key
            ? 'bg-surface-card text-ink-primary shadow-[0_1px_2px_rgba(0,0,0,0.18)]'
            : 'text-ink-secondary hover:text-ink-primary'
        "
        @click="tab = item.key"
      >
        <OnIcon :name="item.icon" :size="13" />
        {{ t(item.label) }}
      </button>
    </nav>

    <div v-if="error" class="py-48px">
      <OnErrorState @retry="refresh()" />
    </div>

    <template v-else-if="tab === 'map'">
      <section class="overflow-hidden rounded-big border border-solid border-line-subtle bg-surface-card">
        <div
          class="flex flex-wrap items-center justify-between gap-8px border-b border-solid border-line-subtle px-12px py-9px"
        >
          <div
            class="flex items-center gap-2px rounded-[8px] bg-surface-inset p-2px"
            role="group"
            :aria-label="
              t('developerMap.map.legend', {
                metric: metric === 'developers' ? t('developerMap.map.developers') : t('developerMap.map.installs')
              })
            "
          >
            <button
              type="button"
              class="h-24px rounded-[6px] px-9px text-11.5px font-500 outline-none"
              :class="
                metric === 'developers'
                  ? 'bg-surface-card text-ink-primary shadow-[0_1px_2px_rgba(0,0,0,0.16)]'
                  : 'text-ink-secondary hover:text-ink-primary'
              "
              @click="metric = 'developers'"
            >
              {{ t('developerMap.map.developers') }}
            </button>
            <button
              type="button"
              class="h-24px rounded-[6px] px-9px text-11.5px font-500 outline-none"
              :class="
                metric === 'installs'
                  ? 'bg-surface-card text-ink-primary shadow-[0_1px_2px_rgba(0,0,0,0.16)]'
                  : 'text-ink-secondary hover:text-ink-primary'
              "
              @click="metric = 'installs'"
            >
              {{ t('developerMap.map.installs') }}
            </button>
          </div>
          <div class="flex items-center gap-1px overflow-x-auto" role="group" :aria-label="t('developerMap.map.all')">
            <button
              v-for="item in regions"
              :key="item.key"
              type="button"
              class="h-24px flex-none rounded-[6px] px-8px text-11.5px font-500 outline-none"
              :class="
                region === item.key ? 'bg-surface-raised text-ink-primary' : 'text-ink-secondary hover:text-ink-primary'
              "
              @click="region = item.key"
            >
              {{ t(item.label) }}
            </button>
          </div>
        </div>

        <div v-if="data" class="relative">
          <DeveloperMapWorld
            :places="data.places"
            :selected="selectedKey"
            :metric="metric"
            :region="region"
            @select="key => (selectedKey = key)"
          />
          <div
            class="pointer-events-none absolute bottom-10px left-12px flex items-center gap-6px text-10.5px text-ink-tertiary"
          >
            <span class="h-5px w-5px rounded-full bg-[var(--dm-dot)]" />
            {{
              t('developerMap.map.legend', {
                metric: metric === 'developers' ? t('developerMap.map.developers') : t('developerMap.map.installs')
              })
            }}
          </div>
          <div class="absolute right-12px bottom-10px text-10.5px text-ink-tertiary">
            {{ t('developerMap.map.updated', { date: updatedAt }) }}
          </div>
        </div>
        <div v-else class="grid min-h-260px place-items-center text-13px text-ink-tertiary">
          {{ t('developerMap.lede') }}
        </div>

        <div class="border-t border-solid border-line-subtle px-12px py-11px">
          <template v-if="selectedPlace">
            <div class="mb-8px flex items-center gap-8px">
              <button
                type="button"
                class="grid h-24px w-24px place-items-center rounded-[6px] text-ink-secondary outline-none hover:bg-surface-raised hover:text-ink-primary focus-visible:shadow-focus-ring"
                :aria-label="t('developerMap.map.cities')"
                @click="selectedKey = null"
              >
                <OnIcon name="lucide:arrow-left" :size="14" />
              </button>
              <b class="text-13px font-600 text-ink-primary">{{ placeName(selectedPlace) }}</b>
              <span class="text-11px text-ink-tertiary">
                {{ selectedPlace.countryCode }} ·
                {{
                  metric === 'installs'
                    ? `${number.format(selectedPlace.installs30d)} ${t('developerMap.map.installs')}`
                    : t('developerMap.map.developersAt', { count: number.format(selectedPlace.developers) })
                }}
                · {{ t('developerMap.map.appsAt', { count: number.format(selectedPlace.apps) }) }}
              </span>
            </div>
            <div class="grid grid-cols-1 gap-6px sm:grid-cols-2">
              <div
                v-for="developer in selectedPlace.topDevelopers"
                :key="developer.name"
                class="flex min-w-0 items-center gap-8px rounded-default border border-solid border-line-subtle px-9px py-7px"
              >
                <span
                  class="grid h-24px w-24px shrink-0 place-items-center rounded-[6px] bg-surface-inset text-10px font-700 text-ink-secondary"
                  >{{ developer.name.slice(0, 1) }}</span
                >
                <span class="min-w-0 flex-1 truncate text-12px text-ink-primary">{{ developer.name }}</span>
                <span class="shrink-0 text-10.5px tabular-nums text-ink-tertiary">{{
                  t('developerMap.map.appsAt', { count: developer.apps })
                }}</span>
              </div>
            </div>
          </template>
          <template v-else>
            <div class="mb-8px flex items-baseline justify-between gap-10px">
              <b class="text-13px font-600 text-ink-primary">{{ t('developerMap.map.topCities') }}</b>
              <span class="text-10.5px text-ink-tertiary">{{ t('developerMap.map.citiesHint') }}</span>
            </div>
            <div class="grid grid-cols-1 gap-x-14px gap-y-1px sm:grid-cols-2 lg:grid-cols-3">
              <button
                v-for="(place, index) in topPlaces"
                :key="place.key"
                type="button"
                class="flex min-w-0 items-center gap-8px rounded-[6px] px-5px py-6px text-left outline-none hover:bg-surface-raised focus-visible:shadow-focus-ring"
                @click="selectCity(place)"
              >
                <span class="w-16px shrink-0 text-10.5px tabular-nums text-ink-tertiary">{{
                  String(index + 1).padStart(2, '0')
                }}</span>
                <span class="min-w-0 flex-1">
                  <span class="block truncate text-12px font-500 text-ink-primary">{{ placeName(place) }}</span>
                  <span class="block truncate text-10.5px text-ink-tertiary"
                    >{{ countryName(place.countryCode) }} ·
                    {{ t('developerMap.map.appsAt', { count: place.apps }) }}</span
                  >
                </span>
                <span class="shrink-0 text-12px font-600 tabular-nums text-ink-primary">{{
                  number.format(metricValue(place))
                }}</span>
              </button>
            </div>
          </template>
        </div>
      </section>

      <section aria-labelledby="developer-map-cities">
        <div class="mb-10px flex items-baseline justify-between gap-12px">
          <div>
            <h2 id="developer-map-cities" class="m-0 text-16px font-600 text-ink-primary">
              {{ t('developerMap.map.cities') }}
            </h2>
            <p class="m-0 mt-2px text-11.5px text-ink-tertiary">
              {{ t('developerMap.map.citiesHint') }} · {{ number.format(visiblePlaces.length) }}
            </p>
          </div>
        </div>
        <div class="grid grid-cols-[repeat(auto-fill,minmax(220px,1fr))] gap-9px">
          <button
            v-for="(place, index) in visiblePlaces"
            :key="place.key"
            type="button"
            class="group relative flex min-w-0 flex-col gap-7px overflow-hidden rounded-big border border-solid border-line-subtle bg-surface-card px-12px py-10px text-left outline-none hover:bg-surface-raised focus-visible:shadow-focus-ring"
            @click="selectCity(place)"
          >
            <span class="flex items-baseline justify-between gap-8px">
              <span class="text-10.5px tabular-nums text-ink-tertiary">{{ String(index + 1).padStart(2, '0') }}</span>
              <span class="text-12px font-600 tabular-nums text-ink-primary">{{
                number.format(metricValue(place))
              }}</span>
            </span>
            <span class="min-w-0">
              <span class="block truncate text-13px font-600 text-ink-primary">{{ placeName(place) }}</span>
              <span class="block truncate text-10.5px text-ink-tertiary"
                >{{ countryName(place.countryCode) }} · {{ t('developerMap.map.appsAt', { count: place.apps }) }}</span
              >
            </span>
            <span class="mt-2px block h-2px rounded-full bg-surface-inset">
              <span
                class="block h-2px rounded-full bg-[var(--dm-dot,#ff7356)]"
                :style="{ width: `${Math.max(4, (metricValue(place) / maxMetric) * 100)}%` }"
              />
            </span>
          </button>
        </div>
      </section>
    </template>

    <section v-else-if="tab === 'docs'" class="flex flex-col gap-16px">
      <div>
        <h2 class="m-0 text-20px font-600 text-ink-primary">{{ t('developerMap.docs.title') }}</h2>
        <p class="m-0 mt-5px max-w-760px text-13.5px leading-[1.7] text-ink-secondary">
          {{ t('developerMap.docs.body') }}
        </p>
      </div>
      <div class="grid grid-cols-1 gap-10px md:grid-cols-3">
        <article class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
          <span class="grid h-30px w-30px place-items-center rounded-[8px] bg-surface-inset text-ink-secondary"
            ><OnIcon name="square-terminal" :size="15"
          /></span>
          <h3 class="m-0 mt-10px text-14px font-600 text-ink-primary">{{ t('developerMap.docs.apiTitle') }}</h3>
          <p class="m-0 mt-4px text-12px leading-[1.65] text-ink-secondary">{{ t('developerMap.docs.apiBody') }}</p>
          <NuxtLink
            :to="{ path: localePath('/developers'), hash: '#api' }"
            class="mt-9px inline-flex items-center gap-4px text-11.5px font-500 text-ink-primary no-underline hover:underline"
            >{{ t('developerMap.docs.apiLink') }} <OnIcon name="lucide:arrow-up-right" :size="12"
          /></NuxtLink>
        </article>
        <article class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
          <span class="grid h-30px w-30px place-items-center rounded-[8px] bg-surface-inset text-ink-secondary"
            ><OnIcon name="globe" :size="15"
          /></span>
          <h3 class="m-0 mt-10px text-14px font-600 text-ink-primary">{{ t('developerMap.docs.mcpTitle') }}</h3>
          <p class="m-0 mt-4px text-12px leading-[1.65] text-ink-secondary">{{ t('developerMap.docs.mcpBody') }}</p>
          <NuxtLink
            :to="{ path: localePath('/developers'), hash: '#mcp' }"
            class="mt-9px inline-flex items-center gap-4px text-11.5px font-500 text-ink-primary no-underline hover:underline"
            >{{ t('developerMap.docs.mcpLink') }} <OnIcon name="lucide:arrow-up-right" :size="12"
          /></NuxtLink>
        </article>
        <article class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
          <span class="grid h-30px w-30px place-items-center rounded-[8px] bg-surface-inset text-ink-secondary"
            ><OnIcon name="file-text" :size="15"
          /></span>
          <h3 class="m-0 mt-10px text-14px font-600 text-ink-primary">{{ t('developerMap.docs.dataTitle') }}</h3>
          <p class="m-0 mt-4px text-12px leading-[1.65] text-ink-secondary">{{ t('developerMap.docs.dataBody') }}</p>
          <NuxtLink
            :to="{ path: localePath('/developers'), hash: '#terms' }"
            class="mt-9px inline-flex items-center gap-4px text-11.5px font-500 text-ink-primary no-underline hover:underline"
            >{{ t('developerMap.docs.dataLink') }} <OnIcon name="lucide:arrow-up-right" :size="12"
          /></NuxtLink>
        </article>
      </div>
      <div>
        <h3 class="m-0 text-15px font-600 text-ink-primary">{{ t('developerMap.docs.modesTitle') }}</h3>
        <div class="mt-9px grid grid-cols-1 gap-10px md:grid-cols-2">
          <article class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
            <h4 class="m-0 mt-7px text-13px font-600 text-ink-primary">{{ t('developerMap.docs.modeLink') }}</h4>
            <p class="m-0 mt-3px text-11.5px leading-[1.65] text-ink-secondary">
              {{ t('developerMap.docs.modeLinkBody') }}
            </p>
          </article>
          <article class="rounded-big border border-solid border-line-subtle bg-surface-card p-14px">
            <span
              class="rounded-[4px] bg-surface-inset px-6px py-2px text-9.5px font-700 uppercase tracking-[0.08em] text-ink-secondary"
              >Cask</span
            >
            <h4 class="m-0 mt-7px text-13px font-600 text-ink-primary">{{ t('developerMap.docs.modeCask') }}</h4>
            <p class="m-0 mt-3px text-11.5px leading-[1.65] text-ink-secondary">
              {{ t('developerMap.docs.modeCaskBody') }}
            </p>
          </article>
        </div>
      </div>
      <p
        class="m-0 rounded-big border border-solid border-line-subtle bg-surface-card px-13px py-10px text-11.5px leading-[1.65] text-ink-secondary"
      >
        {{ t('developerMap.docs.status') }}
      </p>
    </section>

    <template v-else-if="tab === 'submit'">
      <DeveloperSubmitForm />
      <DeveloperStatusPanel :initial-token="String(route.query.submission ?? '')" />
    </template>
    <DeveloperClaimForm v-else />
  </div>
</template>
