import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import type { ReleaseEntry } from '@opennavo/api';
import OnVersionEntry from '../src/components/OnVersionEntry.vue';
import OnVersionTimeline from '../src/components/OnVersionTimeline.vue';
import { compatibilityText } from '../src/utils/compatibility';
import { withLocale } from './helpers';

const labels = {
  title: 'Compatibility',
  unknown: 'Unknown',
  minimum: 'macOS {version} or later',
  maximum: 'up to macOS {version}',
  appleSilicon: { yes: 'Native on Apple silicon', no: 'Not native on Apple silicon' }
};

type Requirement = NonNullable<ReleaseEntry['compatibility']>['requirements'][number];

const requirement = (patch: Partial<Requirement>): Requirement => ({
  versions: ['2.0'],
  architectures: [],
  platformMacOS: [],
  minimumMacOS: null,
  maximumMacOS: null,
  appleSilicon: 'unknown',
  downloadUrl: null,
  sha256: null,
  evidence: { sourceUrl: 'https://vendor.example/requirements', checkedAt: null, validUntil: null, status: 'unknown' },
  ...patch
});

function entry(compatibility?: ReleaseEntry['compatibility']): ReleaseEntry {
  return {
    id: 7,
    version: '2.0',
    publishedAt: '2026-09-30T08:00:00Z',
    source: 'webpage',
    isLatest: true,
    isPrerelease: false,
    hasNotes: false,
    sections: [],
    translation: { status: 'source', locale: 'en-US' },
    ...(compatibility ? { compatibility } : {})
  };
}

describe('Version compatibility (F6)', () => {
  it('States sourced bounds and Apple silicon support, and unknown without data', () => {
    expect(
      compatibilityText(
        { appVersion: '2.0', minimumMacOS: '13.0', maximumMacOS: null, appleSilicon: 'yes', requirements: [] },
        labels
      )
    ).toBe('macOS 13.0 or later · Native on Apple silicon');
    expect(
      compatibilityText(
        { appVersion: '2.0', minimumMacOS: '12', maximumMacOS: '15', appleSilicon: 'no', requirements: [] },
        labels
      )
    ).toBe('macOS 12 or later · up to macOS 15 · Not native on Apple silicon');
    expect(compatibilityText(undefined, labels)).toBe('Unknown');
    expect(
      compatibilityText(
        { appVersion: '2.0', minimumMacOS: null, maximumMacOS: null, appleSilicon: 'unknown', requirements: [] },
        labels
      )
    ).toBe('Unknown');
  });

  it('Keeps per-architecture bounds instead of merging them into one range', () => {
    const value = {
      appVersion: '2.0',
      minimumMacOS: null,
      maximumMacOS: null,
      appleSilicon: 'unknown' as const,
      requirements: [
        requirement({ architectures: ['arm64'], minimumMacOS: '13' }),
        requirement({ architectures: ['x86_64'], minimumMacOS: '11' }),
        requirement({ architectures: ['x86_64'], minimumMacOS: '11' })
      ]
    };
    expect(compatibilityText(value, labels)).toBe('arm64: macOS 13 or later · x86_64: macOS 11 or later');
  });

  it('Renders the row inside the entry details only when the host supplies the copy', () => {
    const shown = mount(OnVersionEntry, {
      props: {
        entry: entry({
          appVersion: '2.0',
          minimumMacOS: '14',
          maximumMacOS: null,
          appleSilicon: 'yes',
          requirements: []
        }),
        compatibilityLabels: labels
      },
      global: withLocale('en-US')
    });
    const row = shown.get('details .on-version-entry__compatibility');
    expect(row.text()).toContain('Compatibility');
    expect(row.text()).toContain('macOS 14 or later · Native on Apple silicon');

    const unknown = mount(OnVersionEntry, {
      props: { entry: entry(), compatibilityLabels: labels, collapsed: true },
      global: withLocale('en-US')
    });
    // Closed entries keep the row in their HTML, like the notes.
    expect(unknown.get('details .on-version-entry__compatibility').text()).toContain('Unknown');

    const desktop = mount(OnVersionEntry, { props: { entry: entry() }, global: withLocale('en-US') });
    expect(desktop.find('.on-version-entry__compatibility').exists()).toBe(false);
  });

  it('Passes the copy from the timeline to every entry', () => {
    const wrapper = mount(OnVersionTimeline, {
      props: {
        entries: [entry(), { ...entry(), id: 8, version: '1.9', isLatest: false }],
        compatibilityLabels: labels
      },
      global: withLocale('en-US')
    });
    expect(wrapper.findAll('.on-version-entry__compatibility')).toHaveLength(2);
  });
});
