// Per-version macOS compatibility (F6), pure for testing. The server states only sourced facts: bounds are
// null and Apple silicon support is unknown when nothing says otherwise, so this never guesses either.
import type { ReleaseEntry } from '@opennavo/api';
import { interpolate } from '@opennavo/shared';

/** Host copy for the compatibility row; without it the row is not shown (desktop keeps its layout). */
export interface OnVersionCompatibilityLabels {
  /** Row label, e.g. "Compatibility". */
  title: string;
  /** Shown when the version has no sourced compatibility facts. */
  unknown: string;
  /** Lower bound, with {version}, e.g. "macOS {version} or later". */
  minimum: string;
  /** Upper bound, with {version}, e.g. "up to macOS {version}". */
  maximum: string;
  /** Apple silicon support a source states. */
  appleSilicon: { yes: string; no: string };
}

type Compatibility = NonNullable<ReleaseEntry['compatibility']>;

function bounds(
  minimum: string | null | undefined,
  maximum: string | null | undefined,
  labels: OnVersionCompatibilityLabels
): string[] {
  return [
    ...(minimum ? [interpolate(labels.minimum, { version: minimum })] : []),
    ...(maximum ? [interpolate(labels.maximum, { version: maximum })] : [])
  ];
}

/**
 * One line for a version: its macOS bounds and Apple silicon support, or the unknown label. When the version
 * has no single range because its architectures differ, each requirement keeps its own bounds rather than a
 * merged range no source states.
 */
export function compatibilityText(
  value: Compatibility | null | undefined,
  labels: OnVersionCompatibilityLabels
): string {
  if (!value) return labels.unknown;
  const parts = bounds(value.minimumMacOS, value.maximumMacOS, labels);
  if (!parts.length) {
    const rows = value.requirements.flatMap(requirement => {
      const range = bounds(requirement.minimumMacOS, requirement.maximumMacOS, labels).join(', ');
      if (!range) return [];
      const arch = requirement.architectures.join(', ');
      return [arch ? `${arch}: ${range}` : range];
    });
    parts.push(...new Set(rows));
  }
  if (value.appleSilicon === 'yes' || value.appleSilicon === 'no') parts.push(labels.appleSilicon[value.appleSilicon]);
  return parts.length ? parts.join(' · ') : labels.unknown;
}
