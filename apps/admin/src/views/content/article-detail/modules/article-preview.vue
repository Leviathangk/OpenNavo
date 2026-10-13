<script setup lang="ts">
import { computed } from 'vue';
import { OnMarkdownView } from '@opennavo/ui';
import { renderArticle } from '@opennavo/ui/markdown';
import type { ArticleImageInfo } from '@opennavo/ui/markdown';
import { $t } from '@/locales';

defineOptions({ name: 'ArticlePreview' });

// The site's renderer, so reviewers see what readers get: only listed assets render as images and
// ::app[token] lines become app cards (shown here as placeholders).
const props = defineProps<{ source: string; images: ArticleImageInfo[] }>();
const rendered = computed(() => renderArticle(props.source, { images: props.images }));
</script>

<template>
  <div class="markdown-preview min-h-120px flex-col gap-12px rounded-default bg-surface-card px-16px py-12px">
    <NText v-if="!rendered.segments.length" depth="3">—</NText>
    <template v-for="(segment, index) in rendered.segments" :key="index">
      <OnMarkdownView v-if="segment.type === 'html'" :html="segment.html" size="article" />
      <div
        v-else
        class="flex items-center gap-8px rounded-default border border-dashed border-gray-400 px-12px py-8px text-13px"
        data-testid="app-card-placeholder"
      >
        <NTag size="small" :bordered="false">{{ $t('page.content.articleDetail.appCard') }}</NTag>
        <span class="font-mono">{{ segment.token }}</span>
      </div>
    </template>
  </div>
</template>
