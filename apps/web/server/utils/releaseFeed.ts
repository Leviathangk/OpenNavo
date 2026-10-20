import type { PublicComponents } from '@opennavo/api';
import type { H3Event } from 'h3';
import { isValidToken, LOCALE_METADATA, SLUG_PATTERN } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import { apiStatus, fetchPackageDetail, publicApi } from './publicApi';
import { siteMessage } from './siteCopy';

type ReleaseEntry = PublicComponents['schemas']['ReleaseEntry'];
type CollectionDetail = PublicComponents['schemas']['CollectionDetail'];
type PackageSummary = PublicComponents['schemas']['PackageSummary'];

// Update feeds (F6): an RSS 2.0 feed per app at <prefix>/apps/{token}/feed.xml with its newest releases and
// their notes, and one per collection at <prefix>/collections/{slug}/feed.xml with the newest releases of
// its apps. Same pattern as the article feeds (server/utils/articleFeed.ts): the API decides what exists,
// channel copy comes from the site's messages, and feeds are noindex.

/** Releases in an app feed. */
export const RELEASE_FEED_SIZE = 20;
/** Releases read per app for a collection feed, and the most the collection feed lists. */
export const COLLECTION_RELEASES_PER_APP = 3;
export const COLLECTION_FEED_SIZE = 40;
/** Parallel release requests while building a collection feed. */
const COLLECTION_CONCURRENCY = 6;

/** Text for XML: markup characters escaped, and control characters XML 1.0 cannot carry removed. */
export function escapeXml(text: string): string {
  return (
    text
      // eslint-disable-next-line no-control-regex -- removing exactly these characters is the point
      .replace(/[\u0000-\u0008\u000B\u000C\u000E-\u001F\uFFFE\uFFFF]/g, '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&apos;')
  );
}

/** Inline **bold** and `code` of release text as HTML; everything else is escaped text. */
export function inlineHtml(text: string): string {
  return text
    .split(/(\*\*[^*]+\*\*|`[^`]+`)/)
    .map(part => {
      if (/^\*\*[^*]+\*\*$/.test(part)) return `<strong>${escapeXml(part.slice(2, -2))}</strong>`;
      if (/^`[^`]+`$/.test(part)) return `<code>${escapeXml(part.slice(1, -1))}</code>`;
      return escapeXml(part);
    })
    .join('');
}

/**
 * A release body in Markdown as plain HTML for feed readers: paragraphs, headings as bold lines and lists,
 * with inline bold and code. Nothing else is interpreted, and no raw HTML passes through.
 */
export function markdownHtml(source: string): string {
  const blocks: string[] = [];
  let list: string[] = [];
  let paragraph: string[] = [];
  const flush = () => {
    if (paragraph.length) blocks.push(`<p>${paragraph.map(inlineHtml).join('<br>')}</p>`);
    if (list.length) blocks.push(`<ul>${list.map(item => `<li>${inlineHtml(item)}</li>`).join('')}</ul>`);
    paragraph = [];
    list = [];
  };
  for (const raw of source.split(/\r?\n/)) {
    const line = raw.trim();
    const heading = /^#{1,6}\s+(.+)$/.exec(line);
    const item = /^(?:[-*+]|\d+[.)])\s+(.+)$/.exec(line);
    if (!line) flush();
    else if (heading?.[1]) {
      flush();
      blocks.push(`<p><strong>${inlineHtml(heading[1].replace(/\*\*/g, ''))}</strong></p>`);
    } else if (item?.[1]) {
      if (paragraph.length) flush();
      list.push(item[1]);
    } else {
      if (list.length) flush();
      paragraph.push(line);
    }
  }
  flush();
  return blocks.join('');
}

/** The notes of one release as HTML: its summary, then grouped highlights or the Markdown body. */
export function releaseHtml(entry: ReleaseEntry, noNotes: string): string {
  if (!entry.hasNotes) return `<p>${escapeXml(noNotes)}</p>`;
  const parts: string[] = [];
  if (entry.summary) parts.push(`<p>${inlineHtml(entry.summary)}</p>`);
  if (entry.sections.length) {
    for (const section of entry.sections) {
      parts.push(`<p><strong>${escapeXml(section.area)}</strong></p>`);
      parts.push(`<ul>${section.items.map(item => `<li>${inlineHtml(item)}</li>`).join('')}</ul>`);
    }
  } else if (entry.bodyMarkdown) parts.push(markdownHtml(entry.bodyMarkdown));
  return parts.join('') || `<p>${escapeXml(noNotes)}</p>`;
}

/** When a release came out: the upstream date, else when Homebrew picked it up; null when neither is valid. */
export function releaseDate(entry: Pick<ReleaseEntry, 'publishedAt' | 'brewCommittedAt'>): string | null {
  for (const value of [entry.publishedAt, entry.brewCommittedAt]) {
    if (!value) continue;
    const time = Date.parse(value);
    if (Number.isFinite(time) && new Date(time).getUTCFullYear() > 1) return new Date(time).toISOString();
  }
  return null;
}

/** The entry's anchor on its versions page, as OnVersionEntry writes it. */
export function releaseAnchor(entry: Pick<ReleaseEntry, 'id' | 'version'>): string {
  return entry.id ? `r-${entry.id}` : `v-${entry.version.replace(/[^0-9A-Za-z._-]+/g, '-')}`;
}

export interface FeedItem {
  title: string;
  link: string;
  /** Stable across rebuilds and page shifts: the app and its version, not a URL. */
  guid: string;
  date: string | null;
  html: string;
  category?: string;
}

export interface FeedChannel {
  title: string;
  link: string;
  description: string;
  locale: SiteLocale;
  /** This feed's own URL (atom:link rel=self). */
  self: string;
  items: FeedItem[];
}

export function renderReleaseFeed(channel: FeedChannel): string {
  const items = channel.items.map(item =>
    [
      '<item>',
      `<title>${escapeXml(item.title)}</title>`,
      `<link>${escapeXml(item.link)}</link>`,
      `<guid isPermaLink="false">${escapeXml(item.guid)}</guid>`,
      ...(item.date ? [`<pubDate>${new Date(item.date).toUTCString()}</pubDate>`] : []),
      ...(item.category ? [`<category>${escapeXml(item.category)}</category>`] : []),
      `<description>${escapeXml(item.html)}</description>`,
      '</item>'
    ].join('')
  );
  const newest = channel.items.map(item => item.date).find(Boolean);
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom">',
    '<channel>',
    `<title>${escapeXml(channel.title)}</title>`,
    `<link>${escapeXml(channel.link)}</link>`,
    `<description>${escapeXml(channel.description)}</description>`,
    `<language>${channel.locale}</language>`,
    `<atom:link href="${escapeXml(channel.self)}" rel="self" type="application/rss+xml"/>`,
    ...(newest ? [`<lastBuildDate>${new Date(newest).toUTCString()}</lastBuildDate>`] : []),
    ...items,
    '</channel>',
    '</rss>',
    ''
  ].join('\n');
}

/** Newest first; releases without a date go last, in their original order. */
export function newestFirst<T extends { date: string | null }>(items: readonly T[]): T[] {
  return items
    .map((item, index) => ({ item, index, time: item.date ? Date.parse(item.date) : -Infinity }))
    .sort((a, b) => b.time - a.time || a.index - b.index)
    .map(({ item }) => item);
}

const FEED_PATH = /^(?:\/([a-z-]+))?\/(apps|collections)\/([^/]+)\/feed\.xml$/;

/** The feed a request path names: its language (from the prefix), section and token or slug. */
export function feedTarget(
  pathname: string
): { locale: SiteLocale; section: 'apps' | 'collections'; key: string } | undefined {
  const match = FEED_PATH.exec(pathname);
  if (!match) return undefined;
  const [, prefix, section, raw] = match;
  const locale = (Object.keys(LOCALE_METADATA) as SiteLocale[]).find(
    code => LOCALE_METADATA[code].prefix === (prefix ? `/${prefix}` : '')
  );
  let key: string;
  try {
    key = decodeURIComponent(raw ?? '');
  } catch {
    return undefined;
  }
  if (!locale || !section) return undefined;
  const valid = section === 'apps' ? isValidToken(key) : SLUG_PATTERN.test(key);
  return valid ? { locale, section: section as 'apps' | 'collections', key } : undefined;
}

const notFound = () => createError({ statusCode: 404, statusMessage: 'Feed not found' });

function respond(event: H3Event, xml: string) {
  setHeader(event, 'content-type', 'application/rss+xml; charset=utf-8');
  setHeader(event, 'x-robots-tag', 'noindex');
  return xml;
}

/** An app's feed: casks first, then link apps, as the app page looks them up (usePackage). */
export async function packageFeed(event: H3Event, locale: SiteLocale, token: string) {
  const api = publicApi(event);
  const { siteUrl, get } = api;
  const found = await fetchPackageDetail(api, token, locale);
  if (!found) throw notFound();
  const detail = found;
  const page = await get<{ records: ReleaseEntry[] }>(
    `/packages/${detail.kind}/${encodeURIComponent(token)}/releases`,
    { locale, current: 1, size: RELEASE_FEED_SIZE }
  );
  const prefix = LOCALE_METADATA[locale].prefix;
  const versions = `${siteUrl}${prefix}/apps/${token}/versions`;
  const noNotes = siteMessage(locale, 'package.release.noNotes');
  return respond(
    event,
    renderReleaseFeed({
      title: siteMessage(locale, 'feeds.appTitle', { name: detail.displayName }),
      link: versions,
      description: siteMessage(locale, 'feeds.appDescription', { name: detail.displayName }),
      locale,
      self: `${siteUrl}${prefix}/apps/${token}/feed.xml`,
      items: page.records.map(entry => ({
        title: siteMessage(locale, 'feeds.itemTitle', { name: detail.displayName, version: entry.version }),
        link: `${versions}#${releaseAnchor(entry)}`,
        guid: `${detail.kind}/${token}@${entry.version}`,
        date: releaseDate(entry),
        html: releaseHtml(entry, noNotes)
      }))
    })
  );
}

async function inBatches<T, R>(values: readonly T[], size: number, run: (value: T) => Promise<R>): Promise<R[]> {
  const out: R[] = [];
  for (let start = 0; start < values.length; start += size)
    out.push(...(await Promise.all(values.slice(start, start + size).map(run))));
  return out;
}

/** A collection's feed: the newest releases of its apps. One app failing leaves it out; all failing is a 503. */
export async function collectionFeed(event: H3Event, locale: SiteLocale, slug: string) {
  const { siteUrl, get } = publicApi(event);
  const collection = await get<CollectionDetail>(`/collections/${slug}`, { locale }).catch((error: unknown) => {
    throw apiStatus(error) === 404 ? notFound() : error;
  });
  const apps: PackageSummary[] = collection.items
    .map(item => item.package)
    .filter(pkg => pkg.kind === 'cask' || pkg.kind === 'link');
  let failures = 0;
  const lists = await inBatches(apps, COLLECTION_CONCURRENCY, pkg =>
    get<{ records: ReleaseEntry[] }>(`/packages/${pkg.kind}/${encodeURIComponent(pkg.token)}/releases`, {
      locale,
      current: 1,
      size: COLLECTION_RELEASES_PER_APP
    }).then(
      page => page.records.map(entry => ({ pkg, entry })),
      () => {
        failures += 1;
        return [];
      }
    )
  );
  if (apps.length && failures === apps.length)
    throw createError({ statusCode: 503, statusMessage: 'Feed unavailable' });
  const prefix = LOCALE_METADATA[locale].prefix;
  const noNotes = siteMessage(locale, 'package.release.noNotes');
  const items = newestFirst(
    lists.flat().map(({ pkg, entry }) => ({
      title: siteMessage(locale, 'feeds.itemTitle', { name: pkg.displayName, version: entry.version }),
      link: `${siteUrl}${prefix}/apps/${pkg.token}/versions#${releaseAnchor(entry)}`,
      guid: `${pkg.kind}/${pkg.token}@${entry.version}`,
      date: releaseDate(entry),
      html: releaseHtml(entry, noNotes),
      category: pkg.displayName
    }))
  ).slice(0, COLLECTION_FEED_SIZE);
  const page = `${siteUrl}${prefix}/collections/${slug}`;
  return respond(
    event,
    renderReleaseFeed({
      title: siteMessage(locale, 'feeds.collectionTitle', { title: collection.title }),
      link: page,
      description: siteMessage(locale, 'feeds.collectionDescription', { title: collection.title }),
      locale,
      self: `${page}/feed.xml`,
      items
    })
  );
}
