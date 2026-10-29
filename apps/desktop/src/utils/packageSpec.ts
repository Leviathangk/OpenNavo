import { formatDate, formatVersion, sharedMessages } from '@opennavo/shared';
import type { SiteLocale } from '@opennavo/shared';
import {
  architectures,
  displayUrl,
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
  detail: PackageDetail | null | undefined,
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
  const value = detail ?? local;
  if (!value) return { rows, checkedAt: null, repoSignals: signals };
  const link = detail?.link ?? local?.link;
  if (value.kind === 'link') {
    const version = formatVersion(detail?.facts?.overview.version || value.version);
    if (version && version.toLowerCase() !== 'latest') {
      const latest = detail?.latestRelease;
      const released = latest && formatVersion(latest.version) === version ? latest.publishedAt : null;
      rows.push({
        key: 'version',
        label: t('package.stats.latest'),
        value: [version, released ? formatDate(released, { locale }) : ''].filter(Boolean).join(' · ')
      });
    }
    const host = (url: string | null | undefined) => {
      try {
        const parsed = new URL(url ?? '');
        return parsed.protocol === 'https:' ? parsed.hostname.replace(/^www\./, '') : undefined;
      } catch {
        return undefined;
      }
    };
    const source =
      link && (host(link.downloadUrl) ?? (link.github ? `github.com/${link.github.repo}` : host(link.homepage)));
    if (source) rows.push({ key: 'source', label: t('package.spec.downloadSource'), value: source });
  }
  const artifacts = detail?.facts?.overview.artifacts ?? detail?.artifacts;
  if (value.isFont) {
    const files = [
      ...new Set((artifacts?.entries ?? []).filter(entry => entry.type === 'font').flatMap(entry => entry.sources))
    ];
    const formats = [
      ...new Set(files.flatMap(file => /\.([a-z0-9]{2,5})$/i.exec(file)?.[1]?.toUpperCase() ?? []))
    ].sort();
    if (files.length)
      rows.push({
        key: 'files',
        label: t('package.spec.fontFiles'),
        value: [String(files.length), formats.join(', ')].filter(Boolean).join(' · ')
      });
    if (value.kind === 'cask')
      rows.push({ key: 'fontLocation', label: t('package.details.location'), value: '~/Library/Fonts', mono: true });
  }
  const price =
    link?.pricing && link.pricing !== 'unknown'
      ? t(`package.link.pricing.${link.pricing}`)
      : detail?.price
        ? t(`package.link.pricing.${detail.price.type}`)
        : '';
  if (price) rows.push({ key: 'price', label: t('package.spec.price'), value: price });

  const system = detail?.dependsOn?.macos ?? detail?.minMacos;
  if (system) rows.push({ key: 'system', label: t('package.spec.system'), value: system });
  const chips = detail
    ? architectures(detail.supports)
        .map(arch => t(`package.arch.${arch}`))
        .join(' · ')
    : '';
  if (!value.isFont && detail)
    rows.push({
      key: 'chip',
      label: t('package.spec.chip'),
      value:
        detail.supports?.status !== 'known'
          ? sharedMessages[locale].cask.unknown
          : chips || sharedMessages[locale].cask.noMac
    });
  if (value.kind === 'cask')
    rows.push({
      key: 'auto',
      label: t('package.spec.autoUpdates'),
      value: t(value.autoUpdates ? 'package.spec.yes' : 'package.spec.no')
    });

  const repository = (detail as (PackageDetail & { repository?: PackageRepositoryInput }) | undefined | null)
    ?.repository;
  const osi = detail?.openSource;
  const github = value.kind === 'link' && link?.github;
  if (repository || osi || github) {
    const summary = repoSummary(signals, t);
    rows.push({
      key: 'openSource',
      label: t('package.spec.repository'),
      value: [osi ? t('package.spec.openSource') : t('package.spec.githubRepository'), osi?.license, summary.value]
        .filter(Boolean)
        .join(' · '),
      href: repository?.url ?? osi?.url ?? (github ? `https://github.com/${github.repo}` : undefined),
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
  const identity = detail?.facts?.security?.identities.find(
    item => item.evidence.status === 'verified' && item.evidence.checkedAt && (item.developer || item.teamId)
  );
  const developer = identity?.developer || detail?.developer;
  if (developer) rows.push({ key: 'developer', label: t('package.spec.developer'), value: developer });
  const teamId = identity?.teamId ?? link?.teamId;
  if (teamId) rows.push({ key: 'teamId', label: t('package.link.teamId'), value: teamId, mono: true });
  if (detail?.license) rows.push({ key: 'license', label: t('package.info.license'), value: detail.license });
  const homepage = value.kind === 'link' ? (link?.homepage ?? value.homepage) : value.homepage;
  if (homepage)
    rows.push({
      key: 'homepage',
      label: t('package.spec.homepage'),
      value: displayUrl(homepage),
      href: homepage
    });
  const bundleIds = detail?.bundleIds ?? local?.bundleIds ?? [];
  if (value.kind === 'link' && bundleIds.length)
    rows.push({ key: 'bundle', label: 'Bundle ID', value: bundleIds.join(' '), mono: true });
  const names = value.names?.filter(name => name !== (detail?.displayName ?? local?.name)) ?? [];
  if (names.length) rows.push({ key: 'names', label: t('package.details.names'), value: names.join(', ') });
  if (value.kind !== 'link') {
    const binaries = detail?.artifacts?.binaries ?? local?.binaries ?? [];
    if (binaries.length)
      rows.push({ key: 'binaries', label: t('package.info.binaries'), value: binaries.join(' '), mono: true });
    if (detail?.artifacts?.uninstallNotes?.length)
      rows.push({
        key: 'uninstall',
        label: t('package.info.uninstall'),
        value: detail.artifacts.uninstallNotes.join(' · ')
      });
    if (detail?.supports?.requiresRosetta)
      rows.push({ key: 'rosetta', label: t('package.arch.arm64'), value: sharedMessages[locale].cask.rosetta });
    if (detail?.platforms?.some(platform => platform.version !== detail.version))
      rows.push({
        key: 'platformVersions',
        label: sharedMessages[locale].cask.version,
        value: sharedMessages[locale].cask.legacy
      });
  }
  rows.push({ key: 'token', label: t('package.spec.token'), value: value.token, mono: true });
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
    ...(detail?.facts?.requirements ?? []).map(row => row.evidence),
    ...(detail?.facts?.prices ?? []).map(row => row.evidence),
    ...(detail?.facts?.security?.identities ?? []).map(row => row.evidence)
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
