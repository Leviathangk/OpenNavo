import { LOCALE_METADATA } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import { BRAND_FACTS } from '../../shared/utils/brandFacts';
import { siteMessage } from './siteCopy';

/** A stable Markdown projection of the landing page for agents that negotiate text/markdown. */
export function renderHomeMarkdown(siteUrl: string, locale: SiteLocale): string {
  const origin = new URL(siteUrl).origin;
  const prefix = LOCALE_METADATA[locale].prefix;
  const message = (key: string) => siteMessage(locale, key);
  const links = [
    [message('nav.apps'), `${prefix}/apps`],
    [message('nav.categories'), `${prefix}/categories`],
    [message('nav.articles'), `${prefix}/articles`],
    [message('nav.security'), `${prefix}/security`],
    [message('nav.download'), `${prefix}/download`]
  ];
  return [
    `# ${message('site.title')}`,
    '',
    `> ${message(BRAND_FACTS.definitionKey)}`,
    '',
    `## ${message('home.introTitle')}`,
    '',
    message(BRAND_FACTS.definitionKey),
    '',
    ...links.map(([label, path]) => `- [${label}](${origin}${path})`),
    ''
  ].join('\n');
}
