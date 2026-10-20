import { APP_LOCALES, BRAND, SITE_LOCALES } from '@opennavo/shared';

/**
 * What OpenNavo says about itself, the same on the homepage, in its structured data, in the README's
 * definition block and in /llms.txt (G3/G4/G6). Only stable facts live here: the release, its system
 * requirements and the languages open right now come from the API and site settings at request time.
 */
export const BRAND_FACTS = {
  name: BRAND.name,
  /** SPDX identifier; the root package.json is the authority. */
  license: BRAND.license,
  repository: BRAND.repository,
  /** Languages the website is translated into; settings decide which of them are open. */
  siteLanguages: SITE_LOCALES.length,
  /** Languages of the Mac client (and the admin). */
  appLanguages: APP_LOCALES.length,
  /** Message key of the one-sentence definition every surface shows unchanged. */
  definitionKey: 'landing.about.definition'
} as const;
