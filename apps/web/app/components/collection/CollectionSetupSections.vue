<script setup lang="ts">
import { formatCount } from '@opennavo/shared';
import { displayUrl, OnButton, OnIcon } from '@opennavo/ui';
import {
  compatibilityRows,
  externalEntries,
  formatTotal,
  installableEntries,
  pricePeriod,
  regionName
} from '~/utils/collectionSetup';
import type { CollectionDetail } from '~/utils/collectionSetup';

// Collection setup (F4) below the list: what "Install all with OpenNavo" installs (exactly the Brewfile's
// casks), the link apps to get from their developers instead, price totals as the API groups them (by
// region, currency and billing period; subscriptions never added to one-time prices) and each app's
// sourced macOS compatibility, "unknown" where no source says.
const props = defineProps<{ detail: CollectionDetail; brewfileHref: string }>();
const emit = defineEmits<{ install: [] }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const localePath = useLocalePath();

const installable = computed(() => installableEntries(props.detail));
const external = computed(() => externalEntries(props.detail));
const setup = computed(() => props.detail.setup);
const count = (value: number) => formatCount(value, { locale: appLocale.value });

const totals = computed(() =>
  (setup.value?.priceTotals ?? []).map(total => ({
    key: `${total.region}/${total.currency}/${total.period}`,
    region: regionName(total.region, appLocale.value),
    period: t(`collections.setup.periods.${pricePeriod(total.period)}`),
    amount: formatTotal(total, appLocale.value),
    covered: t('collections.setup.priceCovered', { count: count(total.coveredApps) }, { plural: total.coveredApps })
  }))
);
const unknownPrices = computed(() => setup.value?.priceUnknownCount ?? 0);

const rows = computed(() =>
  // The bound templates keep their {version} placeholder for compatibilityRows to fill per app.
  compatibilityRows(props.detail, {
    minimum: t('package.facts.minimum', { version: '{version}' }),
    maximum: t('package.facts.maximum', { version: '{version}' })
  })
);
const native = computed(() => rows.value.filter(row => row.appleSilicon === 'yes').length);
</script>

<template>
  <div v-if="setup" class="flex flex-col gap-32px">
    <section
      v-if="installable.length || external.length"
      class="flex flex-col gap-10px"
      aria-labelledby="collection-install"
    >
      <h2 id="collection-install" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('collections.setup.installTitle') }}
      </h2>
      <div v-if="installable.length" class="flex flex-col gap-8px">
        <h3 class="m-0 text-13.5px font-600 text-ink-secondary">
          {{ t('collections.setup.installable', { count: count(installable.length) }, { plural: installable.length }) }}
        </h3>
        <ul class="m-0 flex list-none flex-wrap gap-x-14px gap-y-6px p-0 text-13.5px">
          <li v-for="entry in installable" :key="entry.token">
            <NuxtLink
              :to="localePath(entry.path)"
              class="text-ink-secondary no-underline underline-offset-3 hover:text-ink-primary hover:underline"
              >{{ entry.name }}</NuxtLink
            >
          </li>
        </ul>
        <div class="flex flex-wrap items-center gap-8px pt-4px">
          <OnButton variant="primary" shape="round" icon="download" @click="emit('install')">{{
            t('collections.setup.installAll')
          }}</OnButton>
          <OnButton shape="round" :href="brewfileHref" download="Brewfile">
            <OnIcon name="file-text" :size="15" />
            {{ t('collections.export') }}
          </OnButton>
        </div>
        <p class="m-0 text-12.5px leading-[1.6] text-ink-tertiary">
          {{ t('collections.setup.installHint', { count: count(installable.length) }, { plural: installable.length }) }}
        </p>
      </div>
      <div v-if="external.length" class="flex flex-col gap-8px">
        <h3 class="m-0 text-13.5px font-600 text-ink-secondary">
          {{ t('collections.setup.external', { count: count(external.length) }, { plural: external.length }) }}
        </h3>
        <p class="m-0 text-12.5px leading-[1.6] text-ink-tertiary">{{ t('collections.setup.externalHint') }}</p>
        <ul class="m-0 flex list-none flex-col gap-6px p-0 text-13.5px">
          <li v-for="entry in external" :key="entry.token" class="flex flex-wrap items-center gap-x-10px gap-y-2px">
            <NuxtLink
              :to="localePath(entry.path)"
              class="font-600 text-ink-primary no-underline underline-offset-3 hover:underline"
              >{{ entry.name }}</NuxtLink
            >
            <a
              :href="entry.url"
              target="_blank"
              rel="noopener noreferrer"
              class="inline-flex items-center gap-2px text-12.5px text-ink-secondary no-underline hover:text-ink-primary hover:underline"
              >{{ displayUrl(entry.url) }}<OnIcon name="arrow-up-right" :size="12"
            /></a>
          </li>
        </ul>
      </div>
    </section>

    <section class="flex flex-col gap-10px" aria-labelledby="collection-prices">
      <h2 id="collection-prices" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('collections.setup.priceTitle') }}
      </h2>
      <p class="m-0 max-w-720px text-12.5px leading-[1.6] text-ink-tertiary">{{ t('collections.setup.priceHint') }}</p>
      <div v-if="totals.length" class="overflow-x-auto">
        <table class="collection-table w-full border-collapse text-left text-13px">
          <thead>
            <tr class="text-12px text-ink-tertiary">
              <th scope="col">{{ t('collections.setup.region') }}</th>
              <th scope="col">{{ t('collections.setup.period') }}</th>
              <th scope="col">{{ t('collections.setup.total') }}</th>
              <th scope="col">{{ t('collections.setup.covered') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in totals" :key="row.key" class="text-ink-secondary">
              <td>{{ row.region }}</td>
              <td>{{ row.period }}</td>
              <td class="font-600 text-ink-primary tabular-nums">{{ row.amount }}</td>
              <td class="tabular-nums">{{ row.covered }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else class="m-0 text-13px text-ink-secondary">{{ t('collections.setup.priceNone') }}</p>
      <p v-if="totals.length && unknownPrices" class="m-0 text-12.5px text-ink-tertiary">
        {{ t('collections.setup.priceUnknown', { count: count(unknownPrices) }, { plural: unknownPrices }) }}
      </p>
    </section>

    <section v-if="rows.length" class="flex flex-col gap-10px" aria-labelledby="collection-compatibility">
      <h2 id="collection-compatibility" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('collections.setup.compatibilityTitle') }}
      </h2>
      <p class="m-0 max-w-720px text-12.5px leading-[1.6] text-ink-tertiary">
        {{
          t(
            'collections.setup.compatibilitySummary',
            { native: count(native), count: count(rows.length) },
            { plural: rows.length }
          )
        }}
      </p>
      <div class="overflow-x-auto">
        <table class="collection-table w-full border-collapse text-left text-13px">
          <thead>
            <tr class="text-12px text-ink-tertiary">
              <th scope="col">{{ t('collections.setup.app') }}</th>
              <th scope="col">{{ t('collections.setup.macos') }}</th>
              <th scope="col">{{ t('collections.setup.appleSilicon') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.token" class="text-ink-secondary">
              <th scope="row" class="font-500">
                <NuxtLink
                  :to="localePath(row.path)"
                  class="text-ink-primary no-underline underline-offset-3 hover:underline"
                  >{{ row.name }}</NuxtLink
                >
              </th>
              <td>
                <span v-for="line in row.macos" :key="line" class="block">{{ line }}</span>
                <span v-if="!row.macos.length" class="text-ink-tertiary">{{ t('collections.setup.unknown') }}</span>
              </td>
              <td :class="row.appleSilicon === 'unknown' ? 'text-ink-tertiary' : ''">
                {{ t(`collections.setup.silicon.${row.appleSilicon}`) }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<style>
.collection-table th,
.collection-table td {
  padding: 8px 16px 8px 0;
  border-bottom: 1px solid var(--on-border-subtle);
  vertical-align: top;
}

.collection-table thead th {
  font-weight: 500;
}
</style>
