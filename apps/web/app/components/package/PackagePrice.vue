<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import { displayUrl, OnChip } from '@opennavo/ui';
import {
  aroundDate,
  isoDate,
  priceAmount,
  priceEdition,
  priceUsage,
  priceRegionName,
  shownPrices
} from '~/utils/packageFacts';
import type { PackagePage, PriceFact } from '~/utils/packageFacts';

// Price and purchase options (F2): every price fact OpenNavo checked, by region, edition and billing period,
// with its source and check date. Stale prices keep their date and lose their claim to be current; prices
// under review are not shown. A free download is never presented as free to use, and nothing is called a
// lowest price: OpenNavo keeps no price history. JSON-LD offers come only from the API's offer-ready subset.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const rows = computed(() =>
  shownPrices(props.pkg).map((price: PriceFact, index) => ({
    key: `${index}:${price.channel}:${price.region}:${price.edition}:${price.period}`,
    edition: priceEdition(price),
    usage: priceUsage(price, t),
    amount: priceAmount(price, t, appLocale.value),
    region: priceRegionName(price.region, appLocale.value),
    channel: t(`package.price.channels.${price.channel}`),
    url: price.purchaseUrl,
    stale: price.evidence.status === 'stale',
    // An App Store price says what the download costs, not what using the app costs.
    storeNote: price.channel === 'app_store' && price.usage === 'unknown',
    checked: price.evidence.checkedAt
      ? {
          iso: isoDate(price.evidence.checkedAt),
          date: formatDate(price.evidence.checkedAt, { locale: appLocale.value }),
          // A stale price is only true as of its date.
          ...(price.evidence.status === 'stale' ? aroundDate(t, 'package.facts.asOf') : { before: '', after: '' })
        }
      : null
  }))
);
</script>

<template>
  <section
    v-if="rows.length"
    id="price"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="price-title"
    data-testid="package-price"
  >
    <h2 id="price-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.price.title', { name: pkg.displayName }) }}
    </h2>
    <p class="m-0 mt-4px text-12px text-ink-tertiary">{{ t('package.price.hint') }}</p>
    <div class="mt-10px overflow-x-auto">
      <table class="w-full border-collapse text-left text-12.5px leading-[1.5]">
        <thead>
          <tr class="text-11.5px text-ink-tertiary">
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.price.edition') }}</th>
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.facts.price') }}</th>
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.price.region') }}</th>
            <th scope="col" class="py-6px pr-12px font-500">{{ t('package.price.where') }}</th>
            <th scope="col" class="py-6px font-500">{{ t('package.price.checked') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.key" class="border-t border-t-solid border-line-subtle align-top">
            <td class="py-8px pr-12px text-ink-primary">{{ row.edition || '—' }}</td>
            <td class="py-8px pr-12px text-ink-primary">
              {{ [row.usage, row.amount].filter(Boolean).join(' · ') || '—' }}
              <span v-if="row.storeNote" class="mt-2px block text-11.5px text-ink-tertiary">{{
                t('package.price.storeNote')
              }}</span>
            </td>
            <td class="py-8px pr-12px text-ink-secondary">{{ row.region || '—' }}</td>
            <td class="py-8px pr-12px">
              <a
                :href="row.url"
                target="_blank"
                rel="nofollow noopener noreferrer"
                class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
                >{{ row.channel }}</a
              >
              <span class="block text-11.5px text-ink-tertiary [overflow-wrap:anywhere]">{{
                displayUrl(row.url)
              }}</span>
            </td>
            <td class="py-8px text-ink-tertiary">
              <template v-if="row.checked"
                >{{ row.checked.before }}<time :datetime="row.checked.iso">{{ row.checked.date }}</time
                >{{ row.checked.after }}</template
              >
              <OnChip v-if="row.stale" class="mt-4px" tone="warning">{{ t('package.facts.stale') }}</OnChip>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
