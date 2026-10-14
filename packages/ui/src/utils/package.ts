// Shared web/desktop detail calculations (08 §10.5, §11.7), pure for testing.
import type { InstallStats } from '@opennavo/api';

export interface MonthlyRow {
  key: 'd30' | 'avg90' | 'avg365';
  value: number;
}

/** Monthly average installs (08 §11.7): 30 days, 90-day monthly average, yearly monthly average. */
export function monthlyInstalls(installs: InstallStats): MonthlyRow[] {
  return [
    { key: 'd30', value: installs.d30 },
    { key: 'avg90', value: Math.round(installs.d90 / 3) },
    { key: 'avg365', value: Math.round(installs.d365 / 12) }
  ];
}

export type InstallTrend = 'rising' | 'slowing' | 'steady';

/** Last 30 days versus yearly monthly average: >10% increase means growth, >10% decrease means slowdown. */
export function installTrend(installs: InstallStats): InstallTrend {
  const average = installs.d365 / 12;
  if (average <= 0) return 'steady';
  const ratio = installs.d30 / average;
  if (ratio > 1.1) return 'rising';
  if (ratio < 0.9) return 'slowing';
  return 'steady';
}

/** Supported architectures: Apple silicon · Intel when both apply. */
export function architectures(supports: { arm64: boolean; x86_64: boolean }): ('arm64' | 'x86_64')[] {
  return [...(supports.arm64 ? (['arm64'] as const) : []), ...(supports.x86_64 ? (['x86_64'] as const) : [])];
}

/** Display URLs as host/path without scheme/trailing slash. */
export function displayUrl(url: string): string {
  return url.replace(/^https?:\/\//, '').replace(/\/$/, '');
}
