import type { PackageSummary } from '@opennavo/api';
import { installCommand } from '@opennavo/shared';

/** Landing demo apps (08 §10.14): homepage/ranking APIs, falling back to SAMPLE_APPS. */
export interface LandingApp {
  token: string;
  name: string;
  summary: string | null;
  iconUrl: string | null;
  iconVariants?: PackageSummary['iconVariants'];
  accentColor: string | null;
  version: string | null;
  versionChangedAt: string | null;
}

export function toLandingApp(pkg: PackageSummary): LandingApp {
  return {
    token: pkg.token,
    name: pkg.displayName,
    summary: pkg.summary ?? null,
    iconUrl: pkg.iconUrl ?? null,
    iconVariants: pkg.iconVariants,
    accentColor: pkg.accentColor ?? null,
    version: pkg.version || null,
    versionChangedAt: pkg.versionChangedAt || null
  };
}

const sample = (token: string, name: string): LandingApp => ({
  token,
  name,
  summary: null,
  iconUrl: null,
  accentColor: null,
  version: null,
  versionChangedAt: null
});

/** Fallback demo apps contain only tokens/names; never invent versions/install counts. */
export const SAMPLE_APPS: readonly LandingApp[] = [
  sample('visual-studio-code', 'Visual Studio Code'),
  sample('ghostty', 'Ghostty'),
  sample('google-chrome', 'Google Chrome'),
  sample('docker-desktop', 'Docker Desktop'),
  sample('obsidian', 'Obsidian'),
  sample('raycast', 'Raycast')
];

/**
 * Merge lists, retaining installable Casks (exclude fonts/disabled), deduplicating tokens while preserving API order.
 * Prefer entries with icons to minimize letter tiles in demos.
 */
export function pickShowcaseApps(
  lists: readonly (readonly PackageSummary[] | null | undefined)[],
  limit: number
): PackageSummary[] {
  const seen = new Set<string>();
  const unique: PackageSummary[] = [];
  for (const list of lists)
    for (const pkg of list ?? []) {
      if (pkg.kind !== 'cask' || pkg.disabled || pkg.isFont || seen.has(pkg.token)) continue;
      seen.add(pkg.token);
      unique.push(pkg);
    }
  return [...unique.filter(pkg => pkg.iconUrl), ...unique.filter(pkg => !pkg.iconUrl)].slice(0, Math.max(0, limit));
}

/** Fill to min with sample apps, skipping existing tokens. */
export function withSampleApps(apps: readonly LandingApp[], min: number): LandingApp[] {
  const result = [...apps];
  for (const item of SAMPLE_APPS) {
    if (result.length >= min) break;
    if (!result.some(app => app.token === item.token)) result.push(item);
  }
  return result;
}

/**
 * Marquee: distribute enough entries across rows; otherwise use all entries in each row with offset starts to avoid nearly empty rows.
 * Repeat each row to minPerRow so one copy exceeds viewport width for seamless looping; empty data yields an empty array.
 */
export function marqueeRows<T>(items: readonly T[], rows: number, minPerRow: number): T[][] {
  if (!items.length || rows < 1) return [];
  const count = Math.min(rows, items.length);
  const enough = items.length >= count * Math.ceil(minPerRow / 2);
  const lines: T[][] = [];
  for (let row = 0; row < count; row += 1) {
    const start = Math.floor((row * items.length) / count);
    lines.push(
      enough ? items.filter((item, index) => index % count === row) : [...items.slice(start), ...items.slice(0, start)]
    );
  }
  return lines.map(line => {
    const filled = [...line];
    while (filled.length < minPerRow) filled.push(...line);
    return filled;
  });
}

/** Count/progress easing: fast then slow, clamping t outside 0–1 to endpoints. */
export function easeOutCubic(t: number): number {
  const clamped = Math.min(1, Math.max(0, t));
  return 1 - (1 - clamped) ** 3;
}

/** Install demo terminal output follows real Homebrew Cask command/log format; illustrative only, never executed. */
export function installLog(token: string): string[] {
  return [
    `$ ${installCommand('cask', token)}`,
    `==> Fetching downloads for: ${token}`,
    `==> Installing Cask ${token}`,
    `🍺  ${token} was successfully installed!`
  ];
}

/**
 * First line of a two-line heading that reads as one sentence. Vue drops whitespace-only text between the block
 * spans, so the separating space ends the first line (on screen it vanishes at the line end); Chinese and Japanese
 * lines join without one.
 */
export function headingLead(title: string): string {
  return /[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\u3000-\u303f\uff00-\uffef]$/u.test(title)
    ? title
    : `${title} `;
}
