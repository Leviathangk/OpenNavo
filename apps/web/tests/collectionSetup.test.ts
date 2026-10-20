import { describe, expect, it } from 'vitest';
import type { PublicComponents } from '@opennavo/api';
import {
  compatibilityRows,
  externalEntries,
  formatTotal,
  installableEntries,
  pricePeriod,
  regionName
} from '../app/utils/collectionSetup';
import type { CollectionDetail } from '../app/utils/collectionSetup';

type RequirementFact = PublicComponents['schemas']['RequirementFact'];
type PackageSummary = PublicComponents['schemas']['PackageSummary'];

const pkg = (token: string, kind: PackageSummary['kind'] = 'cask'): PackageSummary => ({
  kind,
  token,
  name: token,
  displayName: token.toUpperCase(),
  summary: null,
  iconUrl: null,
  accentColor: null,
  version: '1.0',
  primaryCategory: null,
  installs30d: 0,
  rank30d: null,
  autoUpdates: false,
  deprecated: false,
  disabled: false,
  isFont: false,
  isLibrary: false,
  editorChoice: false,
  versionChangedAt: '2026-09-30T08:00:00Z'
});

const requirement = (patch: Partial<RequirementFact>): RequirementFact => ({
  versions: [],
  architectures: [],
  platformMacOS: [],
  minimumMacOS: null,
  maximumMacOS: null,
  appleSilicon: 'unknown',
  downloadUrl: null,
  sha256: null,
  evidence: { sourceUrl: 'https://formulae.brew.sh/', checkedAt: null, validUntil: null, status: 'unknown' },
  ...patch
});

const detail = (setup: CollectionDetail['setup']): Pick<CollectionDetail, 'items' | 'setup'> => ({
  items: [{ package: pkg('alpha') }, { package: pkg('beta') }, { package: pkg('linked', 'link') }],
  setup
});

const setup = (
  patch: Partial<NonNullable<CollectionDetail['setup']>> = {}
): NonNullable<CollectionDetail['setup']> => ({
  criteria: null,
  installableTokens: ['alpha', 'beta'],
  externalApps: [{ token: 'linked', url: 'https://linked.example/' }],
  brewfile: 'cask "alpha"\ncask "beta"\n',
  priceTotals: [],
  priceUnknownCount: 0,
  compatibility: [],
  ...patch
});

const labels = { minimum: 'macOS {version} or later', maximum: 'up to macOS {version}' };

describe('Collection setup (F4)', () => {
  it('Installs exactly the Brewfile casks and lists link apps on their own with their sites', () => {
    const value = detail(setup());
    expect(installableEntries(value).map(entry => entry.token)).toEqual(['alpha', 'beta']);
    expect(installableEntries(value)[0]).toEqual({ token: 'alpha', name: 'ALPHA', path: '/apps/alpha' });
    expect(externalEntries(value)).toEqual([
      { token: 'linked', name: 'LINKED', path: '/apps/linked', url: 'https://linked.example/' }
    ]);
    // The link app never becomes something "Install all" or the Brewfile covers.
    expect(installableEntries(value).some(entry => entry.token === 'linked')).toBe(false);
    expect(installableEntries(detail(undefined))).toEqual([]);
  });

  it('Formats each total in its own currency and never guesses a billing period', () => {
    expect(formatTotal({ amount: '19.98', currency: 'USD' }, 'en-US')).toBe('$19.98');
    expect(formatTotal({ amount: '0.3', currency: 'EUR' }, 'en-US')).toBe('€0.30');
    expect(formatTotal({ amount: '5', currency: 'NOTACODE' }, 'en-US')).toBe('5 NOTACODE');
    expect(pricePeriod('yearly')).toBe('yearly');
    expect(pricePeriod('weekly')).toBe('unknown');
    expect(regionName('US', 'en-US')).toBe('United States');
    expect(regionName('us', 'zh-CN')).toBe('美国');
  });

  it('States sourced bounds per app, keeping architectures apart only when they differ', () => {
    const rows = compatibilityRows(
      detail(
        setup({
          compatibility: [
            {
              token: 'alpha',
              requirements: [
                requirement({ architectures: ['arm64'], minimumMacOS: '13', appleSilicon: 'yes' }),
                requirement({ architectures: ['x86_64'], minimumMacOS: '12', appleSilicon: 'unknown' })
              ]
            },
            {
              token: 'beta',
              requirements: [
                requirement({ architectures: ['arm64'], minimumMacOS: '14', maximumMacOS: '26', appleSilicon: 'no' }),
                requirement({ architectures: ['x86_64'], minimumMacOS: '14', maximumMacOS: '26' })
              ]
            }
          ]
        })
      ),
      labels
    );
    expect(rows.map(row => [row.token, row.macos, row.appleSilicon])).toEqual([
      ['alpha', ['arm64: macOS 13 or later', 'x86_64: macOS 12 or later'], 'yes'],
      ['beta', ['macOS 14 or later, up to macOS 26'], 'no'],
      // No facts for the link app: unknown, never assumed.
      ['linked', [], 'unknown']
    ]);
  });
});
