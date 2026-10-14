<script setup lang="ts">
import type { PublicComponents } from '@opennavo/api';
import { formatDate, formatReleaseVersion } from '@opennavo/shared';
import { displayUrl, OnChip, OnCodeText, OnIcon, parseInline } from '@opennavo/ui';

type ReleaseEntry = PublicComponents['schemas']['ReleaseEntry'];

// Latest-release card (08 §10.5, .latest): up to three highlights, View all N versions, original link.
// Render bold/code highlight segments as OnVersionEntry does.
const props = defineProps<{ release: ReleaseEntry; total: number; versionsHref: string }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

// Take the first highlight per group, up to three lines (mockup).
const rows = computed(() =>
  props.release.sections
    .flatMap(section => (section.items[0] ? [{ area: section.area, parts: parseInline(section.items[0]) }] : []))
    .slice(0, 3)
);

const meta = computed(() =>
  [
    props.release.publishedAt ? formatDate(props.release.publishedAt, { locale: appLocale.value }) : null,
    t(`package.release.source.${props.release.source}`),
    props.release.translation.status === 'machine' || props.release.translation.status === 'manual'
      ? t('package.release.translated')
      : null
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
        <span class="min-w-0">
          <template v-for="(part, key) in row.parts" :key="key">
            <strong v-if="part.type === 'strong'" class="font-600 text-ink-primary">{{ part.text }}</strong>
            <code
              v-else-if="part.type === 'code'"
              class="rounded-tiny bg-surface-inset px-6px py-1px font-mono text-12px text-component-mono-text"
              ><OnCodeText :text="part.text"
            /></code>
            <template v-else>{{ part.text }}</template>
          </template>
        </span>
      </li>
    </ul>
    <p v-else-if="release.summary" class="m-0 mt-10px text-13px leading-[1.5] text-ink-secondary">
      {{ release.summary }}
    </p>
    <MarkdownContent v-else-if="release.bodyMarkdown" class="mt-10px" :source="release.bodyMarkdown" />
    <p v-else class="m-0 mt-10px text-13px text-ink-tertiary">{{ t('package.release.noNotes') }}</p>
    <div class="mt-12px flex flex-wrap items-center justify-between gap-8px text-12.5px">
      <NuxtLink
        :to="versionsHref"
        class="inline-flex items-center gap-4px rounded-tiny text-ink-secondary no-underline outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
      >
        {{ t('package.release.viewAll', { count: total }, { plural: total }) }}
        <OnIcon name="arrow-right" :size="14" />
      </NuxtLink>
      <a
        v-if="release.sourceUrl"
        :href="release.sourceUrl"
        target="_blank"
        rel="noopener noreferrer"
        class="inline-flex items-center gap-4px rounded-tiny text-ink-tertiary no-underline outline-none hover:text-ink-primary focus-visible:shadow-focus-ring"
      >
        {{ displayUrl(release.sourceUrl) }}
        <OnIcon name="arrow-up-right" :size="14" />
      </a>
    </div>
  </section>
</template>
