<script setup lang="ts">
import { formatDate } from '@opennavo/shared';
import { displayUrl } from '@opennavo/ui';
import { aroundDate, factSources, isoDate } from '~/utils/packageFacts';
import type { PackagePage } from '~/utils/packageFacts';

// Sources (T2): every source the page's facts cite, once each, with what it backs and when it was checked.
// A stale source reads "as of" its date; a source without a date is listed without one, never given one.
const props = defineProps<{ pkg: PackagePage }>();

const { t, locale } = useI18n();
const appLocale = computed(() => toAppLocale(locale.value));

const items = computed(() =>
  factSources(props.pkg).map(source => ({
    key: `${source.kind}:${source.url}`,
    // The colon is in the page language; Chinese and Japanese put no space after it.
    label: `${t('package.facts.labelled', { label: t(`package.sources.kinds.${source.kind}`) })}${
      ['zh-CN', 'zh-Hant', 'ja-JP'].includes(appLocale.value) ? '' : ' '
    }`,
    url: source.url,
    checked: source.checkedAt
      ? {
          iso: isoDate(source.checkedAt),
          date: formatDate(source.checkedAt, { locale: appLocale.value }),
          ...aroundDate(t, source.stale ? 'package.facts.asOf' : 'package.facts.checkedOn')
        }
      : null
  }))
);
</script>

<template>
  <section
    v-if="items.length"
    id="sources"
    class="rounded-big border border-solid border-line-subtle bg-surface-card px-18px py-16px"
    aria-labelledby="sources-title"
    data-testid="package-sources"
  >
    <h2 id="sources-title" class="m-0 text-13.5px font-600 text-ink-primary">{{ t('package.sources.title') }}</h2>
    <p class="m-0 mt-4px text-12px leading-[1.6] text-ink-tertiary">{{ t('package.sources.hint') }}</p>
    <ul class="m-0 mt-8px flex list-none flex-col gap-6px p-0 text-12.5px leading-[1.6]">
      <li v-for="item in items" :key="item.key" class="text-ink-tertiary">
        <span class="text-ink-secondary">{{ item.label }}</span
        ><a
          :href="item.url"
          target="_blank"
          rel="nofollow noopener noreferrer"
          class="text-ink-primary underline-offset-2 [overflow-wrap:anywhere] hover:underline"
          >{{ displayUrl(item.url) }}</a
        ><template v-if="item.checked"
          >{{ ` · ${item.checked.before}` }}<time :datetime="item.checked.iso">{{ item.checked.date }}</time
          >{{ item.checked.after }}</template
        >
      </li>
    </ul>
  </section>
</template>
