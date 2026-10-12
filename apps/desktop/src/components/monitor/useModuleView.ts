import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import type { FanState, MonitorSample, MonitorTrendPoint, SensorReading } from '@/ipc/bindings';
import { useCleanupStore } from '@/stores/cleanup';
import { useMonitorStore } from '@/stores/monitor';
import type { DisplayModule } from '@/stores/monitor';
import { CHART, NONE, average, useMonitorFormat, useMonitorNames } from './meta';

export type ModuleVariant = 'card' | 'popover' | 'detail';
/** Detail charts cover the last minute (live history) or the last hour (5-second trend, M3). */
export type MonitorRange = 'minute' | 'hour';

/** One module's numbers for a card, the menu bar popover or the detail sheet (monitor proposal §12). */
export function useModuleView(
  module: () => DisplayModule,
  variant: () => ModuleVariant,
  range: () => MonitorRange = () => 'minute'
) {
  const { t } = useI18n();
  const store = useMonitorStore();
  const cleanup = useCleanupStore();
  const f = useMonitorFormat();
  const names = useMonitorNames();

  const s = computed(() => store.sample);
  const sys = computed(() => store.system);
  const detail = computed(() => variant() === 'detail');
  const card = computed(() => variant() === 'card');
  /** Popovers show the last 30 seconds; cards and details the whole minute, or the hour when a detail asks for it. */
  const span = (values: (number | null)[]) => (variant() === 'popover' ? values.slice(-30) : values);
  const hour = computed(() => detail.value && range() === 'hour');
  const chart = (
    recent: (sample: MonitorSample) => number | null | undefined,
    long: (point: MonitorTrendPoint) => number | null
  ) => (hour.value ? store.trendSeries(long) : span(store.series(recent)));
  const chartHeight = computed(() => (detail.value ? 90 : variant() === 'popover' ? 34 : 40));
  const ratio = (part: number, whole: number) => (whole > 0 ? part / whole : null);

  const uptime = computed(() => (sys.value ? f.span(Math.max(0, (Date.now() - sys.value.bootedAt) / 1000)) : ''));
  const primaryVolume = computed(() => {
    const volumes = s.value?.disk.volumes ?? [];
    return volumes.find(volume => volume.mountPoint === '/') ?? volumes.find(volume => volume.internal) ?? volumes[0];
  });
  const sensors = computed(() =>
    [...(s.value?.sensors ?? [])].sort((a, b) => (b.celsius ?? -Infinity) - (a.celsius ?? -Infinity))
  );
  const hottest = (readings: readonly SensorReading[] | null) =>
    readings?.reduce<number | null>(
      (top, reading) => (reading.celsius == null ? top : Math.max(top ?? -Infinity, reading.celsius)),
      null
    ) ?? null;
  const fans = computed(() => store.fans ?? []);
  const fanName = (fan: FanState) => names.fan(fan, fans.value.length);
  const maxRpm = computed(() => Math.max(1, ...fans.value.map(fan => fan.maxRpm)));
  /** The detail's speed ranges: fans of one Mac usually share one, then it is listed once rather than per fan. */
  const fanRanges = computed(() => {
    if (!detail.value || !fans.value.length) return [];
    const first = fans.value[0]!;
    const shared = fans.value.every(fan => fan.minRpm === first.minRpm && fan.maxRpm === first.maxRpm);
    return (shared ? [first] : fans.value).map(fan => ({
      key: shared ? 'range' : `range-${fan.id}`,
      label: shared ? t('monitor.fans.rangeLabel') : fanName(fan),
      value: t('monitor.fans.range', { min: f.rpm(fan.minRpm), max: f.rpm(fan.maxRpm) })
    }));
  });
  const cores = computed(() => {
    const performance = sys.value?.performanceCores ?? 0;
    return (s.value?.cpu.cores ?? []).map((value, index) => ({
      value: value ?? 0,
      efficiency: performance > 0 && index >= performance
    }));
  });

  const batteryStatus = computed(() => {
    const battery = s.value?.battery;
    if (!battery) return '';
    const time = battery.minutesRemaining != null ? f.span(battery.minutesRemaining * 60) : null;
    if (battery.charging) return time ? t('monitor.battery.chargingTime', { time }) : t('monitor.battery.charging');
    if (battery.onAc) return t('monitor.battery.onAc');
    return time ? t('monitor.battery.onBatteryTime', { time }) : t('monitor.battery.onBattery');
  });

  interface Line {
    key: string;
    label: string;
    value: string;
  }
  interface View {
    value: string;
    sub: string;
    warn?: boolean;
    charts: {
      key: string;
      label?: string;
      values: (number | null)[];
      color: string;
      max?: number;
      height?: number;
    }[];
    rows: Line[];
  }

  const view = computed<View | null>(() => {
    const sample = s.value;
    if (!sample) return null;
    const lines = (entries: [string, string, string][]) =>
      entries.map(([key, label, value]) => ({ key, label, value }));
    switch (module()) {
      case 'cpu': {
        const load = sample.cpu.load.map(value => f.decimal(value)).join(' · ');
        return {
          value: f.percent(sample.cpu.usage),
          sub: t('monitor.cpu.split', {
            user: f.percent(sample.cpu.user),
            system: f.percent(sample.cpu.system)
          }),
          charts: [
            {
              key: 'usage',
              values: chart(
                entry => entry.cpu.usage,
                point => point.cpu
              ),
              color: CHART.cpu,
              max: 1
            }
          ],
          rows: lines([
            ['load', detail.value ? t('monitor.cpu.loadLong') : t('monitor.cpu.load'), load],
            ...(detail.value && sys.value?.performanceCores != null
              ? ([
                  [
                    'cores',
                    t('monitor.cpu.coresLabel'),
                    t('monitor.cpu.cores', {
                      p: sys.value.performanceCores,
                      e: sys.value.efficiencyCores ?? 0
                    })
                  ]
                ] as [string, string, string][])
              : []),
            ...(detail.value && sample.cpu.powerWatts != null
              ? ([['power', t('monitor.cpu.power'), f.watts(sample.cpu.powerWatts)]] as [string, string, string][])
              : []),
            ['uptime', t('monitor.cpu.uptime'), uptime.value]
          ])
        };
      }
      case 'gpu':
        return {
          value: f.percent(sample.gpu?.usage),
          sub: sys.value?.gpuCores
            ? t('monitor.gpu.cores', { chip: sys.value.chip, count: sys.value.gpuCores })
            : (sys.value?.chip ?? ''),
          charts: [
            {
              key: 'usage',
              values: chart(
                entry => entry.gpu?.usage,
                point => point.gpu
              ),
              color: CHART.gpu,
              max: 1
            }
          ],
          rows: detail.value
            ? lines([
                ['model', t('monitor.gpu.model'), sys.value?.chip ?? ''],
                ...(sample.gpu?.powerWatts != null
                  ? ([['power', t('monitor.gpu.power'), f.watts(sample.gpu.powerWatts)]] as [string, string, string][])
                  : [])
              ])
            : []
        };
      case 'memory': {
        const memory = sample.memory;
        return {
          value: f.percent(ratio(memory.usedBytes, memory.totalBytes)),
          sub: t('monitor.memory.summary', {
            used: f.bytes(memory.usedBytes),
            total: f.bytes(memory.totalBytes),
            pressure: t(`monitor.memory.pressure.${memory.pressure}`)
          }),
          warn: memory.pressure !== 'normal',
          charts: card.value
            ? []
            : [
                {
                  key: 'used',
                  values: chart(
                    entry => ratio(entry.memory.usedBytes, entry.memory.totalBytes),
                    point => point.memory
                  ),
                  color: CHART.memory,
                  max: 1
                }
              ],
          rows: lines([
            ['app', t('monitor.memory.app'), f.bytes(memory.appBytes)],
            ['wired', t('monitor.memory.wired'), f.bytes(memory.wiredBytes)],
            ...(card.value
              ? []
              : ([['compressed', t('monitor.memory.compressed'), f.bytes(memory.compressedBytes)]] as [
                  string,
                  string,
                  string
                ][])),
            ...(detail.value
              ? ([['cached', t('monitor.memory.cached'), f.bytes(memory.cachedBytes)]] as [string, string, string][])
              : []),
            ['swap', t('monitor.memory.swap'), f.bytes(memory.swapUsedBytes)]
          ])
        };
      }
      case 'disk': {
        const volume = primaryVolume.value;
        return {
          value: f.bytes(volume?.freeBytes),
          sub: volume ? t('monitor.disk.volume', { name: volume.name, total: f.bytes(volume.totalBytes) }) : '',
          warn: sample.health.issues.some(issue => issue.key === 'disk_low'),
          charts: detail.value
            ? [
                {
                  key: 'read',
                  label: t('monitor.disk.readAt', { value: f.speed(sample.disk.readBytesPerSec) }),
                  values: chart(
                    entry => entry.disk.readBytesPerSec,
                    point => point.diskRead
                  ),
                  color: CHART.read,
                  height: 44
                },
                {
                  key: 'write',
                  label: t('monitor.disk.writeAt', {
                    value: f.speed(sample.disk.writeBytesPerSec)
                  }),
                  values: chart(
                    entry => entry.disk.writeBytesPerSec,
                    point => point.diskWrite
                  ),
                  color: CHART.write,
                  height: 30
                }
              ]
            : [],
          rows: lines([
            ['read', t('monitor.disk.read'), f.speed(sample.disk.readBytesPerSec)],
            ['write', t('monitor.disk.write'), f.speed(sample.disk.writeBytesPerSec)]
          ])
        };
      }
      case 'network': {
        const network = sample.network;
        const link = network.kind
          ? network.interface
            ? t('monitor.network.link', {
                kind: t(`monitor.network.kinds.${network.kind}`),
                name: network.interface
              })
            : t(`monitor.network.kinds.${network.kind}`)
          : network.interface;
        const where = [link, network.localIp].filter((part): part is string => Boolean(part));
        return {
          value: `↓ ${f.speed(network.downBytesPerSec)}`,
          sub: [`↑ ${f.speed(network.upBytesPerSec)}`, ...where].join(' · '),
          charts: [
            {
              key: 'down',
              label: detail.value ? t('monitor.network.down') : undefined,
              values: chart(
                entry => entry.network.downBytesPerSec,
                point => point.netDown
              ),
              color: CHART.down,
              height: detail.value ? 54 : card.value ? 26 : 34
            },
            ...(variant() === 'popover'
              ? []
              : [
                  {
                    key: 'up',
                    label: detail.value ? t('monitor.network.up') : undefined,
                    values: chart(
                      entry => entry.network.upBytesPerSec,
                      point => point.netUp
                    ),
                    color: CHART.up,
                    height: detail.value ? 30 : 16
                  }
                ])
          ],
          rows: card.value
            ? []
            : lines([
                ['interface', t('monitor.network.interface'), link ?? NONE],
                ['ip', t('monitor.network.localIp'), network.localIp ?? NONE],
                ...(detail.value
                  ? ([
                      ['down', t('monitor.network.totalDown'), f.bytes(network.totalDownBytes)],
                      ['up', t('monitor.network.totalUp'), f.bytes(network.totalUpBytes)]
                    ] as [string, string, string][])
                  : [])
              ])
        };
      }
      case 'battery': {
        const battery = sample.battery;
        if (!battery) return null;
        const all: [string, string, string][] = [
          ['health', t('monitor.battery.health'), f.percent(battery.healthPercent)],
          ['cycles', t('monitor.battery.cycles'), f.count(battery.cycleCount)],
          [
            'design',
            t('monitor.battery.designCapacity'),
            battery.designCapacityMah == null ? NONE : `${f.count(battery.designCapacityMah)} mAh`
          ],
          [
            'max',
            t('monitor.battery.maxCapacity'),
            battery.maxCapacityMah == null ? NONE : `${f.count(battery.maxCapacityMah)} mAh`
          ],
          ['temperature', t('monitor.battery.temperature'), f.celsius(battery.temperatureC)],
          [
            'voltage',
            t('monitor.battery.voltage'),
            battery.voltageV == null ? NONE : `${f.decimal(battery.voltageV, 1)} V`
          ],
          ['power', t('monitor.battery.power'), f.watts(battery.powerWatts)],
          [
            'adapter',
            t('monitor.battery.adapter'),
            battery.adapterWatts == null ? NONE : `${f.count(battery.adapterWatts)} W`
          ]
        ];
        return {
          value: f.percent(battery.percent),
          sub: batteryStatus.value,
          warn: sample.health.issues.some(issue => issue.key === 'battery_health' || issue.key === 'battery_hot'),
          charts: detail.value
            ? [
                {
                  key: 'level',
                  values: chart(
                    entry => entry.battery?.percent,
                    point => point.battery
                  ),
                  color: 'var(--on-status-success)',
                  max: 1
                }
              ]
            : [],
          rows: lines(detail.value ? all : card.value ? all.slice(0, 2) : [all[0]!, all[1]!, all[7]!])
        };
      }
      case 'sensors': {
        const top = sensors.value[0];
        const temperature = {
          key: 'hottest',
          values: chart(
            entry => hottest(entry.sensors),
            point => point.temperature
          ),
          color: CHART.temperature,
          max: 100
        };
        return {
          value: f.celsius(top?.celsius),
          sub: top ? t('monitor.sensors.hottest', { name: names.sensor(top) }) : '',
          warn: sample.health.issues.some(issue => issue.key === 'temperature'),
          // The detail also charts the fans' mean speed, each chart under its own label.
          charts:
            detail.value && fans.value.length
              ? [
                  { ...temperature, label: t('monitor.sensors.temperatures') },
                  {
                    key: 'rpm',
                    label: t('monitor.modules.fans'),
                    values: chart(
                      entry => average((entry.fans ?? []).map(fan => fan.rpm)),
                      point => point.fanRpm
                    ),
                    color: CHART.fan,
                    max: maxRpm.value
                  }
                ]
              : [temperature],
          rows: lines(
            (variant() === 'popover' ? sensors.value.slice(0, 4) : card.value ? [] : sensors.value).map(reading => [
              reading.key,
              names.sensor(reading),
              f.celsius(reading.celsius)
            ])
          )
        };
      }
    }
    return null;
  });

  const cleanupHint = computed(() =>
    cleanup.summary?.scannedAt != null && cleanup.summary.reclaimableBytes > 0
      ? t('monitor.disk.cleanup', { size: f.bytes(cleanup.summary.reclaimableBytes) })
      : t('monitor.disk.cleanupNever')
  );

  return { f, s, sys, detail, hour, chartHeight, view, cores, fans, fanName, fanRanges, primaryVolume, cleanupHint };
}
