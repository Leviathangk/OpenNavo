import { describe, expect, it, vi } from 'vitest';
import { LOCALE_METADATA, SITE_LOCALES, sharedMessages, type SiteLocale } from '@opennavo/shared';
import type { PackageDetail } from '@opennavo/api';
import { faqItems } from '../app/utils/packageFaq';
import { softwareAppNode } from '../app/utils/structuredData';
import {
  currentVersion,
  hasVersion,
  macosRange,
  macosText,
  priceAmount,
  priceEdition,
  priceRegionName,
  shownPrices,
  shownRequirements,
  slimPackage,
  uninstallFacts
} from '../app/utils/packageFacts';
import { renderPackageMarkdown, mdText } from '../server/utils/packageMarkdown';
import { siteMessage } from '../server/utils/siteCopy';
import { declaringApps, usableContent } from '../app/utils/backgroundItems';
import { GEO_SAMPLES, GEO_NOW, GEO_CHECKED, GEO_HASH } from './geoFixtures';
import { plainHtml, renderGeo, translator } from './geoRendering';

const origin = 'https://opennavo.example';
async function outputs(detail: PackageDetail, locale: SiteLocale = 'en-US') {
  const page = slimPackage(detail);
  const t = translator(locale);
  return {
    page,
    t,
    html: await renderGeo(detail, locale),
    faq: faqItems(page, t, locale),
    schema: softwareAppNode(page, `${origin}${LOCALE_METADATA[locale].prefix}/apps/${page.token}`, t, locale),
    md: renderPackageMarkdown(detail, { siteUrl: origin, locale })
  };
}
type Output = Awaited<ReturnType<typeof outputs>>;
function sharedAssertions(out: Output, locale: SiteLocale) {
  const { page, html, faq, schema, md, t } = out;
  // The FAQ component renders exactly the public answers, including collapsed bodies.
  const faqText = plainHtml(html.faq!);
  for (const item of faq) {
    expect(faqText).toContain(item.question.replace(/\s+/g, ' '));
    expect(faqText).toContain(item.answer.replace(/`|\*\*/g, '').replace(/\s+/g, ' '));
  }
  expect(md).toContain(`${origin}${LOCALE_METADATA[locale].prefix}/apps/${page.token}`);
  if (!shownPrices(page).length) {
    expect(html.price).not.toContain('<table');
    expect(html.price).not.toContain('id="price"');
  }
  for (const price of shownPrices(page)) {
    const amount = priceAmount(price, t, locale);
    for (const value of [amount, priceEdition(price), priceRegionName(price.region, locale)].filter(Boolean)) {
      expect(plainHtml(html.price!)).toContain(value.replace(/\s+/g, ' '));
      expect(md).toContain(mdText(value));
    }
    expect(html.price).toContain(price.evidence.checkedAt!.slice(0, 10));
    expect(md).toContain(price.evidence.checkedAt!.slice(0, 10));
    if (price.evidence.status === 'stale') {
      expect(plainHtml(html.price!)).toContain(t('package.facts.stale'));
      expect(md).toContain(t('package.facts.stale'));
    }
  }
  for (const row of shownRequirements(page)) {
    for (const version of row.versions) {
      expect(html.requirements).toContain(version);
      expect(md).toContain(`${t('package.requirements.version')}: ${row.versions.join(', ')}`);
    }
    expect(html.requirements).toContain(row.evidence.sourceUrl);
    expect(md).toContain(row.evidence.sourceUrl);
    if (row.evidence.status !== 'verified') {
      const key = row.evidence.status === 'stale' ? 'package.facts.stale' : 'package.facts.pending';
      expect(plainHtml(html.requirements!)).toContain(t(key));
      expect(md).toContain(t(key));
    }
  }
  for (const item of [...uninstallFacts(page, 'uninstall'), ...uninstallFacts(page, 'zap')]) {
    expect(plainHtml(html.uninstall!)).toContain(item.value);
    expect(md).toContain(item.value);
    expect(md).toContain(item.evidence.checkedAt!.slice(0, 10));
  }
  if (schema) {
    if (hasVersion(currentVersion(page))) expect(schema.softwareVersion).toBe(currentVersion(page));
    else expect(schema).not.toHaveProperty('softwareVersion');
    const range = macosText(macosRange(page), t);
    if (range) expect(schema.softwareRequirements).toBe(range);
    else expect(schema).not.toHaveProperty('softwareRequirements');
    const offers = Array.isArray(schema.offers) ? schema.offers : schema.offers ? [schema.offers] : [];
    expect(offers).toHaveLength(page.facts.offers.length);
    offers.forEach((offer, index) => {
      const price = page.facts.offers[index]!;
      expect(offer).toMatchObject({
        price: Number(price.amount),
        priceCurrency: price.currency,
        name: price.edition,
        eligibleRegion: { name: price.region }
      });
      if (price.period === 'monthly' || price.period === 'yearly')
        expect(offer.priceSpecification.referenceQuantity).toEqual({
          '@type': 'QuantitativeValue',
          value: 1,
          unitCode: price.period === 'monthly' ? 'MON' : 'ANN'
        });
      else expect(offer).not.toHaveProperty('priceSpecification');
    });
    expect(schema).not.toHaveProperty('aggregateRating');
    expect(schema).not.toHaveProperty('review');
    expect(schema).not.toHaveProperty('publisher');
  } else expect(page.isFont).toBe(true);
}

describe('G5 cross-export normalized facts', () => {
  it('keeps exactly the 30 stable scenario IDs', () => {
    expect(GEO_SAMPLES.map(item => item.id)).toEqual(
      Array.from({ length: 30 }, (_, i) => `G5-${String(i + 1).padStart(2, '0')}`)
    );
  });

  it.each(GEO_SAMPLES)('$id $label — HTML/FAQ/JSON-LD/MD', async sample => {
    const out = await outputs(sample.detail, sample.locale);
    sharedAssertions(out, sample.locale);
    const { page, html, faq, schema, md, t } = out;
    const answers = Object.fromEntries(faq.map(item => [item.key, item.answer]));
    switch (sample.id) {
      case 'G5-01':
        expect(plainHtml(html.lead!)).toContain('Example Studio');
        expect(md).toContain('Example Studio');
        expect(schema).toMatchObject({
          softwareVersion: '2.0.0',
          sameAs: page.homepage,
          identifier: { value: page.token }
        });
        expect(md).toContain(GEO_CHECKED.slice(0, 10));
        break;
      case 'G5-02':
        expect(md).not.toMatch(/brew (?:install|uninstall|upgrade)|30-day installs/);
        expect(faq.map(item => item.key)).not.toEqual(
          expect.arrayContaining(['price', 'chips', 'version', 'uninstall'])
        );
        expect(schema).not.toHaveProperty('identifier');
        expect(schema?.downloadUrl).toBe(page.link?.downloadUrl);
        break;
      case 'G5-03':
        expect(schema).toBeNull();
        expect(html.safety).toBe('');
        expect(faq.some(item => ['safety', 'chips'].includes(item.key))).toBe(false);
        expect(md).toContain(t('package.answer.font', { name: page.displayName }));
        break;
      case 'G5-04':
        expect(schema).not.toHaveProperty('softwareVersion');
        expect(answers.version).toBeUndefined();
        expect(md).not.toContain(`**${t('package.stats.latest')}**`);
        break;
      case 'G5-05':
        expect(plainHtml(html.quick!)).toContain(t('package.quick.homebrewPaused'));
        expect(md).toContain(t('package.faq.reasons.discontinued'));
        expect(md).toContain(page.installCommand);
        break;
      case 'G5-06':
        expect(schema?.alternateName).toContain('Former Fixture');
        expect(md).toContain('geo-fixture-old');
        expect(md).not.toContain('/apps/geo-fixture-old');
        break;
      case 'G5-07':
        // HTTP/API projection is tested separately; render helpers are intentionally not access-control gates.
        expect(page.indexing?.pages.overview.robots).toBe('noindex, follow');
        break;
      case 'G5-08':
      case 'G5-09':
      case 'G5-10':
      case 'G5-11':
      case 'G5-13':
      case 'G5-14':
        for (const price of page.facts.prices) {
          expect(answers.price).toContain(priceEdition(price));
          expect(answers.price).toContain(priceRegionName(price.region, sample.locale));
        }
        if (sample.id === 'G5-13') {
          expect(answers.price).toContain('14-day trial');
          expect(schema).not.toHaveProperty('offers');
        }
        if (sample.id === 'G5-14') expect(JSON.stringify(schema)).not.toContain('CNY');
        break;
      case 'G5-12':
        expect(schema).not.toHaveProperty('offers');
        expect(md).not.toContain(t('package.facts.types.free_with_iap'));
        expect(html.price).not.toContain('<table');
        expect(answers.price).toContain(t('package.faq.priceA.free_with_iap', { name: page.displayName }));
        break;
      case 'G5-15':
        expect(schema).toHaveProperty('offers');
        expect(answers.price).toBeTruthy();
        break;
      case 'G5-16':
      case 'G5-17':
        expect(html.safety).not.toContain('EXAMPLE123');
        expect(md).not.toContain('EXAMPLE123');
        expect(answers.openSource).toBeUndefined();
        expect(schema).not.toHaveProperty('author');
        break;
      case 'G5-18':
        expect(html.safety).toContain('EXAMPLE123');
        expect(md).toContain('EXAMPLE123');
        expect(answers.openSource).toContain('MIT');
        expect(md).toContain(t('package.safety.hint'));
        break;
      case 'G5-19':
        expect(html.safety).toContain('CVE-2026-99991');
        expect(md).toContain('CVE-2026-99991');
        expect(md).toContain(t('package.safety.cveVersion', { version: '2.0.0' }));
        expect(JSON.stringify(schema)).not.toContain('CVE-');
        break;
      case 'G5-20':
        expect(schema).not.toHaveProperty('softwareRequirements');
        expect(answers.chips).toContain(t('package.faq.chips.rosetta'));
        expect(md).toContain(t('package.requirements.notNative'));
        break;
      case 'G5-21':
        expect(schema?.softwareRequirements).toBe(t('package.facts.minimum', { version: '13' }));
        expect(md).not.toContain(t('package.facts.pending'));
        expect(html.requirements).not.toContain(t('package.facts.pending'));
        break;
      case 'G5-22':
        expect(schema?.softwareRequirements).toContain('13');
        break;
      case 'G5-23':
        expect(html.requirements).toContain('2026-10-19');
        expect(md).not.toContain('2020-01-01');
        expect(schema).toHaveProperty('softwareRequirements');
        break;
      case 'G5-24':
        expect(schema).not.toHaveProperty('softwareRequirements');
        expect(schema).toHaveProperty('offers');
        expect(answers.chips).toBeUndefined();
        expect(answers.uninstall).not.toContain('--zap');
        expect(md).toContain('2025-01-01');
        break;
      case 'G5-25': {
        const warning = t('package.guide.zap');
        expect(plainHtml(html.uninstall!)).toContain(warning.replace(/`/g, ''));
        expect(md.slice(md.indexOf('brew uninstall --zap'))).toContain(warning);
        expect(answers.uninstall).toContain(warning);
        expect(md).toContain(t('package.uninstall.checked', { date: '2026-10-19' }));
        break;
      }
      case 'G5-26':
        expect(md).toContain('com.example.shared');
        expect(answers.uninstall).not.toMatch(/safe to|belongs only/);
        expect(usableContent(sample.background!)).not.toBeNull();
        expect(declaringApps(sample.background!)).toHaveLength(2);
        const hiddenOwners = { ...sample.background!, apps: [] };
        expect(usableContent(hiddenOwners)).not.toBeNull();
        expect(declaringApps(hiddenOwners)).toEqual([]);
        break;
      case 'G5-27':
        expect(plainHtml(html.alternatives!)).toContain('JSON');
        expect(md).toContain('JSON');
        for (const step of page.facts.alternatives[0]!.migration!.steps) {
          expect(plainHtml(html.alternatives!)).toContain(step.text);
          expect(md).toContain(step.text);
        }
        expect(md).toContain(t('package.compare.migrationNote', { name: page.displayName }));
        expect(schema).not.toHaveProperty('offers');
        break;
      case 'G5-28':
        expect(html.details).toContain(GEO_HASH);
        expect(md).toContain(GEO_HASH);
        expect(plainHtml(html.install!)).toContain(page.facts.overview.caveats!);
        expect(md).toContain(mdText(page.facts.overview.caveats!));
        expect(md).toContain(t('package.guide.sha'));
        break;
      case 'G5-29':
        expect(schema?.description).toBe('Instantané publié H1');
        expect(md).toContain('Corps publié H1');
        expect(md).toContain(sharedMessages['fr-FR'].about.machineTranslated);
        expect(md).not.toContain('H2');
        break;
      case 'G5-30':
        expect(page.indexing?.locales).toEqual([...SITE_LOCALES]);
        break;
    }
  });

  for (const sample of GEO_SAMPLES)
    for (const variant of sample.variants) {
      it(`${sample.id} ${variant.label}`, async () => {
        const locale = variant.locale ?? sample.locale;
        if (variant.now) vi.useFakeTimers({ toFake: ['Date'] }).setSystemTime(new Date(variant.now));
        try {
          const out = await outputs(variant.detail, locale);
          sharedAssertions(out, locale);
          const keys = out.faq.map(item => item.key);
          if (sample.id === 'G5-12') {
            expect(out.md).toContain(siteMessage(locale, 'package.price.storeNote'));
            expect(out.schema).not.toHaveProperty('offers');
          }
          if (sample.id === 'G5-15') {
            expect(Date.now()).toBe(Date.parse(GEO_NOW));
            expect(keys).not.toContain('price');
            expect(out.schema).not.toHaveProperty('offers');
            expect(out.md).toContain(siteMessage(locale, 'package.facts.stale'));
          }
          if (sample.id === 'G5-17') expect(out.md).not.toContain('EXAMPLE123');
          if (sample.id === 'G5-19') {
            expect(out.md).not.toContain('CVE-');
            expect(out.html.safety).not.toContain('CVE-2026-99991');
          }
          if (sample.id === 'G5-21' || sample.id === 'G5-22') {
            expect(out.schema).not.toHaveProperty('softwareRequirements');
            expect(keys).not.toContain('chips');
          }
          if (sample.id === 'G5-28') {
            expect(out.md).not.toContain(GEO_HASH);
            expect(out.html.details).not.toContain(GEO_HASH);
          }
          if (sample.id === 'G5-29') expect(out.md).not.toContain(sharedMessages['fr-FR'].about.machineTranslated);
          if (sample.id === 'G5-30') expect(out.schema).toMatchObject({ offers: { eligibleRegion: { name: 'US' } } });
        } finally {
          if (variant.now) vi.useRealTimers();
        }
      });
    }
});
