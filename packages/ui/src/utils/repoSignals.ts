/**
 * Optional repository metadata used by the package detail spec card.
 *
 * The public API will add these fields independently of the UI rollout, so the
 * adapter deliberately accepts unknown input and supports both snake_case and
 * camelCase spellings. Keeping this view model in the UI package means web and
 * desktop can render the same data without coupling to an API response shape.
 */
export interface PackageRepoSignals {
  stars?: number;
  pushedAt?: string;
  checkedAt?: string;
  archived?: boolean;
  repoUrl?: string;
}

type UnknownRecord = Record<string, unknown>;

function record(value: unknown): UnknownRecord | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as UnknownRecord) : null;
}

function firstRecord(value: UnknownRecord): UnknownRecord {
  return (
    record(value.repoSignals) ??
    record(value.repositorySignals) ??
    record(value.githubSignals) ??
    record(value.github) ??
    value
  );
}

function numberValue(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value) && value >= 0) return value;
  if (typeof value === 'string' && value.trim() && Number.isFinite(Number(value)) && Number(value) >= 0)
    return Number(value);
  return undefined;
}

function stringValue(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value : undefined;
}

/**
 * Adapt a package/API payload to the independent display view model.
 * Missing or malformed optional fields are ignored; a null result means the
 * page has no repository signal to show yet.
 */
export function toPackageRepoSignals(input: unknown, fallbackRepoUrl?: string | null): PackageRepoSignals | null {
  const source = record(input);
  if (!source) return null;
  const value = firstRecord(source);
  const signals: PackageRepoSignals = {
    stars: numberValue(value.stargazers_count ?? value.stargazersCount ?? value.stars),
    pushedAt: stringValue(value.pushed_at ?? value.pushedAt),
    checkedAt: stringValue(value.signals_checked_at ?? value.signalsCheckedAt ?? value.checked_at ?? value.checkedAt),
    archived: typeof value.archived === 'boolean' ? value.archived : undefined,
    repoUrl: stringValue(value.repo_url ?? value.repoUrl) ?? stringValue(fallbackRepoUrl)
  };
  const hasSignal =
    signals.stars !== undefined ||
    signals.pushedAt !== undefined ||
    signals.checkedAt !== undefined ||
    signals.archived !== undefined;
  return hasSignal || signals.repoUrl ? signals : null;
}

/** Format stars using a single decimal below the next unit, avoiding 1050.0K. */
export function formatRepoStars(stars: number): string {
  const value = Math.max(0, Math.round(stars));
  if (value < 1000) return String(value);
  if (value < 1_000_000) return `${(Math.floor(value / 100) / 10).toFixed(1)}K`;
  return `${(Math.floor(value / 100_000) / 10).toFixed(1)}M`;
}

/** Whole elapsed days for a pushed-at timestamp, or null for an invalid/future date. */
export function repoAgeDays(pushedAt: string, now = Date.now()): number | null {
  const timestamp = Date.parse(pushedAt);
  if (!Number.isFinite(timestamp) || timestamp > now) return null;
  return Math.floor((now - timestamp) / 86_400_000);
}

/** Archived takes precedence over the unmaintained warning. */
export function repoMaintenanceStatus(
  signals: Pick<PackageRepoSignals, 'archived' | 'pushedAt'>,
  now = Date.now()
): 'archived' | 'unmaintained' | 'current' | null {
  if (signals.archived === true) return 'archived';
  if (!signals.pushedAt) return null;
  const days = repoAgeDays(signals.pushedAt, now);
  return days !== null && days > 365 ? 'unmaintained' : 'current';
}

export function repoSignalDays(signals: Pick<PackageRepoSignals, 'pushedAt'>, now = Date.now()): number | null {
  return signals.pushedAt ? repoAgeDays(signals.pushedAt, now) : null;
}
