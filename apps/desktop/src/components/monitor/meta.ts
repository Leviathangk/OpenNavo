import { useI18n } from 'vue-i18n';
import { formatBytes, formatCount, formatDecimal, formatPercent, formatSpeed } from '@opennavo/shared';
import type { FanState, MenuBarStyle, MonitorModule, SensorReading } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import type { DisplayModule } from '@/stores/monitor';

/** Icon per module, shared by cards, details, menu bar previews and settings rows. */
export const MODULE_ICONS: Record<MonitorModule, string> = {
  cpu: 'cpu',
  gpu: 'gpu',
  memory: 'memory-stick',
  disk: 'hard-drive',
  network: 'wifi',
  battery: 'battery',
  sensors: 'thermometer',
  fans: 'fan'
};

/** Menu bar chart styles exist for modules whose value moves; disk space and battery change too slowly (M3). */
export const CHART_MODULES: readonly DisplayModule[] = ['cpu', 'gpu', 'memory', 'network', 'sensors'];
/** A temperature reads on its own, as in Stats: the sensors item has no two-line style. */
const UNLABELLED: readonly DisplayModule[] = ['sensors'];
/** The style Rust actually draws: a chart style saved for disk or battery falls back to two lines. */
export const shownStyle = (module: DisplayModule, style: MenuBarStyle): MenuBarStyle => {
  if ((style === 'line' || style === 'bars') && !CHART_MODULES.includes(module)) return 'text';
  return style === 'text' && UNLABELLED.includes(module) ? 'value' : style;
};

/** Chart colors from the design tokens. */
export const CHART = {
  cpu: 'var(--on-brand-coral)',
  efficiency: 'var(--on-chart-series1)',
  gpu: 'var(--on-chart-series1)',
  memory: 'var(--on-chart-series1)',
  read: 'var(--on-status-info)',
  write: 'var(--on-brand-salmon)',
  down: 'var(--on-status-info)',
  up: 'var(--on-brand-salmon)',
  temperature: 'var(--on-status-warning)',
  fan: 'var(--on-status-info)'
} as const;

/** How a process list is ordered: CPU and memory always; disk and network rates from M3. */
export type ProcessSort = 'cpu' | 'memory' | 'disk' | 'network';

/** Shown where Rust has no reading yet (first rate sample, sleep) or the hardware lacks one. */
export const NONE = '—';

/** Locale-bound formatters that accept missing readings. Units (rpm, °C) are technical and stay untranslated. */
export function useMonitorFormat() {
  const { appLocale } = useAppLocale();
  const opts = () => ({ locale: appLocale.value });
  return {
    percent: (value: number | null | undefined) => (value == null ? NONE : formatPercent(value, opts())),
    bytes: (value: number | null | undefined) => (value == null ? NONE : formatBytes(value, opts())),
    speed: (value: number | null | undefined) => (value == null ? NONE : formatSpeed(value, opts())),
    rpm: (value: number | null | undefined) =>
      value == null ? NONE : `${formatCount(Math.round(value), opts())}\u00a0rpm`,
    celsius: (value: number | null | undefined) => (value == null ? NONE : `${Math.round(value)}\u00a0°C`),
    decimal: (value: number | null | undefined, digits = 2) =>
      value == null ? NONE : formatDecimal(value, { ...opts(), digits }),
    count: (value: number | null | undefined) => (value == null ? NONE : formatCount(value, opts())),
    watts: (value: number | null | undefined) =>
      value == null ? NONE : `${formatDecimal(value, { ...opts(), digits: 1 })}\u00a0W`,
    /** Uptime and battery time: the two largest of days, hours and minutes in the locale's words ("6 days, 4 hours"). */
    span: (seconds: number) => {
      const minutes = Math.max(1, Math.round(seconds / 60));
      const parts = [
        [Math.floor(minutes / 1440), 'day'],
        [Math.floor((minutes % 1440) / 60), 'hour'],
        [minutes % 60, 'minute']
      ] as const;
      const first = parts.findIndex(([value]) => value > 0);
      const shown = parts.slice(first, first + 2).filter(([value]) => value > 0);
      return new Intl.ListFormat(appLocale.value, { type: 'unit', style: 'short' }).format(
        shown.map(([value, unit]) =>
          new Intl.NumberFormat(appLocale.value, { style: 'unit', unit, unitDisplay: 'long' }).format(value)
        )
      );
    }
  };
}

/** Rust names sensor zones in English from a fixed vocabulary; known names are localized, anything else passes through. */
const SENSOR_NAMES: Record<string, string> = {
  'cpu performance cores': 'cpuPerformance',
  'cpu efficiency cores': 'cpuEfficiency',
  cpu: 'cpu',
  gpu: 'gpu',
  soc: 'soc',
  ssd: 'ssd',
  battery: 'battery'
};

export function useMonitorNames() {
  const { t } = useI18n();
  return {
    sensor: (reading: SensorReading) => {
      const key = SENSOR_NAMES[reading.name.trim().toLowerCase()];
      return key ? t(`monitor.sensors.names.${key}`) : reading.name;
    },
    /** One fan is just "Fan"; otherwise left/right when the SMC says so, else numbered by SMC index. */
    fan: (fan: FanState, count: number) => {
      if (count <= 1) return t('monitor.fans.single');
      if (/\bleft\b/i.test(fan.name)) return t('monitor.fans.left');
      if (/\bright\b/i.test(fan.name)) return t('monitor.fans.right');
      return t('monitor.fans.numbered', { n: fan.id + 1 });
    }
  };
}

/** Style choices for one module's menu bar item, as menu items. */
export function useMenuBarStyles() {
  const { t } = useI18n();
  return (module: DisplayModule) => [
    ...(UNLABELLED.includes(module) ? [] : [{ key: 'text' as const, label: t('monitor.menuBar.styles.text') }]),
    { key: 'value' as const, label: t('monitor.menuBar.styles.value') },
    ...(CHART_MODULES.includes(module)
      ? [
          { key: 'line' as const, label: t('monitor.menuBar.styles.line') },
          { key: 'bars' as const, label: t('monitor.menuBar.styles.bars') }
        ]
      : [])
  ];
}

export const average = (values: readonly (number | null)[]) => {
  const known = values.filter((value): value is number => value != null);
  return known.length ? known.reduce((sum, value) => sum + value, 0) / known.length : null;
};
