// Article body renderer (WP16/WP17): the admin preview and the published page use this one
// function, so what reviewers approve is what readers get. Raw HTML stays escaped (markdown-it
// html:false) and DOMPurify keeps only the tags below. Images render only when they are listed
// assets with stored sizes; other image URLs become plain links. ::app[token] lines become app
// card segments for the host to render, and headings get anchors for the table of contents.
import { sanitize } from 'isomorphic-dompurify';
import MarkdownIt from 'markdown-it';

const ALLOWED_TAGS = [
  'a',
  'blockquote',
  'br',
  'code',
  'em',
  'h2',
  'h3',
  'h4',
  'hr',
  'img',
  'li',
  'ol',
  'p',
  'pre',
  's',
  'strong',
  'table',
  'tbody',
  'td',
  'th',
  'thead',
  'tr',
  'ul'
];
const ALLOWED_ATTR = [
  'href',
  'title',
  'target',
  'rel',
  'class',
  'start',
  'id',
  'src',
  'alt',
  'width',
  'height',
  'loading',
  'decoding'
];

export const ARTICLE_MAX_APP_CARDS = 20;

export interface ArticleImageInfo {
  url: string;
  width?: number | null;
  height?: number | null;
}

export type ArticleSegment = { type: 'html'; html: string } | { type: 'app'; token: string };

export interface ArticleHeading {
  id: string;
  text: string;
}

export interface RenderedArticle {
  segments: ArticleSegment[];
  toc: ArticleHeading[];
}

type ArticleEnv = { images: Map<string, ArticleImageInfo>; ids: Set<string>; toc: ArticleHeading[] };

const md = new MarkdownIt({ html: false, linkify: true });

/** Anchor from heading text: letters and digits of any script, joined by hyphens. */
export function headingId(text: string): string {
  return (
    text
      .normalize('NFKC')
      .toLowerCase()
      .replace(/[^\p{L}\p{N}]+/gu, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 60) || 'section'
  );
}

// The page title is the only h1: # and ## become h2, ### h3, deeper levels h4. h2 headings feed the
// table of contents; every heading gets a unique id.
md.core.ruler.push('article_headings', state => {
  const env = state.env as ArticleEnv;
  state.tokens.forEach((token, index) => {
    if (token.type !== 'heading_open' && token.type !== 'heading_close') return;
    const level = Number(token.tag.slice(1));
    token.tag = `h${Math.min(4, Math.max(2, level === 1 ? 2 : level))}`;
    if (token.type !== 'heading_open') return;
    const text = state.tokens[index + 1]?.children?.map(child => child.content).join('') ?? '';
    let id = headingId(text);
    for (let suffix = 2; env.ids.has(id); suffix += 1) id = `${headingId(text)}-${suffix}`;
    env.ids.add(id);
    token.attrSet('id', id);
    if (token.tag === 'h2') env.toc.push({ id, text });
  });
});

md.core.ruler.push('article_tables', state => {
  for (const token of state.tokens) {
    if (token.type !== 'th_open' && token.type !== 'td_open') continue;
    const align = /^text-align:(left|center|right)$/.exec(String(token.attrGet('style') ?? ''))?.[1];
    token.attrs = token.attrs?.filter(([name]) => name !== 'style') ?? null;
    if (align) token.attrJoin('class', `on-md-${align}`);
  }
});

// Site links stay in the tab; other links open a new one without opener access.
md.core.ruler.push('article_links', state => {
  for (const block of state.tokens) {
    for (const token of block.children ?? []) {
      if (token.type !== 'link_open') continue;
      const href = String(token.attrGet('href') ?? '');
      if (href.startsWith('/') && !href.startsWith('//')) continue;
      if (href.startsWith('#')) continue;
      token.attrSet('target', '_blank');
      token.attrSet('rel', 'noopener noreferrer');
    }
  }
});

md.renderer.rules.image = (tokens, index, options, env, self) => {
  const token = tokens[index];
  const src = String(token?.attrGet('src') ?? '');
  const alt = self.renderInlineAsText(token?.children ?? [], options, env);
  const image = (env as ArticleEnv).images.get(src);
  if (!image || !alt.trim()) {
    const href = md.utils.escapeHtml(src);
    return `<a href="${href}" target="_blank" rel="noopener noreferrer">${md.utils.escapeHtml(alt) || href}</a>`;
  }
  const size = image.width && image.height ? ` width="${image.width}" height="${image.height}"` : '';
  return `<img src="${md.utils.escapeHtml(image.url)}" alt="${md.utils.escapeHtml(alt)}"${size} loading="lazy" decoding="async">`;
};

const DIRECTIVE = /^::app\[([a-z0-9][a-z0-9@+._-]*)\]\s*$/;
const FENCE = /^\s{0,3}(`{3,}|~{3,})/;

/** Markdown → sanitized HTML segments, app card segments and the h2 table of contents. */
export function renderArticle(source: string, options: { images?: readonly ArticleImageInfo[] } = {}): RenderedArticle {
  const env: ArticleEnv = {
    images: new Map((options.images ?? []).map(image => [image.url, image])),
    ids: new Set(),
    toc: []
  };
  const segments: ArticleSegment[] = [];
  let chunk: string[] = [];
  let fence: string | null = null;
  let cards = 0;
  const flush = () => {
    const text = chunk.join('\n');
    chunk = [];
    if (!text.trim()) return;
    segments.push({
      type: 'html',
      html: sanitize(md.render(text, env), { ALLOWED_TAGS, ALLOWED_ATTR, ALLOW_DATA_ATTR: false })
    });
  };
  for (const line of source.replace(/\r\n/g, '\n').split('\n')) {
    const marker = FENCE.exec(line)?.[1];
    if (marker && (fence === null || marker.startsWith(fence))) fence = fence === null ? marker[0]!.repeat(3) : null;
    const token = fence === null ? DIRECTIVE.exec(line)?.[1] : undefined;
    if (!token) {
      chunk.push(line);
      continue;
    }
    flush();
    if (cards < ARTICLE_MAX_APP_CARDS) segments.push({ type: 'app', token });
    cards += 1;
  }
  flush();
  return { segments, toc: env.toc };
}
