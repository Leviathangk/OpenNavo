<script setup lang="ts">
import type { PublicComponents } from '@opennavo/api';
import {
  formatBytes,
  formatCount,
  formatCountCompact,
  formatDate,
  formatRelativeTime,
  formatVersion
} from '@opennavo/shared';
import { architectures, OnPackageStats, useUiMessages } from '@opennavo/ui';
import type { OnPackageStatsCell } from '@opennavo/ui';
import { latestVersionDate } from '~/utils/indexing';
import type { PackagePage } from '~/utils/packageFacts';

const { locale: useI18nLocale } = useI18n();
const formattingLocale = computed(() => toAppLocale(useI18nLocale.value));

type ReleaseStats = PublicComponents['schemas']['ReleaseStats'];

// Five stats (08 §10.5): 30-day installs/rank, yearly installs, latest version/date, download size, cadence.
const props = defineProps<{ pkg: PackagePage; releaseStats?: ReleaseStats | null }>();

const { t, locale } = useI18n();
const messages = useUiMessages();
const appLocale = computed(() => toAppLocale(locale.value));

const size = computed(() => {
  if (!props.pkg.downloadSize) return { value: '—', unit: undefined };
  const [value, unit] = formatBytes(props.pkg.downloadSize, { locale: formattingLocale.value }).split(/\s+(?=\p{L})/u);
  return { value: value ?? '—', unit };
});

const fileType = computed(() =>
  /\.([a-z0-9]{2,5})(?:[?#]|$)/i.exec(props.pkg.facts.overview.downloadUrl ?? '')?.[1]?.toLowerCase()
);

const cells = computed<OnPackageStatsCell[]>(() => {
  // WP3: only a reliable release date; versionChangedAt is when OpenNavo noticed the version.
  const latestAt = latestVersionDate(props.pkg);
  const stats = props.releaseStats;
  return [
    {
      key: 'd30',
      label: t('package.stats.installs30d'),
      value: formatCount(props.pkg.installs.d30, { locale: formattingLocale.value }),
      hint: props.pkg.rank30d
        ? t('package.stats.rank', { kind: messages.value.kind[props.pkg.kind], rank: props.pkg.rank30d })
        : undefined
    },
    {
      key: 'd365',
      label: t('package.stats.installs365d'),
      value: formatCountCompact(props.pkg.installs.d365, { locale: appLocale.value }),
      hint: t('package.stats.total365')
    },
    {
      key: 'version',
      label: t('package.stats.latest'),
      value: formatVersion(props.pkg.version),
      hint: latestAt
        ? `${formatRelativeTime(latestAt, { locale: appLocale.value })} · ${formatDate(latestAt, { locale: appLocale.value })}`
        : undefined
    },
    {
      key: 'size',
      label: t('package.stats.size'),
      value: size.value.value,
      unit: size.value.unit,
      hint: [architectures(props.pkg.supports).join(' / '), fileType.value].filter(Boolean).join(' · ') || undefined
    },
    {
      key: 'cadence',
      label: t('package.stats.cadence'),
      value: stats ? t(`package.cadence.${stats.cadence}`) : '—',
      hint: stats ? t('package.stats.releases30d', { count: stats.count30d }, { plural: stats.count30d }) : undefined
    }
  ];
});
</script>

<template>
  <OnPackageStats :cells="cells" :label="t('package.stats.label')" />
</template>
