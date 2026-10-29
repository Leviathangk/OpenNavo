import { formatDate, formatVersion } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import {
  architectures,
  formatRepoStars,
  repoMaintenanceStatus,
  repoSignalDays,
  toPackageRepoSignals
} from '@opennavo/ui';
import type { OnPackageSpecRow, PackageRepoSignals } from '@opennavo/ui';
import type { PackageDetail } from '@opennavo/api';
import type { LocalItem } from '@/ipc/client';

export interface DesktopPackageSpec {
  rows: OnPackageSpecRow[];
  checkedAt: { iso: string; label: string } | null;
  repoSignals: PackageRepoSignals | null;
}

type Translate = (key: string, named?: Record<string, unknown>) => string;

function repoSignals(detail: PackageDetail): PackageRepoSignals | null {
  const fallback = detail.openSource?.url ?? detail.repoUrl;
  const value = toPackageRepoSignals(detail, fallback);
  if (!value && detail.openSource)
    return {
      repoUrl: fallback ?? undefined,
      archived: detail.openSource.archived,
      checkedAt: detail.openSource.checkedAt
    };
  if (!value) return null;
  return detail.openSource?.archived && value.archived !== true
    ? { ...value, archived: true, checkedAt: value.checkedAt ?? detail.openSource.checkedAt }
    : { ...value, checkedAt: value.checkedAt ?? detail.openSource?.checkedAt };
}

function repoSummary(signals: PackageRepoSignals | null, t: Translate) {
  if (!signals) return { value: '', badge: undefined };
  const values = signals.stars === undefined ? [] : [`★ ${formatRepoStars(signals.stars)}`];
  const days = repoSignalDays(signals);
  const status = repoMaintenanceStatus(signals);
  if (days !== null && status !== 'archived') values.push(t('package.repo.updated', { days }));
  if (status === 'archived')
    return { value: values.join(' · '), badge: { label: t('package.repo.archived'), tone: 'danger' as const } };
  if (status === 'unmaintained')
    return { value: values.join(' · '), badge: { label: t('package.repo.unmaintained'), tone: 'warning' as const } };
  return { value: values.join(' · '), badge: undefined };
}

export function packageSpec(
  detail: PackageDetail,
  local: LocalItem | null | undefined,
  installed:
    | { installedVersion?: string | null; actualVersion?: string | null; appPaths?: string[] }
    | null
    | undefined,
  t: Translate,
  locale: SiteLocale
): DesktopPackageSpec {
  const signals = repoSignals(detail);
  const rows: OnPackageSpecRow[] = [];
  const price =
    detail.link?.pricing && detail.link.pricing !== 'unknown'
      ? t(`package.link.pricing.${detail.link.pricing}`)
      : detail.price
        ? t(`package.link.pricing.${detail.price.type}`)
        : '';
  if (price) rows.push({ key: 'price', label: t('package.spec.price'), value: price });

  const system = detail.dependsOn?.macos ?? detail.minMacos;
  if (system) rows.push({ key: 'system', label: t('package.spec.system'), value: system });
  const chips = architectures(detail.supports)
    .map(arch => t(`package.arch.${arch}`))
    .join(' · ');
  if (chips) rows.push({ key: 'chip', label: t('package.spec.chip'), value: chips });
  if (detail.kind === 'cask')
    rows.push({
      key: 'auto',
      label: t('package.spec.autoUpdates'),
      value: t(detail.autoUpdates ? 'package.spec.yes' : 'package.spec.no')
    });

  const repository = detail.openSource;
  if (repository) {
    const summary = repoSummary(signals, t);
    rows.push({
      key: 'openSource',
      label: t('package.spec.openSource'),
      value: [t('package.spec.yes'), repository.license, summary.value].filter(Boolean).join(' · '),
      href: repository.url,
      badge: summary.badge
    });
  }
  if (detail.developer) rows.push({ key: 'developer', label: t('package.spec.developer'), value: detail.developer });
  if (detail.homepage)
    rows.push({
      key: 'homepage',
      label: t('package.spec.homepage'),
      value: detail.homepage.replace(/^https?:\/\//, '').replace(/\/$/, ''),
      href: detail.homepage
    });
  rows.push({ key: 'token', label: t('package.spec.token'), value: detail.token, mono: true });
  const installedVersion = installed?.actualVersion ?? installed?.installedVersion;
  if (installedVersion)
    rows.push({
      key: 'installedVersion',
      label: t('package.spec.installedVersion'),
      value: formatVersion(installedVersion)
    });
  const location = installed?.appPaths?.filter(Boolean).join(' · ');
  if (location) rows.push({ key: 'location', label: t('package.spec.installLocation'), value: location, mono: true });

  const checked = signals?.checkedAt ?? detail.openSource?.checkedAt ?? null;
  return {
    rows,
    checkedAt: checked ? { iso: checked.slice(0, 10), label: formatDate(checked, { locale }) } : null,
    repoSignals: signals
  };
}
