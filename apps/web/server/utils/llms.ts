import { BRAND_FACTS } from '../../shared/utils/brandFacts';
import { siteMessage } from './siteCopy';

/**
 * A short English orientation, sharing the site's identity and linking only stable public HTML. Laid out as
 * llmstxt.org describes: the name as H1, the one-sentence definition as the summary blockquote, a detail
 * line, then the pages under an H2.
 */
export function renderLlms(siteUrl: string): string {
  const origin = new URL(siteUrl).origin;
  const message = (key: string) => siteMessage('en-US', key);
  const links = [
    [BRAND_FACTS.name, '/'],
    [message('nav.download'), '/download'],
    [message('nav.apps'), '/apps'],
    [message('nav.security'), '/security'],
    [message('nav.about'), '/about']
  ];
  return [
    `# ${BRAND_FACTS.name}`,
    '',
    `> ${message(BRAND_FACTS.definitionKey)}`,
    '',
    `${message('package.info.license')}: ${BRAND_FACTS.license}`,
    '',
    '## Main pages',
    '',
    ...links.map(([label, path]) => `- [${label}](${origin}${path})`),
    ''
  ].join('\n');
}
