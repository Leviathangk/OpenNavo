<script setup lang="ts">
import { formatDate } from '~/utils/packageDate';
import type { PublicComponents } from '@opennavo/api';
import { formatReleaseVersion, releaseTitleDetail } from '@opennavo/shared';
import { displayUrl, OnChip, OnCodeText, OnIcon, parseInline } from '@opennavo/ui';

type ReleaseEntry = PublicComponents['schemas']['ReleaseEntry'];

// Latest-release card (08 §10.5, .latest): up to three highlights, View all N versions, original link.
// Render bold/code highlight segments as OnVersionEntry does. The h2 names only the release (C3): the New
// chip, the date and the source sit in a paragraph after it, so they never become heading text.
const props = defineProps<{ release: ReleaseEntry; total: number; versionsHref: string }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

// Editorial titles often only repeat the app name and version; show the ones that add to it.
const titleDetail = computed(() => releaseTitleDetail(props.release.title, props.release.version));

// Take the first highlight per group, up to three lines (mockup).
const rows = computed(() =>
  props.release.sections
    .flatMap(section =>
      section.items[0]?.trim() ? [{ area: section.area, parts: parseInline(section.items[0]) }] : []
    )
    .slice(0, 3)
);

const meta = computed(() => t(`package.release.source.${props.release.source}`));
</script>

<template>
  <section
    v-if="rows.length || release.summary?.trim() || release.bodyMarkdown?.trim()"
    id="whats-new"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="whats-new-title"
  >
    <h2 id="whats-new-title" class="m-0 text-13.5px font-600 text-ink-primary">
      {{ t('package.release.title', { version: formatReleaseVersion(release) }) }}
    </h2>
    <!-- Separators live in the text, so the line also reads well without its layout. -->
    <p class="m-0 mt-4px flex flex-wrap items-center gap-x-8px gap-y-4px text-12px text-ink-tertiary">
      <OnChip v-if="release.isLatest" tone="accent">{{ t('package.release.new') }}</OnChip>
      <span
        >{{ release.isLatest ? ' ' : ''
        }}<time v-if="release.publishedAt" :datetime="release.publishedAt">{{
          formatDate(release.publishedAt, { locale: appLocale })
        }}</time
        >{{ release.publishedAt ? ` · ${meta}` : meta }}</span
      >
    </p>
    <p v-if="titleDetail" class="m-0 mt-10px text-13px font-600 text-ink-primary">{{ titleDetail }}</p>
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
