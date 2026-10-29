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

function mergeRepoSignals(pkg: PackagePage): PackageRepoSignals | null {
  const fallback = pkg.openSource?.url ?? pkg.repoUrl;
  const adapted = toPackageRepoSignals(pkg, fallback);
  if (!adapted && pkg.openSource) {
    return {
      repoUrl: fallback ?? undefined,
      archived: pkg.openSource.archived,
      checkedAt: pkg.openSource.checkedAt
    };
  }
  if (!adapted) return null;
  return pkg.openSource?.archived && adapted.archived !== true
    ? { ...adapted, archived: true, checkedAt: adapted.checkedAt ?? pkg.openSource.checkedAt }
    : { ...adapted, checkedAt: adapted.checkedAt ?? pkg.openSource?.checkedAt };
}

function repoText(
  signals: PackageRepoSignals | null,
  labels: {
    stars: string;
    updated: (days: number) => string;
    unmaintained: string;
    archived: string;
  }
): { value: string; badge?: OnPackageSpecRow['badge'] } {
  if (!signals) return { value: '' };
  const parts = [signals.stars === undefined ? '' : `${labels.stars} ${formatRepoStars(signals.stars)}`];
  const age = repoSignalDays(signals);
  const maintenance = repoMaintenanceStatus(signals);
  if (age !== null && maintenance !== 'archived') parts.push(labels.updated(age));
  if (maintenance === 'unmaintained')
    return { value: parts.filter(Boolean).join(' · '), badge: { label: labels.unmaintained, tone: 'warning' } };
  if (maintenance === 'archived')
    return { value: parts.filter(Boolean).join(' · '), badge: { label: labels.archived, tone: 'danger' } };
  return { value: parts.filter(Boolean).join(' · ') };
}

export function packageSpec(pkg: PackagePage, t: Translate, locale: SiteLocale): PackageSpecModel {
  const signals = mergeRepoSignals(pkg);
  const rows: OnPackageSpecRow[] = [];
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
  if (range) rows.push({ key: 'macos', label: t('package.spec.system'), value: range });

  const chipValues = chipsOf(pkg);
  const chips = chipValues.length
    ? chipValues.map(arch => t(`package.arch.${arch}`)).join(' · ')
    : architectures(pkg.supports)
        .map(arch => t(`package.arch.${arch}`))
        .join(' · ');
  if (chips) rows.push({ key: 'chip', label: t('package.spec.chip'), value: chips });

  if (pkg.kind === 'cask')
    rows.push({
      key: 'auto',
      label: t('package.spec.autoUpdates'),
      value: t(pkg.autoUpdates ? 'package.spec.yes' : 'package.spec.no')
    });

  const identity = verifiedIdentities(pkg).find(item => item.openSource === 'yes');
  const repository =
    pkg.openSource ??
    (identity?.repositoryUrl
      ? {
          url: identity.repositoryUrl,
          license: pkg.license ?? '',
          repo: identity.repositoryUrl.replace('https://github.com/', ''),
          archived: false,
          checkedAt: identity.evidence.checkedAt ?? ''
        }
      : null);
  if (repository) {
    const repo = repoText(signals, {
      stars: t('package.repo.stars'),
      updated: days => t('package.repo.updated', { days }),
      unmaintained: t('package.repo.unmaintained'),
      archived: t('package.repo.archived')
    });
    rows.push({
      key: 'openSource',
      label: t('package.spec.openSource'),
      value: [t('package.spec.yes'), repository.license, repo.value].filter(Boolean).join(' · '),
      href: repository.url,
      badge: repo.badge
    });
  }

  if (pkg.developer) rows.push({ key: 'developer', label: t('package.spec.developer'), value: pkg.developer });
  const homepage = httpsUrl(pkg.homepage);
  if (homepage)
    rows.push({ key: 'homepage', label: t('package.spec.homepage'), value: displayUrl(homepage), href: homepage });
  rows.push({ key: 'token', label: t('package.spec.token'), value: pkg.token, mono: true });

  const factCheckDates = [
    ...currentRows(pkg).map(row => row.evidence),
    ...prices.map(item => item.evidence),
    ...verifiedIdentities(pkg).map(item => item.evidence)
  ];
  const checkedAt = [oldestCheck(factCheckDates), pkg.openSource?.checkedAt, signals?.checkedAt]
    .filter((value): value is string => Boolean(value))
    .sort((a, b) => Date.parse(a) - Date.parse(b))[0];
  return {
    rows,
    checkedAt: checkedAt ? { iso: checkedAt.slice(0, 10), label: formatDate(checkedAt, { locale }) } : null,
    repoSignals: signals
  };
}
