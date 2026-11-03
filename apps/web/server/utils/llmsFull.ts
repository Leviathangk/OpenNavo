import { LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared';
import { BRAND_FACTS } from '../../shared/utils/brandFacts';
import { publicApiSpecUrl } from './publicApiSpec';
import { siteMessage } from './siteCopy';

/** The subset of a public package summary the full document quotes. */
export interface LlmsFullApp {
  kind: string;
  token: string;
  name?: string;
  displayName?: string;
  summary?: string | null;
  version?: string;
  installs30d?: number;
  rank30d?: number;
  primaryCategory?: { name?: string; slug?: string } | null;
}

export interface LlmsFullCategory {
  slug: string;
  name: string;
  description?: string | null;
  packageCount: number;
  children?: LlmsFullCategory[];
}

export interface LlmsFullCollection {
  slug: string;
  title: string;
  subtitle?: string | null;
  itemCount: number;
}

export interface LlmsFullArticle {
  slug: string;
  title: string;
  summary?: string | null;
  publishedAt?: string | null;
  type?: string;
}

export interface LlmsFullData {
  total: number;
  linkTotal: number;
  categories: LlmsFullCategory[];
  collections: LlmsFullCollection[];
  articles: LlmsFullArticle[];
  apps: LlmsFullApp[];
  generatedAt?: string;
}

export const EMPTY_LLMS_FULL_DATA: LlmsFullData = {
  total: 0,
  linkTotal: 0,
  categories: [],
  collections: [],
  articles: [],
  apps: []
};

const NUMBER = new Intl.NumberFormat('en-US');
const MAX_SUMMARY = 160;
const MAX_APPS = 100;
const MAX_CATEGORIES = 40;
const MAX_COLLECTIONS = 30;
const MAX_ARTICLES = 20;

function text(value: string | null | undefined, max = MAX_SUMMARY): string {
  if (!value) return '';
  const clean = value.replace(/\s+/g, ' ').trim();
  if (clean.length <= max) return clean.replace(/[[\]]/g, '\\$&');
  const cut = clean.slice(0, Math.max(1, max - 1));
  const word = cut.lastIndexOf(' ');
  const body = (word > max * 0.6 ? cut.slice(0, word) : cut).trimEnd();
  return `${body}…`.replace(/[[\]]/g, '\\$&');
}

function appName(app: LlmsFullApp): string {
  return text(app.displayName || app.name || app.token, 120);
}

function appLine(origin: string, item: LlmsFullApp): string {
  const facts: string[] = [`token ${item.token}`];
  if (item.primaryCategory?.name) facts.push(`category ${text(item.primaryCategory.name, 80)}`);
  if (typeof item.rank30d === 'number' && item.rank30d > 0) facts.push(`rank #${NUMBER.format(item.rank30d)}`);
  if (typeof item.installs30d === 'number' && item.installs30d > 0)
    facts.push(`${NUMBER.format(item.installs30d)} installs in 30 days`);
  if (item.version) facts.push(`version ${item.version}`);
  const summary = text(item.summary);
  return `- [${appName(item)}](${origin}/apps/${encodeURIComponent(item.token)})${summary ? ` — ${summary}` : ''} (${facts.join('; ')})`;
}

/** A stable English projection: published catalog facts and interfaces, with no drafts or private fields. */
export function renderLlmsFull(siteUrl: string, apiBase: string | undefined, data: LlmsFullData): string {
  const origin = new URL(siteUrl).origin;
  const message = (key: string) => siteMessage('en-US', key);
  const generated = (data.generatedAt ?? new Date().toISOString()).slice(0, 10);
  const categories: LlmsFullCategory[] = [];
  const collect = (nodes: LlmsFullCategory[]) => {
    for (const node of nodes) {
      if (categories.length >= MAX_CATEGORIES) return;
      categories.push(node);
      collect(node.children ?? []);
    }
  };
  collect(data.categories);
  const collections = data.collections.slice(0, MAX_COLLECTIONS);
  const languages = SITE_LOCALES.map(code => LOCALE_METADATA[code].name).join(', ');
  const apps = data.apps.slice(0, MAX_APPS);
  const articles = data.articles.slice(0, MAX_ARTICLES);
  const lines = [
    `# ${BRAND_FACTS.name}`,
    '',
    `> ${message(BRAND_FACTS.definitionKey)}`,
    '',
    `License: ${BRAND_FACTS.license}`,
    `Site: ${origin}`,
    `Generated: ${generated} (UTC)`,
    'Source: OpenNavo catalog database + public API',
    `Short orientation file: <${origin}/llms.txt>`,
    '',
    '## Site overview',
    '',
    'OpenNavo is a free, open-source Homebrew GUI for Mac and a public directory of Mac apps. The website lists release notes, system requirements, install commands, licenses and uninstall steps for every published app. The Mac app installs and updates Homebrew apps, scans downloads with ClamAV before installation, checks startup items and signatures, and keeps scan results on the Mac.',
    '',
    `- Catalog: ${NUMBER.format(Math.max(0, data.total - data.linkTotal))} Homebrew apps and ${NUMBER.format(data.linkTotal)} link-only entries.`,
    `- Categories: ${NUMBER.format(data.categories.length)}. Published collections: ${NUMBER.format(data.collections.length)}.`,
    `- Website languages: ${languages}. Locale availability varies by page.`,
    '- Mac app: macOS 13 or later, Apple silicon and Intel; free and open source.',
    '- Features: Homebrew install and update, ClamAV local scan, app firewall, login items, disk cleanup and hardware monitoring.',
    '',
    '## URL conventions',
    '',
    `- \`/apps/<token>\` — app overview`,
    `- \`/apps/<token>/versions\` — release history`,
    `- \`/collections/<slug>\` — curated collection`,
    `- \`/articles/<slug>\` — published article`,
    `- \`/categories/<slug>\` — category listing`,
    '- Locale prefix: `/{locale}/...` for non-default locales; `en-US` is unprefixed.',
    `- Canonical host: ${origin}; do not emit \`www\`.`,
    '',
    '## Key pages',
    '',
    `- [Home](${origin}/) — What OpenNavo is and how to get the Mac app.`,
    `- [Discover](${origin}/discover) — Browse apps by use case, category and popularity.`,
    `- [Rankings](${origin}/rankings) — Apps ranked by official Homebrew 30-day install counts.`,
    `- [Recently updated](${origin}/recent) — Apps with a new release, newest first.`,
    `- [All apps](${origin}/apps) — Complete alphabetical app index.`,
    `- [Categories](${origin}/categories) — Browse app categories and subcategories.`,
    `- [Collections](${origin}/collections) — Curated app lists for a task or workflow.`,
    `- [Articles](${origin}/articles) — Guides, comparisons and release coverage.`,
    `- [Download](${origin}/download) — Download the macOS client and read its requirements.`,
    `- [About](${origin}/about) — Project background, licensing and how OpenNavo differs from Homebrew.`,
    `- [Security](${origin}/security) — ClamAV scans, startup items, signature checks and system posture.`,
    `- [Feedback](${origin}/feedback) — Report a correction or a problem.`,
    `- [Developers](${origin}/developers) — API and MCP documentation.`,
    `- [Short llms.txt](${origin}/llms.txt) — One-screen orientation file.`,
    '',
    '## Catalog at a glance',
    ''
  ];
  if (data.total > 0)
    lines.push(
      `- ${NUMBER.format(Math.max(0, data.total - data.linkTotal))} Homebrew apps and ${NUMBER.format(data.linkTotal)} link-only entries that open the developer's download page.`,
      `- ${NUMBER.format(data.categories.length)} top-level categories and ${NUMBER.format(data.collections.length)} published collections.`,
      '- Every app page shows its latest version, release notes, requirements, price and license status, install and uninstall steps, and source.'
    );
  else
    lines.push(
      'Catalog data is temporarily unavailable. The public API, sitemap and category pages remain authoritative until the next snapshot.'
    );
  lines.push('');
  if (categories.length) {
    lines.push('## Categories', '');
    for (const category of categories)
      lines.push(
        `- [${text(category.name, 100)}](${origin}/categories/${encodeURIComponent(category.slug)})${
          category.description ? ` — ${text(category.description, 160)}` : ''
        } (${NUMBER.format(category.packageCount)} apps)`
      );
    lines.push('');
  }
  if (apps.length) {
    lines.push(
      `## App index (top ${apps.length} by 30-day installs)`,
      '',
      'Top 100 by 30-day installs; this is not the full catalog. Use `/apps` or the sitemap for the complete list. Published apps only; hidden, private, disabled and deprecated apps are omitted.',
      ''
    );
    lines.push(...apps.map(item => appLine(origin, item)), '');
  }
  if (collections.length) {
    lines.push('## Collections', '');
    for (const collection of collections)
      lines.push(
        `- [${text(collection.title, 120)}](${origin}/collections/${encodeURIComponent(collection.slug)})${
          collection.subtitle ? ` — ${text(collection.subtitle, 160)}` : ''
        } (${NUMBER.format(collection.itemCount)} apps)`
      );
    lines.push('');
  }
  if (articles.length) {
    lines.push('## Articles (latest 20)', '');
    for (const article of articles) {
      const date = article.publishedAt?.slice(0, 10);
      const meta = [article.type, date].filter(Boolean).join(', ');
      lines.push(
        `- [${text(article.title, 140)}](${origin}/articles/${encodeURIComponent(article.slug)})${
          article.summary ? ` — ${text(article.summary, 200)}` : ''
        }${meta ? ` (${meta})` : ''}`
      );
    }
    lines.push('');
  }
  lines.push(
    '## Developer interfaces',
    '',
    `- MCP: <${origin}/mcp> — streamable HTTP, 9 read-only catalog tools, no account or token; 60 requests per 60 seconds per IP.`,
    `- HTTP API: <${apiBase ? apiBase.replace(/\/$/, '') : 'https://api.opennavo.com/api/v1'}> — anonymous GET; OpenAPI: <${publicApiSpecUrl(apiBase)}>; docs: <${origin}/developers>.`,
    '- API operations: `/categories`, `/packages/{kind}/{token}`, `/packages/{kind}/{token}/releases`, `/packages/{kind}/{token}/dependencies`, `/packages/{kind}/{token}/related`, `/articles`, `/articles/{slug}`, `/search`, `/rankings`, `/collections`, `/collections/{slug}`, `/collections/{slug}/brewfile`.',
    '- Response envelope: `{code, msg, data}`; success `code = "0000"`; camelCase fields; RFC 3339 UTC timestamps; `locale` query or `Accept-Language`, default `en-US`.',
    '- Rate limits: ordinary reads 300/60s per IP; `/search` 60/60s; MCP 60/60s; 429 responses include `Retry-After`.',
    '- Attribution: link back to the relevant opennavo.com page when publishing catalog data, and cache where practical.',
    '',
    '## Data conventions',
    '',
    '- Price not published: `price not published`; never guess.',
    '- Licence values come from the catalog; do not infer them.',
    '- `installs30d` is a 30-day install count, not a quality score.',
    '- Timestamps are UTC; dates are `YYYY-MM-DD`.',
    '- The catalog contains no user data, telemetry or private fields.',
    '',
    '## Machine-readable links',
    '',
    `- Sitemap index: <${origin}/sitemap_index.xml>; \`/sitemap.xml\` redirects here.`,
    `- Short orientation: <${origin}/llms.txt>`,
    `- Full document: <${origin}/llms-full.txt>`,
    `- Robots policy: <${origin}/robots.txt>`,
    `- OpenAPI: <${publicApiSpecUrl(apiBase)}>`,
    `- Source code: <${BRAND_FACTS.repository}>`,
    ''
  );
  return lines.join('\n');
}
