import type { PublicComponents } from '@opennavo/api';
import type { H3Event } from 'h3';
import { SITE_LOCALES, LOCALE_METADATA, type SiteLocale as Locale } from '@opennavo/shared';
import enUS from '../../i18n/locales/en-US.json';
import esES from '../../i18n/locales/es-ES.json';
import jaJP from '../../i18n/locales/ja-JP.json';
import ptBR from '../../i18n/locales/pt-BR.json';
import ruRU from '../../i18n/locales/ru-RU.json';
import zhCN from '../../i18n/locales/zh-CN.json';
import deDE from '../../i18n/locales/de-DE.json';
import frFR from '../../i18n/locales/fr-FR.json';
import itIT from '../../i18n/locales/it-IT.json';
import zhHant from '../../i18n/locales/zh-Hant.json';

type ArticleCard = PublicComponents['schemas']['ArticleCard'];

// Article feeds (WP17): one RSS 2.0 feed per language at <prefix>/articles/feed.xml, holding the
// newest articles whose text is in that language. Channel copy comes from the site's i18n files.
export const FEED_SIZE = 20;

const MESSAGES: Record<Locale, Pick<typeof zhCN, 'articles'>> = {
  'en-US': enUS,
  'zh-CN': zhCN,
  'ja-JP': jaJP,
  'es-ES': esES,
  'pt-BR': ptBR,
  'ru-RU': ruRU,
  'de-DE': deDE,
  'fr-FR': frFR,
  'it-IT': itIT,
  'zh-Hant': zhHant
};

/** The language a feed path prefix stands for; the default language has no prefix. */
export function feedLocale(prefix: string | undefined): Locale | undefined {
  return SITE_LOCALES.find(locale => LOCALE_METADATA[locale].prefix === (prefix ? `/${prefix}` : ''));
}

function escape(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;');
}

export function renderFeed(input: { locale: Locale; siteUrl: string; siteName: string; cards: ArticleCard[] }): string {
  const { locale, siteUrl, siteName } = input;
  const messages = MESSAGES[locale];
  const prefix = LOCALE_METADATA[locale].prefix;
  const list = `${siteUrl}${prefix}/articles`;
  const cards = input.cards.filter(card => card.locale === locale).slice(0, FEED_SIZE);
  const items = cards.map(card => {
    const link = `${list}/${card.slug}`;
    return [
      '<item>',
      `<title>${escape(card.title)}</title>`,
      `<link>${escape(link)}</link>`,
      `<guid isPermaLink="true">${escape(link)}</guid>`,
      `<pubDate>${new Date(card.publishedAt).toUTCString()}</pubDate>`,
      `<dc:creator>${escape(card.author?.name ?? messages.articles.editorial)}</dc:creator>`,
      `<category>${escape(messages.articles.types[card.type])}</category>`,
      ...(card.summary ? [`<description>${escape(card.summary)}</description>`] : []),
      '</item>'
    ].join('');
  });
  const updated = cards[0] ? new Date(cards[0].publishedAt).toUTCString() : undefined;
  return [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/elements/1.1/">',
    '<channel>',
    `<title>${escape(messages.articles.feedTitle.replace('OpenNavo', siteName))}</title>`,
    `<link>${escape(list)}</link>`,
    `<description>${escape(messages.articles.description)}</description>`,
    `<language>${locale}</language>`,
    `<atom:link href="${escape(`${list}/feed.xml`)}" rel="self" type="application/rss+xml"/>`,
    ...(updated ? [`<lastBuildDate>${updated}</lastBuildDate>`] : []),
    ...items,
    '</channel>',
    '</rss>',
    ''
  ].join('\n');
}

/** Feed response for one language; the API decides what is published. */
export async function articleFeed(event: H3Event, locale: Locale) {
  const config = useRuntimeConfig();
  const apiBase = (config.apiBaseInternal || config.public.apiBase).replace(/\/$/, '');
  const settings = await getSiteSettings();
  const page = await $fetch<{ data: { records: ArticleCard[] } }>(`${apiBase}/articles`, {
    query: { locale, current: 1, size: 50 }
  });
  setHeader(event, 'content-type', 'application/rss+xml; charset=utf-8');
  setHeader(event, 'x-robots-tag', 'noindex');
  return renderFeed({
    locale,
    siteUrl: config.public.siteUrl.replace(/\/$/, ''),
    siteName: settings.identity.name || 'OpenNavo',
    cards: page.data.records
  });
}
