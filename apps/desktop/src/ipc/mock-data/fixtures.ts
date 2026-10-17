// Local-state fixtures for browser mode (pnpm dev:web) and component tests: catalog from backend sync fixtures (catalog.json, 60 seed-e2e packages).
// Installed/history scenarios follow mockups 05/06 (updatable, pinned, self-updated, dependency, failed update).
import catalog from './catalog.json';
import { LOCALES } from '@opennavo/shared';
import type { Locale } from '@opennavo/shared';
import type { EnvInfo, InstalledItem, Kind, StorageSummary, Task } from '../bindings';
import type { LocalItem } from '../client';

export interface SnapshotCategory {
  sourceLocale: Locale;
  slug: string;
  parent: string | null;
  icon: string;
  sort: number;
  hiddenByDefault: boolean;
  appliesTo: 'cask' | 'formula' | 'both';
  name: Record<string, string>;
}

// Old fixtures contain only Chinese/English and allow null; IPC v2 emits sparse strings without inventing missing translations.
const sparse = (text: Record<string, string | null>) =>
  Object.fromEntries(LOCALES.flatMap(locale => (text[locale]?.trim() ? [[locale, text[locale]]] : [])));
export const catalogItems: LocalItem[] = (catalog.items as unknown as LocalItem[]).map(item => ({
  ...item,
  sourceLocale: 'en-US',
  displayName: sparse(item.displayName),
  summary: sparse(item.summary)
}));
export const catalogCategories: SnapshotCategory[] = catalog.categories.map(category => ({
  ...category,
  appliesTo: category.appliesTo as SnapshotCategory['appliesTo'],
  sourceLocale: 'zh-CN',
  name: sparse(category.name)
}));
export const catalogCursor = catalog.cursor;

const DAY = 24 * 60 * 60 * 1000;
// Fixture times are relative to now, keeping N-days-ago labels stable.
export const fixtureNow = () => Date.now();

export function findItem(kind: Kind, token: string): LocalItem | undefined {
  return catalogItems.find(item => item.kind === kind && item.token === token);
}

/** Previous version: decrement the last numeric part; approximations such as 1.140.0 → 1.139.9 suffice for update displays. */
export function previousVersion(version: string): string {
  const parts = version.split('.');
  for (let index = parts.length - 1; index >= 0; index -= 1) {
    const value = Number.parseInt(parts[index] ?? '', 10);
    if (Number.isNaN(value)) continue;
    if (value > 0) {
      parts[index] = String(value - 1);
      return parts.join('.');
    }
    parts[index] = '9';
  }
  return `${version}-1`;
}

// Derive stable pseudorandom sizes from tokens so displayed numbers do not change on refresh.
function stableBytes(token: string, min: number, max: number): number {
  let hash = 2166136261;
  for (const char of token) hash = Math.imul(hash ^ char.charCodeAt(0), 16777619) >>> 0;
  return min + (hash % (max - min));
}

/** Download size: E2E fixtures use tiny placeholders; supply realistic stable app/command-line tool sizes for display. */
export function downloadSizeOf(item: LocalItem): number {
  if (item.downloadSize && item.downloadSize > 1_000_000) return item.downloadSize;
  return item.kind === 'cask'
    ? stableBytes(item.token, 40_000_000, 400_000_000)
    : stableBytes(item.token, 1_000_000, 30_000_000);
}

type InstalledSpec = {
  token: string;
  kind: Kind;
  state: 'latest' | 'outdated' | 'pinned' | 'self_updated';
  daysAgo: number;
  onRequest?: boolean;
  requiredBy?: string[];
};

const INSTALLED: InstalledSpec[] = [
  { kind: 'cask', token: 'docker-desktop', state: 'outdated', daysAgo: 306 },
  { kind: 'cask', token: 'google-chrome', state: 'outdated', daysAgo: 321 },
  { kind: 'cask', token: 'visual-studio-code', state: 'outdated', daysAgo: 217 },
  { kind: 'cask', token: 'orbstack', state: 'latest', daysAgo: 270 },
  { kind: 'cask', token: 'raycast', state: 'pinned', daysAgo: 410 },
  { kind: 'cask', token: 'ghostty', state: 'outdated', daysAgo: 233 },
  { kind: 'cask', token: 'wechat', state: 'self_updated', daysAgo: 120 },
  { kind: 'cask', token: 'obsidian', state: 'latest', daysAgo: 64 },
  { kind: 'formula', token: 'node', state: 'outdated', daysAgo: 23 },
  { kind: 'formula', token: 'gh', state: 'latest', daysAgo: 1 },
  { kind: 'formula', token: 'ripgrep', state: 'latest', daysAgo: 67 },
  { kind: 'formula', token: 'git', state: 'latest', daysAgo: 40 },
  { kind: 'formula', token: 'wget', state: 'outdated', daysAgo: 90 },
  {
    kind: 'formula',
    token: 'openssl@3',
    state: 'latest',
    daysAgo: 90,
    onRequest: false,
    requiredBy: ['wget', 'node']
  },
  {
    kind: 'formula',
    token: 'zlib',
    state: 'latest',
    daysAgo: 90,
    onRequest: false,
    requiredBy: ['git']
  }
];

// The Cask-only catalog (ADR-018) has no command-line tools, but local Homebrew may still have them installed.
// Simulate brew info --installed; the UI store filters these tools. Use fixed versions independent of catalog data.
const LOCAL_FORMULA_VERSIONS: Record<string, string> = {
  node: '24.9.0',
  gh: '2.102.0',
  ripgrep: '15.1.0',
  git: '2.51.0',
  wget: '1.25.0',
  'openssl@3': '3.5.4',
  zlib: '1.3.1'
};

function installedSource(spec: InstalledSpec) {
  const item = findItem(spec.kind, spec.token);
  if (item)
    return {
      kind: item.kind,
      token: item.token,
      name: item.displayName['en-US'] ?? item.name,
      version: item.version,
      apps: item.apps,
      autoUpdates: item.autoUpdates
    };
  const version = spec.kind === 'formula' ? LOCAL_FORMULA_VERSIONS[spec.token] : undefined;
  if (!version) return null;
  return {
    kind: spec.kind,
    token: spec.token,
    name: spec.token,
    version,
    apps: [] as string[],
    autoUpdates: false
  };
}

export function installedFixture(now = fixtureNow()): InstalledItem[] {
  return INSTALLED.flatMap(spec => {
    const item = installedSource(spec);
    if (!item) return [];
    const latest = item.version;
    const behind = spec.state === 'outdated' || spec.state === 'pinned' || spec.state === 'self_updated';
    const installedVersion = behind ? previousVersion(latest) : latest;
    const app = item.apps[0];
    const status =
      spec.state === 'latest'
        ? 'up_to_date'
        : spec.state === 'self_updated'
          ? 'self_updated'
          : spec.state === 'pinned'
            ? 'pinned'
            : 'outdated';
    return [
      {
        kind: item.kind,
        token: item.token,
        name: item.name,
        installedVersion,
        actualVersion: spec.state === 'self_updated' ? latest : item.kind === 'cask' ? installedVersion : null,
        latestVersion: latest,
        status,
        onRequest: spec.onRequest ?? true,
        requiredBy: spec.requiredBy ?? [],
        installedAt: now - spec.daysAgo * DAY,
        sizeBytes:
          item.kind === 'cask'
            ? stableBytes(item.token, 60_000_000, 2_200_000_000)
            : stableBytes(item.token, 2_000_000, 120_000_000),
        appPaths: app ? [`/Applications/${app}`] : [],
        iconPath: null,
        autoUpdates: item.autoUpdates,
        pinned: spec.state === 'pinned'
      } satisfies InstalledItem
    ];
  });
}

let taskSeq = 0;
/** Time-ordered UUID v7-style IDs, only required to be unique and increasing within the simulation. */
export function nextTaskId(now = Date.now()): string {
  taskSeq += 1;
  return `${now.toString(16).padStart(12, '0')}-7000-8000-${String(taskSeq).padStart(12, '0')}`;
}

export function finishedTask(partial: Partial<Task> & Pick<Task, 'op' | 'state' | 'createdAt'>): Task {
  const startedAt = partial.startedAt ?? partial.createdAt;
  return {
    id: nextTaskId(partial.createdAt),
    target: null,
    targetName: null,
    outcome: null,
    options: {},
    trigger: 'manual',
    phase: null,
    percent: partial.state === 'succeeded' ? 100 : null,
    bytesDone: null,
    bytesTotal: null,
    speedBps: null,
    stepIndex: null,
    stepCount: null,
    fromVersion: null,
    toVersion: null,
    error: null,
    exitCode: partial.state === 'succeeded' ? 0 : 1,
    logPath: null,
    startedAt,
    finishedAt: startedAt + 38_000,
    ...partial
  };
}

export function historyFixture(now = fixtureNow()): Task[] {
  // Offset backward from now: today's records must not be in the future; earlier days fall on their intended dates.
  const at = (daysAgo: number, hoursAgo: number, minutesAgo: number) =>
    now - daysAgo * DAY - hoursAgo * 60 * 60 * 1000 - minutesAgo * 60 * 1000;
  return [
    finishedTask({
      op: 'upgrade',
      state: 'succeeded',
      target: { kind: 'cask', token: 'iterm2' },
      fromVersion: '3.5.13',
      toVersion: '3.5.14',
      createdAt: at(0, 0, 36)
    }),
    finishedTask({
      op: 'upgrade',
      state: 'succeeded',
      trigger: 'schedule',
      target: { kind: 'formula', token: 'gh' },
      fromVersion: '2.101.0',
      toVersion: '2.102.0',
      createdAt: at(0, 1, 30)
    }),
    finishedTask({
      op: 'upgrade',
      state: 'succeeded',
      trigger: 'schedule',
      target: { kind: 'formula', token: 'ripgrep' },
      fromVersion: '14.1.0',
      toVersion: '14.1.1',
      createdAt: at(0, 1, 29)
    }),
    finishedTask({
      op: 'upgrade',
      state: 'failed',
      target: { kind: 'cask', token: 'firefox' },
      fromVersion: '143.0',
      toVersion: '143.0.1',
      exitCode: 1,
      error: {
        code: 'E_CHECKSUM',
        message: 'SHA256 mismatch',
        detail: 'Expected: 8f1c… Actual: 0d2a…'
      },
      createdAt: at(1, 2, 0)
    }),
    finishedTask({
      op: 'install',
      state: 'succeeded',
      target: { kind: 'cask', token: 'obsidian' },
      toVersion: '1.9.12',
      createdAt: at(3, 4, 12)
    }),
    finishedTask({
      op: 'uninstall',
      state: 'succeeded',
      target: { kind: 'cask', token: 'zoom' },
      fromVersion: '6.6.0',
      createdAt: at(5, 1, 30)
    })
  ];
}

export function envFixture(): EnvInfo {
  return {
    macosVersion: '27.0.1',
    arch: 'arm64',
    rosetta: false,
    cltInstalled: true,
    brew: { path: '/opt/homebrew/bin/brew', prefix: '/opt/homebrew', version: '7.0.7' },
    appManagement: 'granted'
  };
}

export function storageFixture(installed: readonly InstalledItem[], now = fixtureNow()): StorageSummary {
  const sum = (kind: Kind) =>
    installed.filter(item => item.kind === kind).reduce((total, item) => total + (item.sizeBytes ?? 0), 0);
  return {
    appsBytes: sum('cask'),
    appCount: installed.filter(item => item.kind === 'cask').length,
    formulaeBytes: sum('formula'),
    formulaCount: installed.filter(item => item.kind === 'formula').length,
    cacheBytes: 2_300_000_000,
    complete: true,
    measuredAt: now
  };
}
