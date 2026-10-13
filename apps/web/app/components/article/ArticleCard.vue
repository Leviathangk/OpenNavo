<script setup lang="ts">
import { formatDate } from '@opennavo/shared';
import type { ArticleCard } from '~/utils/article';

// Article card (WP17) for lists, author pages and related articles: type, headline, summary, date and
// reading time. Cards whose text is in another language say so with lang for screen readers.
const props = withDefaults(
  defineProps<{ card: ArticleCard; featured?: boolean; compact?: boolean; headingLevel?: 2 | 3 }>(),
  { featured: false, compact: false, headingLevel: 2 }
);

const { t, locale } = useI18n();
const localePath = useLocalePath();
const appLocale = computed(() => toAppLocale(locale.value));
const href = computed(() => localePath(`/articles/${props.card.slug}`));
const meta = computed(() =>
  [
    ...(props.compact ? [] : [formatDate(props.card.publishedAt, { locale: appLocale.value })]),
    t('articles.readingTime', { minutes: props.card.readingMinutes })
  ].join(' · ')
);
</script>

<template>
  <article
    class="relative box-border flex min-w-0 gap-16px rounded-big border border-solid border-line-subtle bg-surface-card p-16px focus-within:shadow-focus-ring"
    :class="featured ? 'flex-col md:flex-row' : 'flex-row'"
    :lang="card.locale === appLocale ? undefined : card.locale"
    data-testid="article-card"
  >
    <img
      v-if="card.cover && featured && !compact"
      :src="card.cover.url"
      :alt="card.cover.alt"
      :width="card.cover.width ?? undefined"
      :height="card.cover.height ?? undefined"
      decoding="async"
      class="block aspect-[16/9] w-full shrink-0 rounded-default bg-surface-inset object-cover md:w-280px"
    />
    <div class="flex min-w-0 flex-1 flex-col gap-6px">
      <span class="text-12px text-ink-tertiary">{{ t(`articles.types.${card.type}`) }}</span>
      <component
        :is="`h${headingLevel}`"
        class="m-0 font-600 text-ink-primary"
        :class="featured ? 'text-20px leading-[1.3]' : 'text-15px leading-[1.4]'"
      >
        <NuxtLink :to="href" class="on-stretched text-inherit no-underline outline-none hover:underline">
          {{ card.title }}
        </NuxtLink>
      </component>
      <p v-if="card.summary && !compact" class="m-0 line-clamp-2 text-13px leading-[1.6] text-ink-secondary">
        {{ card.summary }}
      </p>
      <span class="text-12px text-ink-tertiary tabular-nums">{{ meta }}</span>
    </div>
    <img
      v-if="card.cover && !featured && !compact"
      :src="card.cover.url"
      :alt="card.cover.alt"
      :width="card.cover.width ?? undefined"
      :height="card.cover.height ?? undefined"
      loading="lazy"
      decoding="async"
      class="hidden aspect-[4/3] w-120px shrink-0 self-start rounded-default bg-surface-inset object-cover sm:block"
    />
  </article>
</template>
