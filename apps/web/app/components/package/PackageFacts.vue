<script setup lang="ts">
import type { PackageDetail } from '@opennavo/api';
import { formatDate, formatVersion } from '@opennavo/shared';
import { displayUrl, OnChip } from '@opennavo/ui';
import { priceAmount, versionDates } from '~/utils/appAnswer';

// WP9 facts with their sources: the verified price, the vendor's macOS requirements beside
// Homebrew's (both shown as pending verification where they disagree) and the two dates of the
// current version. Facts without evidence are left out.
const props = defineProps<{ pkg: PackageDetail }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const date = (value: string) => formatDate(value, { locale: appLocale.value });

const price = computed(() => props.pkg.price ?? null);
const amount = computed(() => (price.value ? priceAmount(price.value, t, appLocale.value) : ''));
const supplement = computed(() => props.pkg.macosSupplement ?? null);
const dates = computed(() => versionDates(props.pkg));
const homebrewMacos = computed(() => {
  const parts: string[] = [];
  if (props.pkg.dependsOn?.macos) parts.push(`macOS ${props.pkg.dependsOn.macos}`);
  if (props.pkg.supports.status === 'known')
    parts.push(
      (['arm64', 'x86_64'] as const)
        .filter(arch => props.pkg.supports[arch])
        .map(arch => t(`package.arch.${arch}`))
        .join(' · ')
    );
  return parts.filter(Boolean).join(' · ');
});
const visible = computed(() =>
  Boolean(price.value || supplement.value || dates.value.recorded || dates.value.released)
);

function range(item: NonNullable<PackageDetail['macosSupplement']>['ranges'][number]) {
  const parts = [t(`package.facts.support.${item.support}`)];
  if (item.minimum) parts.push(t('package.facts.minimum', { version: item.minimum }));
  if (item.maximum) parts.push(t('package.facts.maximum', { version: item.maximum }));
  return `${t(`package.arch.${item.arch}`)}: ${parts.join(' · ')}`;
}
</script>

<template>
  <section
    v-if="visible"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="package-facts-title"
    data-testid="package-facts"
  >
    <h2 id="package-facts-title" class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.facts.title') }}</h2>
    <dl class="m-0 mt-8px flex flex-col gap-10px text-13px leading-[1.6]">
      <div v-if="price" data-testid="facts-price">
        <dt class="text-12px text-ink-tertiary">{{ t('package.facts.price') }}</dt>
        <dd class="m-0 text-ink-primary">
          {{ t(`package.facts.types.${price.type}`) }}
          <template v-if="amount"> · {{ amount }}</template>
          <template v-if="price.plan"> · {{ t('package.facts.plan', { plan: price.plan }) }}</template>
          <template v-if="price.region"> · {{ t('package.facts.region', { region: price.region }) }}</template>
          <span class="mt-2px flex flex-wrap items-center gap-6px text-12px text-ink-tertiary">
            <a :href="price.sourceUrl" target="_blank" rel="noopener noreferrer" class="text-ink-secondary">{{
              displayUrl(price.sourceUrl)
            }}</a>
            <span>{{ t('package.facts.checkedOn', { date: date(price.verifiedAt) }) }}</span>
            <OnChip v-if="price.stale" tone="warning">{{ t('package.facts.stale') }}</OnChip>
          </span>
        </dd>
      </div>
      <div v-if="supplement" data-testid="facts-macos">
        <dt class="text-12px text-ink-tertiary">{{ t('package.facts.macos') }}</dt>
        <dd class="m-0 text-ink-primary">
          <span v-for="item in supplement.ranges" :key="item.arch" class="block">{{ range(item) }}</span>
          <span v-if="supplement.conflicts.length" class="mt-4px flex flex-wrap items-center gap-6px text-12.5px">
            <OnChip tone="warning">{{ t('package.facts.pending') }}</OnChip>
            <span class="text-ink-secondary">
              {{
                t('package.facts.conflict', {
                  fields: supplement.conflicts.map(code => t(`package.facts.conflictFields.${code}`)).join(', ')
                })
              }}
              <template v-if="homebrewMacos">{{ t('package.facts.homebrewSays', { value: homebrewMacos }) }}</template>
            </span>
          </span>
          <span class="mt-2px flex flex-wrap items-center gap-6px text-12px text-ink-tertiary">
            <a :href="supplement.sourceUrl" target="_blank" rel="noopener noreferrer" class="text-ink-secondary">{{
              displayUrl(supplement.sourceUrl)
            }}</a>
            <span>{{ t('package.facts.checkedOn', { date: date(supplement.verifiedAt) }) }}</span>
            <OnChip v-if="supplement.stale" tone="warning">{{ t('package.facts.stale') }}</OnChip>
          </span>
        </dd>
      </div>
      <div v-if="dates.recorded || dates.released" data-testid="facts-version">
        <dt class="text-12px text-ink-tertiary">
          {{ t('package.facts.version', { version: formatVersion(pkg.version) }) }}
        </dt>
        <dd class="m-0 text-ink-primary">
          <span v-if="dates.recorded" class="block">{{
            t('package.facts.recorded', { date: date(dates.recorded) })
          }}</span>
          <span v-if="dates.released" class="block">{{
            t('package.facts.released', { date: date(dates.released) })
          }}</span>
        </dd>
      </div>
    </dl>
  </section>
</template>
