// Export Markdown rendering (markdown-it + DOMPurify, about 47 KB brotli) separately from the main entry.
// Every page imports the main entry; including the renderer there would download it everywhere.
export { default as OnMarkdown } from './components/OnMarkdown.vue';
export type { OnMarkdownProps } from './components/OnMarkdown.vue';
export { renderMarkdown, sanitizeReleaseHtml } from './utils/markdown';
export type { RenderMarkdownOptions } from './utils/markdown';
export { ARTICLE_MAX_APP_CARDS, headingId, renderArticle } from './utils/article';
export type {
  ArticleHeading,
  ArticleImageInfo,
  ArticleSegment,
  RenderArticleOptions,
  RenderedArticle
} from './utils/article';
