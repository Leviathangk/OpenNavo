<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import { displayUrl, OnAppIcon, OnBreadcrumb, OnChip, OnErrorState } from '@opennavo/ui';
import {
  alternativesOf,
  alternativesPageOpen,
  attributeValue,
  comparisonAttributes,
  priceText
} from '~/utils/alternativesPage';
import type { ComparisonValue } from '~/utils/alternativesPage';
import { httpsUrl } from '~/utils/linkApp';
import { packagePath } from '~/utils/packageView';
import { appEntityId } from '~/utils/structuredData';

// "Alternatives to {app}" (X2 with F3's comparison facts): an intent page of its own at
// /apps/{token}/alternatives, outside the app's tab layout so it has its own h1, breadcrumb and decision. The
// file sits outside pages/apps/[token]/ for that reason (a file there would be a tab of [token].vue); its
// path comes from definePageMeta.
// It opens only where the server's decision says so (indexing.pages.alternatives: indexable with its own
// canonical, after intentPagesEnabled, a confirmed search target and at least three reasoned alternatives);
// anywhere else it is a 404, so each app × intent × language has exactly one target page. The comparison
// lists only verified attributes with their sources; nothing missing is guessed.
definePageMeta({ path: '/apps/:token/alternatives', name: 'apps-token-alternatives' });

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const localePath = useLocalePath();
const NuxtLink = resolveComponent('NuxtLink');

const notFound = () => createError({ statusCode: 404, statusMessage: 'Page not found', fatal: true });
const { data: pkg, error, refresh, removed } = await usePackage('cask');
if (removed.value || (pkg.value && !alternativesPageOpen(pkg.value))) throw notFound();
watch(pkg, value => {
  if (value && !alternativesPageOpen(value)) showError(notFound());
});

// S5: the server's decision for this page: robots, hreflang (languages where it is open too), canonical.
usePageDecision(() => pkg.value?.indexing?.pages?.alternatives);

const name = computed(() => pkg.value?.displayName ?? '');
const overviewPath = computed(() => localePath(packagePath(pkg.value?.token ?? '')));
const alternatives = computed(() => (pkg.value ? alternativesOf(pkg.value) : []));
const attributes = computed(() => comparisonAttributes(alternatives.value));
const date = (iso: string | null) => (iso ? formatDate(iso, { locale: appLocale.value }) : null);

function valueText(value: ComparisonValue): string {
  if (value.kind === 'formats') return value.formats.join(', ');
  return t(`package.alternatives.page.${value.kind}`);
}

const cards = computed(() =>
  alternatives.value.map(item => ({
    token: item.package.token,
    pkg: item.package,
    path: localePath(packagePath(item.package.token)),
    reasons: item.reasons,
    limits: item.limits,
    bestFor: item.bestFor,
    facts: attributes.value.flatMap(attribute => {
      const { value, fact } = attributeValue(item, attribute);
      return fact
        ? [
            {
              attribute,
              text: valueText(value),
              source: httpsUrl(fact.evidence.sourceUrl),
              checked: date(fact.evidence.checkedAt)
            }
          ]
        : [];
    }),
    prices: (item.prices ?? []).flatMap(price => {
      const text = priceText(price, appLocale.value, t);
      return text
        ? [
            {
              key: `${price.region}/${price.edition}/${price.period}/${price.channel}`,
              text,
              region: price.region,
              source: httpsUrl(price.evidence.sourceUrl),
              checked: date(price.evidence.checkedAt)
            }
          ]
        : [];
    }),
    migration: (item.migration?.steps ?? []).map((step, index) => ({
      key: index,
      action: t(`package.alternatives.page.actions.${step.action}`),
      text: step.text,
      source: httpsUrl(step.evidence.sourceUrl)
    }))
  }))
);

const breadcrumb = computed(() => {
  const category = pkg.value?.primaryCategory;
  return [
    { label: t('nav.discover'), href: localePath('/discover') },
    ...(category ? [{ label: category.name, href: localePath(`/categories/${category.slug}`) }] : []),
    { label: name.value, href: overviewPath.value },
    { label: t('package.alternatives.page.crumb') }
  ];
});
useBreadcrumbSchema(breadcrumb);
// CollectionPage → ItemList of the alternatives, about the app itself (its entity lives on the overview).
useItemListSchema(() => cards.value.map(card => ({ name: card.pkg.displayName, path: card.path })));
const siteUrl = useRuntimeConfig().public.siteUrl.replace(/\/$/, '');
usePageSchema(() => (pkg.value ? { about: appEntityId(siteUrl, pkg.value.token) } : {}));

const title = computed(() => t('package.alternatives.page.title', { name: name.value }));
const description = computed(() =>
  t(
    'package.alternatives.page.description',
    { name: name.value, count: alternatives.value.length },
    { plural: alternatives.value.length }
  )
);
usePageSeo({ title: () => t('package.alternatives.page.metaTitle', { name: name.value }), description });
if (pkg.value)
  defineOgImage('OgListSatori', {
    locale: appLocale.value,
    label: t('package.alternatives.page.crumb'),
    title: title.value,
    description: description.value,
    icons: ogAppIcons(alternatives.value.map(item => item.package)),
    ...useOgFrame()
  });
</script>

<template>
  <OnErrorState v-if="error && !pkg" class="py-64px" @retry="refresh()" />
  <article v-else-if="pkg" class="box-border flex flex-col gap-24px pt-6px" aria-labelledby="alternatives-title">
    <OnBreadcrumb :items="breadcrumb" :link-as="NuxtLink" />
    <header class="flex flex-col gap-12px">
      <h1 id="alternatives-title" class="m-0 text-title1 text-ink-primary">{{ title }}</h1>
      <p class="m-0 max-w-760px text-14px leading-[1.7] text-ink-secondary">
        {{ t('package.alternatives.page.intro', { name }) }}
      </p>
      <NuxtLink
        :to="overviewPath"
        class="flex max-w-560px items-center gap-12px rounded-big border border-solid border-line-subtle bg-surface-card px-14px py-10px text-ink-primary no-underline hover:border-line-strong"
      >
        <OnAppIcon
          :kind="pkg.kind"
          :token="pkg.token"
          :name="pkg.displayName"
          :src="pkg.iconUrl"
          :variants="pkg.iconVariants"
          :accent="pkg.accentColor"
          :size="40"
        />
        <span class="min-w-0">
          <span class="block text-12px text-ink-tertiary">{{ t('package.alternatives.page.current') }}</span>
          <span class="block truncate text-14px font-600">{{ pkg.displayName }}</span>
          <span v-if="pkg.summary" class="block truncate text-12.5px text-ink-secondary">{{ pkg.summary }}</span>
        </span>
      </NuxtLink>
    </header>

    <section v-if="attributes.length" class="flex flex-col gap-10px" aria-labelledby="alternatives-compare">
      <h2 id="alternatives-compare" class="m-0 text-17px font-600 text-ink-primary">
        {{ t('package.alternatives.page.compare') }}
      </h2>
      <div class="overflow-x-auto">
        <table class="alternatives-table w-full border-collapse text-left text-13px">
          <thead>
            <tr class="text-12px text-ink-tertiary">
              <th scope="col">{{ t('package.alternatives.page.attribute') }}</th>
              <th v-for="card in cards" :key="card.token" scope="col">{{ card.pkg.displayName }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="attribute in attributes" :key="attribute" class="text-ink-secondary">
              <th scope="row" class="font-500 text-ink-primary">
                {{ t(`package.alternatives.page.attributes.${attribute}`) }}
              </th>
              <td
                v-for="card in cards"
                :key="card.token"
                :class="card.facts.some(fact => fact.attribute === attribute) ? '' : 'text-ink-tertiary'"
              >
                {{
                  card.facts.find(fact => fact.attribute === attribute)?.text ?? t('package.alternatives.page.unknown')
                }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <ol class="m-0 flex list-none flex-col gap-12px p-0">
      <li v-for="card in cards" :id="`alt-${card.token}`" :key="card.token">
        <section
          class="flex flex-col gap-10px rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
          :aria-labelledby="`alt-title-${card.token}`"
        >
          <div class="flex items-start gap-12px">
            <OnAppIcon
              :kind="card.pkg.kind"
              :token="card.pkg.token"
              :name="card.pkg.displayName"
              :src="card.pkg.iconUrl"
              :variants="card.pkg.iconVariants"
              :accent="card.pkg.accentColor"
              :size="44"
            />
            <div class="min-w-0 flex-1">
              <h2 :id="`alt-title-${card.token}`" class="m-0 text-16px font-600">
                <NuxtLink :to="card.path" class="text-ink-primary no-underline underline-offset-3 hover:underline">{{
                  card.pkg.displayName
                }}</NuxtLink>
              </h2>
              <p v-if="card.pkg.summary" class="m-0 mt-2px text-13px text-ink-secondary">{{ card.pkg.summary }}</p>
            </div>
            <GetMenu :kind="card.pkg.kind" :token="card.pkg.token" :name="card.pkg.displayName" :link="card.pkg.link" />
          </div>
          <div v-if="card.reasons.length || card.limits.length" class="flex flex-wrap gap-6px">
            <OnChip v-for="reason in card.reasons" :key="reason" tone="accent">{{
              t(`package.alternatives.reasons.${reason}`)
            }}</OnChip>
            <OnChip v-for="limit in card.limits" :key="limit" tone="neutral" outline>{{
              t(`package.alternatives.limits.${limit}`)
            }}</OnChip>
          </div>
          <p v-if="card.bestFor" class="m-0 text-12.5px text-ink-secondary">
            {{ t('package.alternatives.bestFor', { audience: t(`package.alternatives.audience.${card.bestFor}`) }) }}
          </p>
          <dl v-if="card.facts.length || card.prices.length" class="m-0 grid gap-8px text-13px sm:grid-cols-2">
            <div v-for="fact in card.facts" :key="fact.attribute">
              <dt class="text-12px text-ink-tertiary">
                {{ t(`package.alternatives.page.attributes.${fact.attribute}`) }}
              </dt>
              <dd class="m-0 text-ink-primary">
                {{ fact.text }}
                <span class="flex flex-wrap items-center gap-6px text-12px text-ink-tertiary">
                  <a
                    v-if="fact.source"
                    :href="fact.source"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-ink-secondary"
                    >{{ displayUrl(fact.source) }}</a
                  >
                  <span v-if="fact.checked">{{ t('package.facts.checkedOn', { date: fact.checked }) }}</span>
                </span>
              </dd>
            </div>
            <div v-for="price in card.prices" :key="price.key">
              <dt class="text-12px text-ink-tertiary">{{ t('package.facts.price') }}</dt>
              <dd class="m-0 text-ink-primary">
                {{ price.text }}
                <span class="flex flex-wrap items-center gap-6px text-12px text-ink-tertiary">
                  <span>{{ t('package.facts.region', { region: price.region }) }}</span>
                  <a
                    v-if="price.source"
                    :href="price.source"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="text-ink-secondary"
                    >{{ displayUrl(price.source) }}</a
                  >
                  <span v-if="price.checked">{{ t('package.facts.checkedOn', { date: price.checked }) }}</span>
                </span>
              </dd>
            </div>
          </dl>
          <div v-if="card.migration.length" class="flex flex-col gap-6px">
            <h3 class="m-0 text-13.5px font-600 text-ink-primary">
              {{ t('package.alternatives.page.migration', { name, target: card.pkg.displayName }) }}
            </h3>
            <ol class="m-0 flex flex-col gap-4px pl-20px text-13px leading-[1.6] text-ink-secondary">
              <li v-for="step in card.migration" :key="step.key">
                <span class="font-600 text-ink-primary">{{ `${step.action} ` }}</span>
                <span>{{ step.text }}</span>
                <a
                  v-if="step.source"
                  :href="step.source"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="ml-6px text-12px text-ink-tertiary"
                  >{{ displayUrl(step.source) }}</a
                >
              </li>
            </ol>
            <p class="m-0 text-12px text-ink-tertiary">{{ t('package.alternatives.page.migrationNote', { name }) }}</p>
          </div>
        </section>
      </li>
    </ol>

    <p class="m-0">
      <NuxtLink
        :to="overviewPath"
        class="text-13px text-ink-secondary no-underline underline-offset-3 hover:text-ink-primary hover:underline"
        >{{ t('package.alternatives.page.back', { name }) }}</NuxtLink
      >
    </p>
  </article>
</template>

<style>
.alternatives-table th,
.alternatives-table td {
  padding: 8px 16px 8px 0;
  border-bottom: 1px solid var(--on-border-subtle);
  vertical-align: top;
}

.alternatives-table thead th {
  font-weight: 500;
}
</style>
