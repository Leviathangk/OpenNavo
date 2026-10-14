<script setup lang="ts">
import { RouterLink } from 'vue-router';
import { useI18n } from 'vue-i18n';
import { formatDate } from '@opennavo/shared';
import type { ArticleCard } from '@/utils/article';
import { useAppLocale } from '@/composables/useAppLocale';

withDefaults(defineProps<{ card: ArticleCard; compact?: boolean; headingLevel?: 2 | 3 }>(), {
  compact: false,
  headingLevel: 2
});
const { t } = useI18n();
const { appLocale } = useAppLocale();
</script>

<template>
  <article
    class="relative flex min-w-0 items-start gap-20px border-b border-b-solid border-line-subtle py-20px focus-within:shadow-focus-ring"
    :lang="card.locale"
    data-testid="article-card"
  >
    <div class="flex min-w-0 flex-1 flex-col gap-8px">
      <component :is="`h${headingLevel}`" class="m-0 text-18px font-600 leading-[1.4] text-ink-primary">
        <RouterLink
          :to="`/articles/${card.slug}`"
          class="on-stretched break-words text-inherit no-underline outline-none hover:underline"
        >
          {{ card.title }}
        </RouterLink>
      </component>
      <p v-if="card.summary && !compact" class="m-0 line-clamp-2 text-14px leading-[1.7] text-ink-secondary">
        {{ card.summary }}
      </p>
      <div class="flex flex-wrap items-center gap-x-12px gap-y-4px text-12px text-ink-tertiary" :lang="appLocale">
        <span>{{ t(`articles.types.${card.type}`) }}</span>
        <time :datetime="card.publishedAt">{{ formatDate(card.publishedAt, { locale: appLocale }) }}</time>
        <span>{{ t('articles.readingTime', { minutes: card.readingMinutes }) }}</span>
      </div>
    </div>
    <img
      v-if="card.cover && !compact"
      :src="card.cover.url"
      :alt="card.cover.alt"
      :width="card.cover.width ?? undefined"
      :height="card.cover.height ?? undefined"
      loading="lazy"
      decoding="async"
      class="hidden aspect-[4/3] w-120px shrink-0 rounded-default bg-surface-inset object-cover sm:block"
    />
  </article>
</template>
