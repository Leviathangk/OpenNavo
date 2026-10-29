import type { PublicComponents } from '@opennavo/api';
import { createError, sendRedirect, type H3Event } from 'h3';
import { formatCount, installCommand, LOCALE_METADATA, sharedMessages } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import { fetchPackageDetail, publicApi } from './publicApi';
import { siteMessage } from './siteCopy';
import {
  currentVersion,
  factSources,
  isVerified,
  quickFacts,
  hasVersion,
  macosText,
  priceAmount,
  priceEdition,
  priceRegionName,
  priceUsage,
  shownPrices,
  shownRequirements,
  slimPackage,
  uninstallFacts,
  verifiedAlternatives,
  verifiedIdentities
} from '../../app/utils/packageFacts';
import type { FactEvidence } from '../../app/utils/packageFacts';
import { packagePath } from '../../app/utils/packageView';

type PackageDetail = PublicComponents['schemas']['PackageDetail'];
type RequirementFact = PublicComponents['schemas']['RequirementFact'];
type ReleaseEntry = PublicComponents['schemas']['ReleaseEntry'];

// Markdown alternate of an app overview (X4, experiment): the facts the page shows, in the page language,
// as plain Markdown for agents that ask for it. Same data as the page (the detail API), nothing added:
// prices and requirements only where a source backs them, links to the site's own pages.

/** Text inside Markdown: characters that would start markup or HTML are escaped. */
export function mdText(text: string): string {
  return text
    .replace(/[\\`*[\]<>]/g, char => `\\${char}`)
    .replace(/\s+/g, ' ')
    .trim();
}

/** Body Markdown from the API nested inside this document: every heading moves down `levels` (h6 at most). */
export function nestedMarkdown(source: string, levels = 1): string {
  return source
    .split(/\r?\n/)
    .map(line => {
      const heading = /^(#{1,6})(\s.*)$/.exec(line);
      return heading ? `${'#'.repeat(Math.min(6, (heading[1] ?? '').length + levels))}${heading[2]}` : line;
    })
    .join('\n')
    .trim();
}

/** Keep the table's applicability and evidence beside each claim; never infer expiry on this server. */
function evidenceText(evidence: FactEvidence, locale: SiteLocale, uninstall = false): string {
  const key = uninstall
    ? evidence.status === 'stale'
      ? 'package.uninstall.asOf'
      : 'package.uninstall.checked'
    : evidence.status === 'stale'
      ? 'package.facts.asOf'
      : 'package.facts.checkedOn';
  return [
    evidence.sourceUrl,
    evidence.checkedAt ? siteMessage(locale, key, { date: evidence.checkedAt.slice(0, 10) }) : '',
    evidence.status === 'needs_review' ? siteMessage(locale, 'package.facts.pending') : '',
    evidence.status === 'stale' ? siteMessage(locale, 'package.facts.stale') : ''
  ]
    .filter(Boolean)
    .join(' · ');
}

function requirementLine(row: RequirementFact, locale: SiteLocale): string {
  const t = (key: string, named?: Record<string, unknown>) =>
    siteMessage(locale, key, named as Record<string, string | number>);
  const range = macosText({ minimum: row.minimumMacOS, maximum: row.maximumMacOS }, t);
  const chips = row.architectures.length
    ? row.architectures
        .map(arch => (arch === 'arm64' || arch === 'x86_64' ? t(`package.arch.${arch}`) : arch))
        .join(', ')
    : t('package.requirements.allChips');
  return [
    range ||
      (row.platformMacOS.length ? t('package.requirements.builtFor', { versions: row.platformMacOS.join(', ') }) : ''),
    `${t('package.requirements.version')}: ${row.versions.join(', ') || '—'}`,
    chips,
    row.appleSilicon === 'yes'
      ? t('package.facts.support.native')
      : row.appleSilicon === 'no'
        ? t('package.requirements.notNative')
        : '',
    evidenceText(row.evidence, locale)
  ]
    .filter(Boolean)
    .map(mdText)
    .join(' · ');
}

function releaseLines(entry: ReleaseEntry): string[] {
  const lines: string[] = [];
  if (entry.summary) lines.push(mdText(entry.summary), '');
  for (const section of entry.sections) {
    lines.push(`**${mdText(section.area)}**`, '');
    for (const item of section.items) lines.push(`- ${item.replace(/\s+/g, ' ').trim()}`);
    lines.push('');
  }
  // The release is an h3 under the h2 section, so its own headings start at h4.
  if (!entry.sections.length && entry.bodyMarkdown) lines.push(nestedMarkdown(entry.bodyMarkdown, 2), '');
  return lines;
}

const day = (iso: string | null | undefined) => (iso ? iso.slice(0, 10) : null);

export interface PackageMarkdownOptions {
  siteUrl: string;
  locale: SiteLocale;
}

/** The overview's facts as a Markdown document. */
export function renderPackageMarkdown(pkg: PackageDetail, { siteUrl, locale }: PackageMarkdownOptions): string {
  const prefix = LOCALE_METADATA[locale].prefix;
  const page = `${siteUrl}${prefix}/apps/${pkg.token}`;
  const m = (key: string, params?: Record<string, unknown>) =>
    siteMessage(locale, key, params as Record<string, string | number>);
  const model = slimPackage(pkg);
  const lines: string[] = [`# ${mdText(pkg.displayName)}`, ''];
  if (pkg.summary) lines.push(mdText(pkg.summary), '');
  if (pkg.isFont) lines.push(m('package.answer.font', { name: mdText(pkg.displayName) }), '');
  lines.push(`> ${m('markdown.note', { name: mdText(pkg.displayName) })} ${page}`, '');

  const facts: [string, string][] = [];
  const version = currentVersion(model);
  if (hasVersion(version)) facts.push([m('package.stats.latest'), `\`${mdText(version)}\``]);
  const developer = verifiedIdentities(model).find(item => item.developer)?.developer || pkg.developer;
  if (developer) facts.push([m('markdown.developer'), mdText(developer)]);
  const homepage = pkg.homepage ?? pkg.link?.homepage;
  if (homepage && /^https?:\/\//.test(homepage)) facts.push([m('package.homepage'), homepage]);
  if (pkg.license) facts.push([m('package.info.license'), mdText(pkg.license)]);
  if (pkg.categories.length)
    facts.push([
      m('categories.title'),
      pkg.categories
        .map(category => `[${mdText(category.name)}](${siteUrl}${prefix}/categories/${category.slug})`)
        .join(', ')
    ]);
  if (pkg.kind !== 'link')
    facts.push([m('markdown.install'), `\`${pkg.installCommand || installCommand(pkg.kind, pkg.token)}\``]);
  else if (pkg.link?.downloadUrl) facts.push([m('package.link.downloadTitle'), pkg.link.downloadUrl]);
  if (pkg.kind !== 'link') facts.push([m('package.stats.installs30d'), formatCount(pkg.installs.d30, { locale })]);
  const overview = model.facts.overview;
  const homebrewStatus = quickFacts(model, m, locale).rows.find(row => row.key === 'homebrew');
  if (homebrewStatus) facts.push(['Homebrew', homebrewStatus.value]);
  if (pkg.homebrewStatus?.state === 'deprecated')
    facts.push([
      m('package.deprecated'),
      [
        m(
          `package.faq.reasons.${['fails_gatekeeper_check', 'unsigned', 'discontinued', 'unmaintained', 'no_longer_available', 'no_longer_meets_criteria', 'moved_to_mas'].includes(pkg.homebrewStatus.reason || '') ? pkg.homebrewStatus.reason : 'other'}`
        ),
        pkg.homebrewStatus.date
      ]
        .filter(Boolean)
        .join(' · ')
    ]);
  if (overview.aliases?.length) facts.push([m('package.details.aliases'), overview.aliases.map(mdText).join(', ')]);
  if (overview.downloadUrl) facts.push([m('package.details.download'), overview.downloadUrl]);
  if (overview.sha256) facts.push(['SHA-256', `\`${overview.sha256}\``]);
  if (overview.artifacts?.binaries.length)
    facts.push([m('package.info.binaries'), overview.artifacts.binaries.map(mdText).join(', ')]);
  if (overview.artifacts?.pkgs.length)
    facts.push([m('package.details.pkgs'), overview.artifacts.pkgs.map(mdText).join(', ')]);
  for (const [label, value] of facts) lines.push(`- **${mdText(label)}**: ${value}`);
  lines.push('');

  if (overview.sha256) lines.push(m('package.guide.sha'), '');
  if (overview.caveats) lines.push(`## ${m('package.caveats')}`, '', mdText(overview.caveats), '');
  if (overview.artifacts?.entries?.length) {
    const declaration = JSON.stringify(overview.artifacts.entries, null, 2);
    const fence = '`'.repeat(Math.max(3, ...[...declaration.matchAll(/`+/g)].map(match => match[0].length + 1)));
    lines.push(`## ${sharedMessages[locale].cask.declarations}`, '', `${fence}json`, declaration, fence, '');
  }
  const about = pkg.description?.trim();
  if (about) lines.push(`## ${m('markdown.about')}`, '', nestedMarkdown(about), '');

  const requirements = shownRequirements(model);
  if (requirements.length) {
    lines.push(`## ${m('package.requirements.title')}`, '');
    for (const row of requirements) {
      lines.push(`- ${requirementLine(row, locale)}`);
      if (row.downloadUrl) lines.push(`  - ${row.downloadUrl}`);
      if (row.sha256) lines.push(`  - SHA-256: \`${row.sha256}\``);
    }
    lines.push('');
  }

  // Same rows and formatting as HTML: stale amounts remain explicitly historical, unknown claims stay absent.
  const prices = shownPrices(model).map(price => {
    const parts = [priceUsage(price, m), priceAmount(price, m, locale)].filter(Boolean);
    const scope = [priceRegionName(price.region, locale), priceEdition(price)].filter(Boolean).join(', ');
    const storeNote = price.channel === 'app_store' && price.usage === 'unknown' ? m('package.price.storeNote') : '';
    return `- ${mdText(parts.join(' · ') || '—')}${scope ? ` (${mdText(scope)})` : ''} — ${mdText(m(`package.price.channels.${price.channel}`))}: ${price.purchaseUrl} · ${mdText(evidenceText(price.evidence, locale))}${storeNote ? ` · ${mdText(storeNote)}` : ''}`;
  });
  if (prices.length) lines.push(`## ${m('package.facts.price')}`, '', ...prices, '');

  const alternatives = verifiedAlternatives(model);
  if (alternatives.length) {
    lines.push(`## ${m('package.alternatives.titleFor', { name: mdText(pkg.displayName) })}`, '');
    for (const item of alternatives) {
      const reasons = item.reasons.map(reason => m(`package.alternatives.reasons.${reason}`)).join(', ');
      lines.push(
        `- [${mdText(item.package.displayName)}](${siteUrl}${prefix}${packagePath(item.package.token)})${reasons ? ` — ${mdText(reasons)}` : ''}`
      );
      for (const attribute of item.attributes ?? []) {
        const key =
          attribute.name === 'import_formats'
            ? 'package.compare.imports'
            : attribute.name === 'export_formats'
              ? 'package.compare.exports'
              : `package.alternatives.reasons.${attribute.name}`;
        const value =
          attribute.formats?.join(', ') ||
          (attribute.booleanValue === undefined
            ? ''
            : m(attribute.booleanValue ? 'package.facts.yes' : 'package.facts.no'));
        if (value) lines.push(`  - ${m(key)}: ${mdText(value)} · ${mdText(evidenceText(attribute.evidence, locale))}`);
      }
      const migration = item.migration;
      if (migration && isVerified(migration.evidence)) {
        const steps = migration.steps.filter(step => isVerified(step.evidence));
        if (steps.length) {
          lines.push(
            '',
            `### ${m('package.compare.migrationTitle', { from: mdText(pkg.displayName), to: mdText(item.package.displayName) })}`,
            ''
          );
          for (const step of steps)
            lines.push(
              `- ${m(`package.compare.actions.${step.action}`)}: ${mdText(step.text)} · ${mdText(evidenceText(step.evidence, locale))}`
            );
          lines.push(m('package.compare.migrationNote', { name: mdText(pkg.displayName) }));
        }
      }
    }
    lines.push('');
  }

  if (!pkg.isFont) {
    const identities = verifiedIdentities(model);
    const vulnerabilities = (model.facts.security?.vulnerabilities ?? []).filter(item => isVerified(item.evidence));
    if (identities.length || vulnerabilities.length) {
      lines.push(
        `## ${m('package.safety.title', { name: mdText(pkg.displayName) })}`,
        '',
        m('package.safety.hint'),
        ''
      );
      for (const identity of identities) {
        const values = [identity.developer, identity.teamId, identity.repositoryUrl]
          .filter(Boolean)
          .map(value => mdText(value!));
        if (identity.openSource !== 'unknown')
          values.push(
            `${m('package.safety.openSource')}: ${m(identity.openSource === 'yes' ? 'package.facts.yes' : 'package.facts.no')}`
          );
        values.push(
          ...identity.channels.map(channel =>
            channel === 'homebrew' ? 'Homebrew' : m(`package.price.channels.${channel}`)
          )
        );
        lines.push(`- ${values.join(' · ')} — ${mdText(evidenceText(identity.evidence, locale))}`);
      }
      for (const item of vulnerabilities)
        lines.push(
          `- ${mdText(item.id)} · ${m('package.safety.cveVersion', { version: mdText(item.appVersion) })} · ${item.sourceUrl} · ${mdText(evidenceText(item.evidence, locale))}`
        );
      if (vulnerabilities.length) lines.push(m('package.safety.cveNote'));
      lines.push('');
    }
  }

  const regular = uninstallFacts(model, 'uninstall');
  const zap = uninstallFacts(model, 'zap');
  if (pkg.kind === 'cask' || regular.length || zap.length) {
    lines.push(`## ${m('markdown.uninstall')}`, '');
    if (pkg.kind === 'cask')
      lines.push(`- \`brew uninstall --cask ${pkg.token}\``, '', m('package.guide.uninstall'), '');
    const declarations = (items: typeof regular) => {
      for (const item of items)
        lines.push(
          `- ${mdText(m(`package.uninstall.kinds.${item.kind}`))}: \`${item.value.replace(/`/g, "'")}\` — ${mdText(evidenceText(item.evidence, locale, true))}`
        );
      lines.push('');
    };
    if (regular.length) {
      lines.push(`### ${m('package.uninstall.also')}`, '');
      declarations(regular);
    }
    if (zap.length) {
      lines.push(`### ${m('package.guide.zapTitle')}`, '');
      if (pkg.kind === 'cask') lines.push(`- \`brew uninstall --zap --cask ${pkg.token}\``, '');
      // Keep the user-data risk and the choice to omit --zap immediately beside the command.
      lines.push(m('package.guide.zap'), '');
      if (pkg.kind === 'cask') lines.push(m('package.guide.uninstall'), '');
      declarations(zap);
    }
  }

  const latest = pkg.latestRelease;
  if (latest?.hasNotes) {
    const date = day(latest.publishedAt ?? latest.brewCommittedAt);
    lines.push(`## ${m('markdown.latestRelease')}`, '', `### ${mdText(latest.version)}${date ? ` (${date})` : ''}`, '');
    lines.push(...releaseLines(latest));
  }

  lines.push(`## ${m('package.facts.title')}`, '', `- ${page}`, `- ${page}/versions`);
  for (const source of factSources(model))
    lines.push(
      `- ${source.url}${source.checkedAt ? ` · ${m(source.stale ? 'package.facts.asOf' : 'package.facts.checkedOn', { date: source.checkedAt.slice(0, 10) })}` : ''}`
    );
  return `${lines
    .join('\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()}\n`;
}

/** The Markdown response for an app; 404 when the app does not exist, like its page. */
export async function packageMarkdown(event: H3Event, locale: SiteLocale, token: string): Promise<string | void> {
  const api = publicApi(event);
  const pkg = await fetchPackageDetail(api, token, locale);
  if (!pkg || (pkg.indexing && (pkg.indexing.reason === 'hidden' || !pkg.indexing.locales.includes(locale))))
    throw createError({ statusCode: 404, statusMessage: 'Page not found' });
  if (pkg.token !== token) return sendRedirect(event, `${LOCALE_METADATA[locale].prefix}/apps/${pkg.token}.md`, 301);
  return renderPackageMarkdown(pkg, { siteUrl: api.siteUrl, locale });
}
