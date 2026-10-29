import { formatDate, formatVersion } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import {
  architectures,
  formatRepoStars,
  repoMaintenanceStatus,
  repoSignalDays,
  toPackageRepoSignals
} from '@opennavo/ui';
import type { OnPackageSpecRow, PackageRepoSignals, PackageRepositoryInput } from '@opennavo/ui';
import type { PackageDetail } from '@opennavo/api';
import type { LocalItem } from '@/ipc/client';

export interface DesktopPackageSpec {
  rows: OnPackageSpecRow[];
  checkedAt: { iso: string; label: string } | null;
  repoSignals: PackageRepoSignals | null;
}

type Translate = (key: string, named?: Record<string, unknown>) => string;

function repoSummary(signals: PackageRepoSignals | null, t: Translate) {
  if (!signals) return { value: '', badge: undefined };
  const values: string[] = [];
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
  const signals = toPackageRepoSignals(detail);
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

  const repository = (detail as PackageDetail & { repository?: PackageRepositoryInput }).repository;
  const osi = detail.openSource;
  if (repository || osi) {
    const summary = repoSummary(signals, t);
    rows.push({
      key: 'openSource',
      label: t('package.spec.repository'),
      value: [osi ? t('package.spec.openSource') : t('package.spec.githubRepository'), osi?.license, summary.value]
        .filter(Boolean)
        .join(' · '),
      href: repository?.url ?? osi?.url,
      stars:
        signals?.stars === undefined
          ? undefined
          : {
              label: `★${formatRepoStars(signals.stars)}`,
              href: repository?.url ?? undefined,
              checked: signals.checkedAt
                ? t('package.repo.checked', { date: formatDate(signals.checkedAt, { locale }) })
                : undefined
            },
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

  const evidence = [
    ...(detail.facts?.requirements ?? []).map(row => row.evidence),
    ...(detail.facts?.prices ?? []).map(row => row.evidence),
    ...(detail.facts?.security?.identities ?? []).map(row => row.evidence)
  ];
  const checked = evidence
    .filter(item => item && ['verified', 'stale'].includes(item.status) && item.checkedAt)
    .map(item => item.checkedAt!)
    .sort((a, b) => Date.parse(a) - Date.parse(b))[0];
  return {
    rows,
    checkedAt: checked ? { iso: checked.slice(0, 10), label: formatDate(checked, { locale }) } : null,
    repoSignals: signals
  };
}
