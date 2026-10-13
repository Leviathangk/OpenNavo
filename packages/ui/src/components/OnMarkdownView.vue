<script setup lang="ts">
export interface OnMarkdownViewProps {
  /** Sanitized renderMarkdown output; web delivers SSR HTML in payload, avoiding the renderer on first load. */
  html: string;
  /** sm is 13/20 for releases; md is 14/1.75 for descriptions; article is 15/1.8 across the column with larger headings. */
  size?: 'sm' | 'md' | 'article';
}

withDefaults(defineProps<OnMarkdownViewProps>(), { size: 'sm' });

// Template disable comments create root fragments; place here through end-of-file.
/* eslint-disable vue/no-v-html -- HTML comes from renderMarkdown: markdown-it with raw HTML disabled and DOMPurify allowlist sanitization. */
</script>

<template>
  <div
    class="on-markdown break-words text-ink-secondary"
    :class="
      size === 'article'
        ? 'on-markdown-article'
        : ['max-w-[72ch]', size === 'md' ? 'text-14px leading-[1.75]' : 'text-body-sm']
    "
    v-html="html"
  ></div>
</template>

<style>
.on-markdown > :first-child {
  margin-top: 0;
}

.on-markdown > :last-child {
  margin-bottom: 0;
}

.on-markdown :where(p, ul, ol, pre, blockquote, table) {
  margin: 0 0 10px;
}

/* Article bodies (the web article page and the admin preview, so both read the same): headings that
   stand apart and anchors clear of the sticky toolbar. */
.on-markdown-article {
  font-size: 15px;
  line-height: 1.8;
}

.on-markdown-article :where(h2, h3, h4) {
  scroll-margin-top: 72px;
}

.on-markdown-article h2 {
  margin: 32px 0 12px;
  font-size: 20px;
  line-height: 1.35;
}

.on-markdown-article h3 {
  margin: 24px 0 8px;
  font-size: 16px;
  line-height: 1.45;
}

.on-markdown-article h4 {
  margin: 20px 0 6px;
  font-size: 15px;
}

/* Article bodies only: renderArticle emits img for listed assets with stored sizes. */
.on-markdown img {
  display: block;
  max-width: 100%;
  height: auto;
  margin: 12px 0;
  border-radius: 10px;
}

.on-markdown :where(h2, h3, h4, h5, h6) {
  margin: 18px 0 8px;
  color: var(--on-text-primary);
  font-weight: 600;
}

/* Semantic heading level depends on nesting; visual size follows body heading order via renderMarkdown classes. */
.on-markdown .on-md-heading-1 {
  font-size: 15px;
  line-height: 22px;
}

.on-markdown .on-md-heading-2 {
  font-size: 14px;
  line-height: 20px;
}

.on-markdown .on-md-heading-3 {
  font-size: 13px;
  line-height: 20px;
}

.on-markdown :where(ul, ol) {
  padding-left: 20px;
}

/* Restore list markers removed by app resets. */
.on-markdown ul {
  list-style: disc;
}

.on-markdown ol {
  list-style: decimal;
}

.on-markdown li::marker {
  color: var(--on-text-tertiary);
}

.on-markdown li + li {
  margin-top: 4px;
}

.on-markdown strong {
  color: var(--on-text-primary);
  font-weight: 600;
}

.on-markdown a {
  color: var(--on-status-info);
  text-decoration: none;
  border-radius: var(--on-radius-tiny);
}

.on-markdown a:hover {
  text-decoration: underline;
}

.on-markdown a:focus-visible {
  outline: none;
  box-shadow: var(--on-shadow-focus-ring);
}

.on-markdown code {
  padding: 1px 5px;
  border-radius: var(--on-radius-tiny);
  background: var(--on-surface-inset);
  color: var(--on-text-primary);
  font-family: var(--on-font-family-mono);
  font-size: 0.92em;
}

.on-markdown pre {
  overflow-x: auto;
  padding: 12px 14px;
  border: 1px solid var(--on-border-subtle);
  border-radius: var(--on-radius-default);
  background: var(--on-surface-inset);
}

.on-markdown pre code {
  padding: 0;
  background: none;
  font-size: 12px;
  line-height: 18px;
}

.on-markdown blockquote {
  padding-left: 12px;
  border-left: 2px solid var(--on-border-default);
  color: var(--on-text-tertiary);
}

.on-markdown hr {
  margin: 16px 0;
  border: none;
  border-top: 1px solid var(--on-border-subtle);
}

.on-markdown table {
  display: block;
  max-width: 100%;
  overflow-x: auto;
  border-collapse: collapse;
}

.on-markdown :where(th, td) {
  padding: 6px 10px;
  border: 1px solid var(--on-border-subtle);
  text-align: left;
}

.on-markdown th {
  color: var(--on-text-primary);
  font-weight: 600;
}

.on-markdown .on-md-center {
  text-align: center;
}

.on-markdown .on-md-right {
  text-align: right;
}
</style>
