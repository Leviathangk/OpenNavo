import { describe, expect, it } from 'vitest';
import { requirementMobileCards } from '../app/utils/packageRequirements';

const labels = {
  allChips: 'All chips',
  unknownChip: 'Unknown',
  native: 'Native',
  notNative: 'Not native',
  arch: (arch: string) => (arch === 'arm64' ? 'Apple silicon' : arch === 'x86_64' ? 'Intel' : arch)
};

const row = (architectures: string[], appleSilicon: 'yes' | 'no' | 'unknown' = 'yes') => ({
  key: '0:1.0.0:arm64',
  architectures,
  appleSilicon
});

describe('mobile requirement cards', () => {
  it('builds one card per declared architecture and keeps Native as a status', () => {
    const cards = requirementMobileCards([row(['arm64', 'x86_64'])], labels);
    expect(cards.map(card => card.chips)).toEqual(['Apple silicon', 'Intel']);
    expect(cards.map(card => card.native)).toEqual(['Native', 'Native']);
    expect(cards.map(card => card.key)).toEqual(['0:1.0.0:arm64:0', '0:1.0.0:arm64:1']);
    expect(cards.some(card => card.chips === 'Native')).toBe(false);
  });

  it('marks non-native rows without adding an architecture card', () => {
    const cards = requirementMobileCards([row(['arm64'], 'no')], labels);
    expect(cards).toHaveLength(1);
    expect(cards[0]).toMatchObject({ chips: 'Apple silicon', native: 'Not native' });
  });

  it('uses the all-chips label when a chip-wide declaration names no architecture', () => {
    const cards = requirementMobileCards([row([], 'yes')], labels);
    expect(cards).toHaveLength(1);
    expect(cards[0]).toMatchObject({ chips: 'All chips', native: 'Native' });
  });

  it('does not call an unknown architecture every chip', () => {
    const cards = requirementMobileCards([row([], 'unknown')], labels);
    expect(cards).toHaveLength(1);
    expect(cards[0]).toMatchObject({ chips: 'Unknown', native: undefined });
  });
});
