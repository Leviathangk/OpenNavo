<script setup lang="ts">
import type { PublicComponents } from '@opennavo/api';
import { formatDate } from '@opennavo/shared';
import { displayUrl, OnChip } from '@opennavo/ui';
import { httpsUrl } from '~/utils/linkApp';

// Recommendation criteria (F4): why these apps are in the collection, with the source the criteria follow
// and when it was checked. Without criteria the page makes no recommendation claim at all.
const props = defineProps<{ criteria: PublicComponents['schemas']['CollectionCriteria'] }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));
const evidence = computed(() => props.criteria.evidence);
const source = computed(() => httpsUrl(evidence.value.sourceUrl));
const checked = computed(() =>
  evidence.value.checkedAt ? formatDate(evidence.value.checkedAt, { locale: appLocale.value }) : null
);
// Unverified or stale criteria are still shown as the editors' criteria, marked as such.
const flag = computed(() =>
  evidence.value.status === 'stale'
    ? t('package.facts.stale')
    : evidence.value.status === 'verified'
      ? null
      : t('package.facts.pending')
);
</script>

<template>
  <section class="flex flex-col gap-8px" aria-labelledby="collection-criteria">
    <h2 id="collection-criteria" class="m-0 text-17px font-600 text-ink-primary">
      {{ t('collections.setup.criteria') }}
    </h2>
    <MarkdownContent size="md" :heading-level="3" :source="criteria.criteria" />
    <p
      v-if="source || checked || flag"
      class="m-0 flex flex-wrap items-center gap-x-8px gap-y-4px text-12px text-ink-tertiary"
    >
      <template v-if="source">
        <span>{{ t('collections.setup.source') }}</span>
        <a :href="source" target="_blank" rel="noopener noreferrer" class="text-ink-secondary">{{
          displayUrl(source)
        }}</a>
      </template>
      <span v-if="checked">{{ t('package.facts.checkedOn', { date: checked }) }}</span>
      <OnChip v-if="flag" tone="warning">{{ flag }}</OnChip>
    </p>
  </section>
</template>
