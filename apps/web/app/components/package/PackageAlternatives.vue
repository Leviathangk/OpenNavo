<script setup lang="ts">
import { formatDate, LOCALE_METADATA } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import { displayUrl, OnAppIcon, OnChip } from '@opennavo/ui';
import { verifiedAlternatives, isoDate, isVerified, priceAmount, priceUsage } from '~/utils/packageFacts';
import { alternativesPageOpen } from '~/utils/alternativesPage';
import type { AlternativeFact, PackagePage } from '~/utils/packageFacts';
import { packageLink, packagePath } from '~/utils/packageView';

// Alternatives to {name} (F3, from facts.alternatives): the editors' confirmed alternatives in their order.
// Reasons and limits are only the ones the API backs with verified facts; the comparison lists verified
// attributes only (price, open source, offline use, Apple silicon, import and export formats), with a dash
// where nothing is verified; and a migration guide appears when one is verified for this pair of versions.
// Where the server opens the "Alternatives to {name}" page (X2), the block links to it as the one target page.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const localePath = useLocalePath();
const appLocale = computed(() => toAppLocale(locale.value));
const items = computed(() => verifiedAlternatives(props.pkg));

type AttributeName = 'open_source' | 'offline' | 'apple_silicon' | 'import_formats' | 'export_formats';
const ATTRIBUTES: { name: AttributeName; label: string }[] = [
  { name: 'open_source', label: 'package.alternatives.reasons.open_source' },
  { name: 'offline', label: 'package.alternatives.reasons.offline' },
  { name: 'apple_silicon', label: 'package.alternatives.reasons.apple_silicon' },
  { name: 'import_formats', label: 'package.compare.imports' },
  { name: 'export_formats', label: 'package.compare.exports' }
];

function attributeValue(item: AlternativeFact, name: AttributeName): string {
  const attribute = (item.attributes ?? []).find(entry => entry.name === name);
  if (!attribute) return '';
  if (attribute.formats?.length) return attribute.formats.join(', ');
  if (attribute.booleanValue === undefined) return '';
  return t(attribute.booleanValue ? 'package.facts.yes' : 'package.facts.no');
}

function priceValue(item: AlternativeFact): string {
  const prices = item.prices ?? [];
  const usages = [...new Set(prices.map(price => priceUsage(price, t)).filter(Boolean))];
  const amount = prices.map(price => priceAmount(price, t, appLocale.value)).find(Boolean);
  return [usages.join(' / '), amount].filter(Boolean).join(' · ');
}

// Rows with at least one verified value; the rest would only be dashes.
const comparison = computed(() => {
  const list = items.value;
  const rows = [
    { key: 'price', label: t('package.facts.price'), values: list.map(priceValue) },
    ...ATTRIBUTES.map(attribute => ({
      key: attribute.name,
      label: t(attribute.label),
      values: list.map(item => attributeValue(item, attribute.name))
    }))
  ];
  return rows.filter(row => row.values.some(Boolean));
});

const migrations = computed(() =>
  items.value.flatMap(item => {
    const guide = item.migration;
    if (!guide || !isVerified(guide.evidence)) return [];
    const steps = guide.steps.filter(step => isVerified(step.evidence));
    if (!steps.length) return [];
    return [
      {
        key: item.package.token,
        target: item.package.displayName,
        // An English guide on another language's page keeps its language.
        lang: guide.locale === appLocale.value ? undefined : LOCALE_METADATA[guide.locale as SiteLocale]?.hreflang,
        steps: steps.map((step, index) => ({
          key: index,
          label: t('package.facts.labelled', { label: t(`package.compare.actions.${step.action}`) }),
          // The label's colon is in the page language; Chinese and Japanese put no space after it.
          text: `${['zh-CN', 'zh-Hant', 'ja-JP'].includes(appLocale.value) ? '' : ' '}${step.text}`,
          url: step.evidence.sourceUrl,
          checked: step.evidence.checkedAt
            ? {
                iso: isoDate(step.evidence.checkedAt),
                date: formatDate(step.evidence.checkedAt, { locale: appLocale.value })
              }
            : null
        }))
      }
    ];
  })
);

const pageHref = computed(() =>
  alternativesPageOpen(props.pkg) ? localePath(`${packagePath(props.pkg.token)}/alternatives`) : undefined
);

const href = (item: AlternativeFact) =>
  packageLink(item.package.kind, item.package.token) ? localePath(packagePath(item.package.token)) : undefined;
</script>

<template>
  <section
    v-if="items.length"
    id="alternatives"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="alternatives-title"
    data-testid="package-alternatives"
  >
    <h2 id="alternatives-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.alternatives.titleFor', { name: pkg.displayName }) }}
    </h2>
    <p class="m-0 mt-4px text-12px text-ink-tertiary">{{ t('package.alternatives.hint') }}</p>
    <ul class="m-0 mt-8px list-none p-0">
      <li
        v-for="item in items"
        :key="item.package.token"
        class="relative flex gap-12px border-t border-t-solid border-line-subtle py-10px first:border-t-0"
      >
        <OnAppIcon
          :kind="item.package.kind"
          :token="item.package.token"
          :name="item.package.displayName"
          :src="item.package.iconUrl"
          :accent="item.package.accentColor"
          :size="40"
        />
        <div class="min-w-0 flex-1">
          <h3 class="m-0 text-13px font-600">
            <NuxtLink v-if="href(item)" :to="href(item)" class="text-ink-primary no-underline hover:underline">{{
              item.package.displayName
            }}</NuxtLink>
            <template v-else>{{ item.package.displayName }}</template>
          </h3>
          <p v-if="item.package.summary" class="m-0 text-12px text-ink-tertiary">{{ item.package.summary }}</p>
          <div v-if="item.reasons.length || item.limits.length" class="mt-6px flex flex-wrap gap-6px">
            <OnChip v-for="reason in item.reasons" :key="reason" tone="accent">{{
              t(`package.alternatives.reasons.${reason}`)
            }}</OnChip>
            <OnChip v-for="limit in item.limits" :key="limit" tone="neutral" outline>{{
              t(`package.alternatives.limits.${limit}`)
            }}</OnChip>
          </div>
          <p v-if="item.bestFor" class="m-0 mt-4px text-12px text-ink-secondary">
            {{ t('package.alternatives.bestFor', { audience: t(`package.alternatives.audience.${item.bestFor}`) }) }}
          </p>
        </div>
      </li>
    </ul>
    <div v-if="comparison.length" class="mt-12px" data-testid="package-compare">
      <h3 class="m-0 text-12.5px font-600 text-ink-primary">{{ t('package.compare.title') }}</h3>
      <div class="mt-6px overflow-x-auto">
        <table class="w-full border-collapse text-left text-12.5px leading-[1.5]">
          <thead>
            <tr class="text-11.5px text-ink-tertiary">
              <td class="py-6px pr-12px"></td>
              <th v-for="item in items" :key="item.package.token" scope="col" class="py-6px pr-12px font-500">
                {{ item.package.displayName }}
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in comparison" :key="row.key" class="border-t border-t-solid border-line-subtle align-top">
              <th scope="row" class="py-7px pr-12px font-400 text-ink-tertiary">{{ row.label }}</th>
              <td v-for="(value, index) in row.values" :key="index" class="py-7px pr-12px text-ink-primary">
                {{ value || '—' }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <p class="m-0 mt-6px text-11.5px text-ink-tertiary">{{ t('package.compare.unverified') }}</p>
    </div>
    <div v-for="guide in migrations" :key="guide.key" class="mt-12px" data-testid="package-migration">
      <h3 class="m-0 text-12.5px font-600 text-ink-primary">
        {{ t('package.compare.migrationTitle', { from: pkg.displayName, to: guide.target }) }}
      </h3>
      <ol
        class="m-0 mt-6px flex flex-col gap-6px pl-18px text-13px leading-[1.6] text-ink-secondary"
        :lang="guide.lang"
      >
        <li v-for="step in guide.steps" :key="step.key">
          <strong class="font-600 text-ink-primary">{{ step.label }}</strong
          >{{ step.text }}
          <span class="block text-11.5px text-ink-tertiary"
            ><a
              :href="step.url"
              target="_blank"
              rel="nofollow noopener noreferrer"
              class="text-ink-secondary underline-offset-2 hover:text-ink-primary"
              >{{ displayUrl(step.url) }}</a
            ><template v-if="step.checked">
              · <time :datetime="step.checked.iso">{{ step.checked.date }}</time></template
            ></span
          >
        </li>
      </ol>
      <p class="m-0 mt-6px text-12px text-ink-tertiary">
        {{ t('package.compare.migrationNote', { name: pkg.displayName }) }}
      </p>
    </div>
    <NuxtLink
      v-if="pageHref"
      :to="pageHref"
      class="mt-10px inline-flex text-12.5px font-600 text-ink-primary no-underline hover:underline"
    >
      {{ t('package.compare.pageLink', { name: pkg.displayName }) }}
    </NuxtLink>
  </section>
</template>
