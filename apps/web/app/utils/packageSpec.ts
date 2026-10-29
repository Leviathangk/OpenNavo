import { formatDate } from './packageDate';
import { formatRepoStars, repoMaintenanceStatus, repoSignalDays, toPackageRepoSignals } from '@opennavo/ui';
import type { OnPackageSpecRow, PackageRepoSignals } from '@opennavo/ui';
import { architectures, displayUrl } from '@opennavo/ui';
import {
  chipsOf,
  currentRows,
  macosRange,
  macosText,
  oldestCheck,
  priceAmount,
  priceUsage,
  quickFacts,
  verifiedIdentities,
  verifiedPrices
} from './packageFacts';
import type { PackagePage } from './packageFacts';
import { httpsUrl } from './linkApp';
import type { SiteLocale } from '@opennavo/shared';
import type { Translate } from './seoTitles';

export interface PackageSpecModel {
  rows: OnPackageSpecRow[];
  checkedAt: { iso: string; label: string } | null;
  repoSignals: PackageRepoSignals | null;
}

function repoText(
  signals: PackageRepoSignals | null,
  labels: {
    updated: (days: number) => string;
    unmaintained: string;
    archived: string;
  },
  now: number
): { value: string; badge?: OnPackageSpecRow['badge'] } {
  if (!signals) return { value: '' };
  const parts: string[] = [];
  const age = repoSignalDays(signals, now);
  const maintenance = repoMaintenanceStatus(signals, now);
  if (age !== null && maintenance !== 'archived') parts.push(labels.updated(age));
  if (maintenance === 'unmaintained')
    return { value: parts.filter(Boolean).join(' · '), badge: { label: labels.unmaintained, tone: 'warning' } };
  if (maintenance === 'archived')
    return { value: parts.filter(Boolean).join(' · '), badge: { label: labels.archived, tone: 'danger' } };
  return { value: parts.filter(Boolean).join(' · ') };
}

export function packageSpec(pkg: PackagePage, t: Translate, locale: SiteLocale, now: number): PackageSpecModel {
  const signals = toPackageRepoSignals(pkg);
  const rows: OnPackageSpecRow[] = [];
  const quick = quickFacts(pkg, t, locale);
  const originalRows = (keys: string[]) =>
    quick.rows
      .filter(row => keys.includes(row.key))
      .map(row => ({ key: row.key, label: row.label, value: row.value, mono: row.code }));
  rows.push(...originalRows(['version', 'source', 'files', 'location']));
  const prices = verifiedPrices(pkg);
  const price = prices.length
    ? [
        ...new Set(prices.map(item => [priceUsage(item, t), priceAmount(item, t, locale)].filter(Boolean).join(' · ')))
      ].join(' / ')
    : pkg.link && pkg.link.pricing !== 'unknown'
      ? t(`package.link.pricing.${pkg.link.pricing}`)
      : '';
  if (price) rows.push({ key: 'price', label: t('package.spec.price'), value: price });

  const range = macosText(macosRange(pkg), t);
  if (!pkg.isFont && range) rows.push({ key: 'macos', label: t('package.spec.system'), value: range });

  const chipValues = chipsOf(pkg);
  const chips = chipValues.length
    ? chipValues.map(arch => t(`package.arch.${arch}`)).join(' · ')
    : architectures(pkg.supports)
        .map(arch => t(`package.arch.${arch}`))
        .join(' · ');
  if (!pkg.isFont && chips) rows.push({ key: 'chip', label: t('package.spec.chip'), value: chips });

  if (pkg.kind === 'cask')
    rows.push({
      key: 'auto',
      label: t('package.spec.autoUpdates'),
      value: t(pkg.autoUpdates ? 'package.spec.yes' : 'package.spec.no')
    });

  const repository = pkg.repository;
  const osi = pkg.openSource;
  const github = pkg.kind === 'link' && pkg.link?.github;
  const repositoryUrl = repository?.url ?? osi?.url ?? (github ? `https://github.com/${github.repo}` : undefined);
  if (osi || repository || github) {
    const repo = repoText(
      signals,
      {
        updated: days => t('package.repo.updated', { days }),
        unmaintained: t('package.repo.unmaintained'),
        archived: t('package.repo.archived')
      },
      now
    );
    rows.push({
      key: 'openSource',
      label: t('package.spec.repository'),
      value: [osi ? t('package.spec.openSource') : t('package.spec.githubRepository'), osi?.license, repo.value]
        .filter(Boolean)
        .join(' · '),
      href: repositoryUrl,
      stars:
        signals?.stars === undefined
          ? undefined
          : {
              label: `★${formatRepoStars(signals.stars, locale)}`,
              href: repository?.url ?? undefined,
              checked: signals.checkedAt
                ? t('package.repo.checked', { date: formatDate(signals.checkedAt, { locale }) })
                : undefined
            },
      badge: repo.badge
    });
  }

  rows.push(...originalRows(['developer', 'teamId', 'license', 'homebrew', 'names']));
  if (pkg.kind === 'link' && pkg.bundleIds?.length)
    rows.push({ key: 'bundle', label: 'Bundle ID', value: pkg.bundleIds.join(' '), mono: true });
  const homepage = httpsUrl(pkg.kind === 'link' ? pkg.link?.homepage : pkg.homepage) ?? httpsUrl(pkg.homepage);
  if (homepage)
    rows.push({ key: 'homepage', label: t('package.spec.homepage'), value: displayUrl(homepage), href: homepage });
  rows.push({ key: 'token', label: t('package.spec.token'), value: pkg.token, mono: true });

  const factCheckDates = [
    ...currentRows(pkg).map(row => row.evidence),
    ...prices.map(item => item.evidence),
    ...verifiedIdentities(pkg).map(item => item.evidence)
  ];
  const checkedAt = oldestCheck(factCheckDates);
  return {
    rows,
    checkedAt: checkedAt ? { iso: checkedAt.slice(0, 10), label: formatDate(checkedAt, { locale }) } : null,
    repoSignals: signals
  };
}
