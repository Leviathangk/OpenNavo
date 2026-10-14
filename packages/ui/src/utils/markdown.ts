// Remote Markdown (releases/collections, 08 §8.31): disable raw HTML in markdown-it, then sanitize with DOMPurify.
// Web SSR renders releases for SEO; isomorphic-dompurify uses jsdom on Node and native DOM in browsers.
import { sanitize } from 'isomorphic-dompurify';
import MarkdownIt from 'markdown-it';
import { codeWordsHtml } from './codeText';

// Allow only tags markdown-it emits with HTML/images disabled.
const ALLOWED_TAGS = [
  'a',
  'blockquote',
  'br',
  'code',
  'em',
  'h2',
  'h3',
  'h4',
  'h5',
  'h6',
  'hr',
  'li',
  'ol',
  'p',
  'pre',
  's',
  'span',
  'strong',
  'table',
  'tbody',
  'td',
  'th',
  'thead',
  'tr',
  'ul',
  'wbr'
];
const ALLOWED_ATTR = ['href', 'title', 'target', 'rel', 'class', 'start'];

export interface RenderMarkdownOptions {
  /** Top body heading level based on outer headings: two below page h1, three by default below section h2. */
  headingLevel?: number;
}

// Use a type alias: markdown-it env requires an indexable object, which an interface does not satisfy.
type MarkdownEnv = { headingLevel: number };

const md = new MarkdownIt({ html: false, linkify: true });

// Map encountered headings in order to headingLevel, headingLevel+1, up to h6,
// without skipping levels for accessibility/Lighthouse; visual sizes use ordered classes independently of tags.
md.core.ruler.push('on_heading_level', state => {
  const base = (state.env as MarkdownEnv).headingLevel;
  const levels = [
    ...new Set(state.tokens.filter(token => token.type === 'heading_open').map(token => token.tag))
  ].sort();
  for (const token of state.tokens) {
    if (token.type !== 'heading_open' && token.type !== 'heading_close') continue;
    const rank = levels.indexOf(token.tag);
    token.tag = `h${Math.min(6, base + rank)}`;
    if (token.type === 'heading_open') token.attrJoin('class', `on-md-heading-${Math.min(rank, 2) + 1}`);
  }
});

// Convert markdown-it table alignment styles to classes; sanitizer does not permit style.
md.core.ruler.push('on_table_align', state => {
  for (const token of state.tokens) {
    if (token.type !== 'th_open' && token.type !== 'td_open') continue;
    const align = /^text-align:(left|center|right)$/.exec(String(token.attrGet('style') ?? ''))?.[1];
    token.attrs = token.attrs?.filter(([name]) => name !== 'style') ?? null;
    if (align) token.attrJoin('class', `on-md-${align}`);
  }
});

// Open external links in new windows without opener access.
md.core.ruler.push('on_link_target', state => {
  for (const block of state.tokens) {
    for (const token of block.children ?? []) {
      if (token.type !== 'link_open') continue;
      token.attrSet('target', '_blank');
      token.attrSet('rel', 'noopener noreferrer');
    }
  }
});

// Inline code wraps between words only, like OnCodeText; OnMarkdownView styles .on-code-word.
md.renderer.rules.code_inline = (tokens, index) =>
  `<code>${codeWordsHtml(tokens[index]?.content ?? '', md.utils.escapeHtml)}</code>`;

// Never render images as img (backend already rewrites them); fallback to original-image links labeled with alt text.
md.renderer.rules.image = (tokens, index, options, env, self) => {
  const token = tokens[index];
  const src = String(token?.attrGet('src') ?? '');
  const alt = self.renderInlineAsText(token?.children ?? [], options, env);
  const href = md.utils.escapeHtml(src);
  return `<a href="${href}" target="_blank" rel="noopener noreferrer">${md.utils.escapeHtml(alt) || href}</a>`;
};

/** Markdown → sanitized HTML string. */
export function renderMarkdown(source: string, options: RenderMarkdownOptions = {}): string {
  const env: MarkdownEnv = { headingLevel: Math.min(6, Math.max(2, options.headingLevel ?? 3)) };
  return sanitize(md.render(source, env), { ALLOWED_TAGS, ALLOWED_ATTR, ALLOW_DATA_ATTR: false });
}
