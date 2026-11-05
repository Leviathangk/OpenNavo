import type { RequirementFact } from './packageFacts';

export interface RequirementMobileSource {
  key: string;
  architectures: readonly string[];
  appleSilicon: RequirementFact['appleSilicon'];
}

export interface RequirementMobileLabels {
  allChips: string;
  unknownChip: string;
  native: string;
  notNative: string;
  arch: (arch: string) => string;
}

export type RequirementMobileCard<T> = T & {
  key: string;
  chips: string;
  native?: string;
};

/**
 * One mobile card per architecture the declaration names. "Native"/"Not native" describes those
 * architectures, so it stays a status on the card instead of becoming a fake architecture card.
 */
export function requirementMobileCards<T extends RequirementMobileSource>(
  rows: readonly T[],
  labels: RequirementMobileLabels
): RequirementMobileCard<T>[] {
  return rows.flatMap(row => {
    const architectures = row.architectures.length
      ? row.architectures.map(labels.arch)
      : [row.appleSilicon === 'yes' || row.appleSilicon === 'no' ? labels.allChips : labels.unknownChip];
    const native =
      row.appleSilicon === 'yes' ? labels.native : row.appleSilicon === 'no' ? labels.notNative : undefined;
    return architectures.map((chips, index) => ({
      ...row,
      key: `${row.key}:${index}`,
      chips,
      native
    }));
  });
}
