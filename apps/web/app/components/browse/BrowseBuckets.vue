<script setup lang="ts">
import { formatCount } from '@opennavo/shared';
import { browsePath, bucketHeading, bucketLabel, bucketPages } from '~/utils/browse';
import type { BrowseBucket, BrowseKind } from '~/utils/browse';

// The A–Z grid of a directory root (T1): one plain link per leaf page, so every page of every bucket is
// one click from here and every listed app three clicks from the home page.
const props = defineProps<{ kind: BrowseKind; buckets: readonly BrowseBucket[] }>();

const { t, locale } = useI18n();
const localePath = useLocalePath();
const appLocale = computed(() => toAppLocale(locale.value));

const items = computed(() =>
  props.buckets.map(bucket => {
    const heading = bucketHeading(props.kind, bucket.key);
    const title = t(heading.key, heading.params);
    return {
      key: bucket.key,
      label: bucketLabel(bucket.key, t),
      title,
      href: localePath(browsePath(props.kind, bucket.key)),
      count: t(
        props.kind === 'fonts' ? 'browse.fontCount' : 'browse.appCount',
        { count: formatCount(bucket.count, { locale: appLocale.value }) },
        { plural: bucket.count }
      ),
      // A bucket longer than one page lists all of its pages under the letter.
      pages: (bucket.pages > 1 ? bucketPages(bucket) : []).map(page => ({
        page,
        href: localePath(browsePath(props.kind, bucket.key, page)),
        label: t('browse.pageLink', { title, page })
      }))
    };
  })
);
</script>

<template>
  <ul class="m-0 grid list-none grid-cols-[repeat(auto-fill,minmax(128px,1fr))] gap-8px p-0">
    <li
      v-for="item in items"
      :key="item.key"
      class="flex min-w-0 flex-col gap-2px rounded-big border border-solid border-line-subtle bg-surface-card px-14px py-10px"
    >
      <NuxtLink
        :to="item.href"
        :title="item.title"
        class="self-start rounded-tiny text-20px font-600 leading-[1.3] text-ink-primary no-underline outline-none underline-offset-3 hover:underline focus-visible:shadow-focus-ring"
        >{{ item.label }}</NuxtLink
      >
      <span class="text-12px text-ink-tertiary tabular-nums">{{ item.count }}</span>
      <span v-if="item.pages.length" class="mt-2px flex flex-wrap gap-x-8px gap-y-2px text-12px">
        <NuxtLink
          v-for="link in item.pages"
          :key="link.page"
          :to="link.href"
          :aria-label="link.label"
          class="rounded-tiny text-ink-secondary no-underline outline-none underline-offset-3 tabular-nums hover:text-ink-primary hover:underline focus-visible:shadow-focus-ring"
          >{{ link.page }}</NuxtLink
        >
      </span>
    </li>
  </ul>
</template>
