// Remote Markdown (releases/collections, 08 §8.31): disable raw HTML in markdown-it, then sanitize with DOMPurify.
// Web SSR renders releases for SEO; isomorphic-dompurify uses jsdom on Node and native DOM in browsers.
import { sanitize } from 'isomorphic-dompurify';
import MarkdownIt from 'markdown-it';
import { cjkFriendlyEmphasis } from './cjkEmphasis';
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
cjkFriendlyEmphasis(md);

// The n-th distinct heading level in document order becomes headingLevel + n. Sorting the levels instead
// would turn a body that starts with "##" and later uses "#" into h4 before h3, skipping a level.
function firstSeenLevels(tags: Iterable<string>): string[] {
  const levels: string[] = [];
  for (const tag of tags) if (!levels.includes(tag)) levels.push(tag);
  return levels;
}

// Map headings in first-seen order to headingLevel, headingLevel+1, up to h6, without skipping levels for
// accessibility/Lighthouse; visual sizes use ordered classes independently of tags.
md.core.ruler.push('on_heading_level', state => {
  const base = (state.env as MarkdownEnv).headingLevel;
  const levels = firstSeenLevels(state.tokens.filter(token => token.type === 'heading_open').map(token => token.tag));
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

// GitHub's generated release notes credit bots by account, but bots only have app pages: a link such as
// github.com/github-actions[bot] (or its percent-encoded spelling) answers 404. Such links stay text.
const GITHUB_BOT_ACCOUNT = /^https?:\/\/(?:www\.)?github\.com\/[\w.-]+(?:\[|%5B)bot(?:\]|%5D)\/?(?:[?#].*)?$/i;

const isGitHubBotAccount = (href: string | null) => GITHUB_BOT_ACCOUNT.test(href ?? '');

md.core.ruler.push('on_bot_links', state => {
  for (const block of state.tokens) {
    if (!block.children) continue;
    // Whether each open link is a bot account, so its closing token goes too.
    const open: boolean[] = [];
    block.children = block.children.filter(token => {
      if (token.type === 'link_open') {
        const bot = isGitHubBotAccount(String(token.attrGet('href') ?? ''));
        open.push(bot);
        return !bot;
      }
      return token.type === 'link_close' ? !open.pop() : true;
    });
  }
});

// Remote release/collection notes link to third-party pages: open them in new windows without opener access, and
// mark them user-generated so search engines do not treat them as our endorsement.
const EXTERNAL_REL = 'noopener noreferrer nofollow ugc';
// Our own pages (relative links, opennavo.com and its subdomains) only need opener isolation, not nofollow ugc.
const INTERNAL_REL = 'noopener noreferrer';
const OWN_HOST = /^(?:[\w-]+\.)*opennavo\.com$/i;

const linkRel = (href: string) => {
  if (!/^[a-z][a-z\d+.-]*:|^\/\//i.test(href)) return INTERNAL_REL;
  try {
    return OWN_HOST.test(new URL(href, 'https://opennavo.com').hostname) ? INTERNAL_REL : EXTERNAL_REL;
  } catch {
    return EXTERNAL_REL;
  }
};

// Open external links in new windows without opener access.
md.core.ruler.push('on_link_target', state => {
  for (const block of state.tokens) {
    for (const token of block.children ?? []) {
      if (token.type !== 'link_open') continue;
      token.attrSet('target', '_blank');
      token.attrSet('rel', linkRel(String(token.attrGet('href') ?? '')));
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
  return `<a href="${href}" target="_blank" rel="${linkRel(src)}">${md.utils.escapeHtml(alt) || href}</a>`;
};

/** Markdown → sanitized HTML string. */
export function renderMarkdown(source: string, options: RenderMarkdownOptions = {}): string {
  const env: MarkdownEnv = { headingLevel: Math.min(6, Math.max(2, options.headingLevel ?? 3)) };
  return sanitize(md.render(source, env), { ALLOWED_TAGS, ALLOWED_ATTR, ALLOW_DATA_ATTR: false });
}

// Vendor HTML release notes (Sparkle appcasts, store/security plan §11.4) carry whole documents with styles, scripts,
// tracking images and their own h1s. Keep the Markdown tag set plus a few inline tags; everything else goes.
const RELEASE_HTML_TAGS = [...ALLOWED_TAGS, 'h1', 'b', 'i', 'u', 'div', 'dl', 'dt', 'dd', 'img'];

/**
 * Vendor release-note HTML → sanitized HTML for OnMarkdownView: links open outside without opener access, images
 * never load (they become links labeled with their alt text, https only), and headings start at headingLevel.
 */
export function sanitizeReleaseHtml(source: string, options: RenderMarkdownOptions = {}): string {
  const base = Math.min(6, Math.max(2, options.headingLevel ?? 4));
  const fragment = sanitize(source, {
    ALLOWED_TAGS: RELEASE_HTML_TAGS,
    ALLOWED_ATTR: ['href', 'title', 'src', 'alt', 'start'],
    ALLOW_DATA_ATTR: false,
    RETURN_DOM_FRAGMENT: true
  });
  const doc = fragment.ownerDocument;
  const https = (value: string | null) => {
    try {
      return value && new URL(value).protocol === 'https:' ? value : null;
    } catch {
      return null;
    }
  };
  // querySelectorAll lists are static, so replacing nodes while iterating is safe.
  for (const image of fragment.querySelectorAll('img')) {
    const src = https(image.getAttribute('src'));
    if (!src) {
      image.remove();
      continue;
    }
    const link = doc.createElement('a');
    link.setAttribute('href', src);
    link.textContent = image.getAttribute('alt')?.trim() || src;
    image.replaceWith(link);
  }
  for (const link of fragment.querySelectorAll('a')) {
    const raw = link.getAttribute('href') ?? '';
    // Vendor HTML may write its own site protocol-relative (//opennavo.com/apps). Resolve it
    // before the https test so an internal link is not stripped, and before the bot check so
    // a protocol-relative bot account is still matched.
    const href = raw.startsWith('//') ? `https:${raw}` : raw;
    if (isGitHubBotAccount(href)) {
      link.replaceWith(...link.childNodes);
      continue;
    }
    if (!/^https?:/i.test(href)) link.removeAttribute('href');
    else link.setAttribute('href', href);
    link.setAttribute('target', '_blank');
    // Our own pages keep opener isolation but are not user-generated third-party content.
    link.setAttribute('rel', linkRel(href));
  }
  // Same first-seen mapping as Markdown: the n-th distinct heading level found becomes headingLevel + n.
  const headings = [...fragment.querySelectorAll('h1, h2, h3, h4, h5, h6')];
  const levels = firstSeenLevels(headings.map(heading => heading.tagName.toLowerCase()));
  for (const heading of headings) {
    const rank = levels.indexOf(heading.tagName.toLowerCase());
    const next = doc.createElement(`h${Math.min(6, base + rank)}`);
    next.className = `on-md-heading-${Math.min(rank, 2) + 1}`;
    next.append(...heading.childNodes);
    heading.replaceWith(next);
  }
  const host = doc.createElement('div');
  host.append(fragment);
  return host.innerHTML;
}
