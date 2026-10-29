<script setup lang="ts">
import { computed } from 'vue';
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import type { ReleaseEntry } from '@opennavo/api';
import { formatDate, formatReleaseVersion } from '@opennavo/shared';
import { displayUrl, OnChip, OnIcon } from '@opennavo/ui';
import { OnMarkdown } from '@opennavo/ui/markdown';
import { useAppLocale } from '@/composables/useAppLocale';
import { openExternal } from '@/utils/external';

// Latest release card (mockup 03 .latest): up to three grouped highlights, View all N versions, and original link.
const props = defineProps<{ release: ReleaseEntry; total: number; versionsTo: string }>();

const { t } = useI18n();
const { appLocale } = useAppLocale();

const rows = computed(() =>
  props.release.sections
    .flatMap(section => (section.items[0] ? [{ area: section.area, text: section.items[0] }] : []))
    .slice(0, 3)
);

const meta = computed(() =>
  [
    props.release.publishedAt ? formatDate(props.release.publishedAt, { locale: appLocale.value }) : null,
    t(`package.release.source.${props.release.source}`)
  ]
    .filter(Boolean)
    .join(' · ')
);
</script>

<template>
  <section class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px">
    <h2 class="m-0 flex flex-wrap items-center gap-8px text-13.5px font-600 text-ink-primary">
      <OnChip v-if="release.isLatest" tone="accent">{{ t('package.release.new') }}</OnChip>
      {{ t('package.release.title', { version: formatReleaseVersion(release) }) }}
      <small class="text-12px font-400 text-ink-tertiary">{{ meta }}</small>
    </h2>
    <ul v-if="rows.length" class="m-0 mt-10px flex list-none flex-col gap-8px p-0">
      <li v-for="(row, index) in rows" :key="index" class="flex gap-10px text-13px leading-[1.5] text-ink-secondary">
        <OnChip class="w-58px shrink-0 justify-center">{{ row.area }}</OnChip>
        <span class="min-w-0">{{ row.text }}</span>
      </li>
    </ul>
    <p v-else-if="release.summary" class="m-0 mt-10px text-13px leading-[1.5] text-ink-secondary">
      {{ release.summary }}
    </p>
    <OnMarkdown v-else-if="release.bodyMarkdown" class="mt-10px" :source="release.bodyMarkdown" />
    <p v-else class="m-0 mt-10px text-13px text-ink-tertiary">{{ t('package.release.noNotes') }}</p>
    <div class="mt-12px flex flex-wrap items-center justify-between gap-8px text-12.5px">
      <RouterLink
        :to="versionsTo"
        class="inline-flex items-center gap-4px rounded-tiny text-ink-secondary no-underline outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
      >
        {{ t('package.release.viewAll', { count: total }, { plural: total }) }}
        <OnIcon name="arrow-right" :size="14" />
      </RouterLink>
      <button
        v-if="release.sourceUrl"
        type="button"
        class="m-0 inline-flex items-center gap-4px rounded-tiny border-none bg-transparent p-0 font-sans text-12.5px text-ink-tertiary outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
        @click="openExternal(release.sourceUrl)"
      >
        {{ displayUrl(release.sourceUrl) }}
        <OnIcon name="arrow-up-right" :size="14" />
      </button>
    </div>
  </section>
</template>
