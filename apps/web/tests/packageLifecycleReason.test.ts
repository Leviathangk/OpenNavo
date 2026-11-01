import { describe, expect, it } from 'vitest';
import type { Translate } from '../app/utils/seoTitles';
import { installLifecycle, lifecycleReason } from '../app/utils/packageInstall';
import type { PackagePage } from '../app/utils/packageFacts';

const t: Translate = (key, named) =>
  ({
    'package.noReason': 'no reason given',
    'package.disabledNotice': `Homebrew has disabled this package: ${named?.reason}`,
    'package.deprecatedNotice': `Homebrew has deprecated this package: ${named?.reason}`,
    'package.staleNotice': `Paused: ${named?.reason}`
  })[key] ?? key;

const pkg = (fields: Record<string, unknown>) =>
  ({
    disabled: false,
    deprecated: false,
    disable: null,
    deprecation: null,
    recommendationPaused: false,
    homebrewStatus: null,
    ...fields
  }) as unknown as PackagePage;

// The 90-day pause writes its own reason into `disable`, and a later Homebrew disable leaves that copy in
// place. Homebrew's status is the current one, so a disabled package states it first.
const stalePauseCopy = 'No version update for more than 90 days';

describe('lifecycleReason', () => {
  it('prefers Homebrew’s current reason over a stale pause reason on the disable object', () => {
    const value = pkg({
      homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check' },
      disable: { reason: stalePauseCopy }
    });
    expect(lifecycleReason(value, 'disabled', t)).toBe('fails_gatekeeper_check');
  });

  it('falls back to the disable reason when Homebrew states none', () => {
    expect(lifecycleReason(pkg({ disable: { reason: 'discontinued' } }), 'disabled', t)).toBe('discontinued');
  });

  it('keeps the catalog’s localized pause reason for a package OpenNavo paused', () => {
    const value = pkg({
      homebrewStatus: { state: 'active', reason: 'homebrew note' },
      disable: { reason: stalePauseCopy }
    });
    expect(lifecycleReason(value, 'paused', t)).toBe(stalePauseCopy);
  });

  it('prefers the deprecation reason and falls back to Homebrew’s', () => {
    expect(
      lifecycleReason(
        pkg({ deprecation: { reason: 'unmaintained' }, homebrewStatus: { state: 'deprecated', reason: 'other' } }),
        'deprecated',
        t
      )
    ).toBe('unmaintained');
    expect(lifecycleReason(pkg({ homebrewStatus: { state: 'deprecated', reason: 'other' } }), 'deprecated', t)).toBe(
      'other'
    );
  });

  it('states that no reason was given when every source is silent', () => {
    expect(lifecycleReason(pkg({}), 'disabled', t)).toBe('no reason given');
  });
});

describe('installLifecycle notices', () => {
  it('writes the same reason into the notice the state selects', () => {
    expect(
      installLifecycle(
        pkg({
          homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check' },
          disable: { reason: stalePauseCopy }
        }),
        t
      )
    ).toEqual({ state: 'disabled', notice: 'Homebrew has disabled this package: fails_gatekeeper_check' });
    expect(installLifecycle(pkg({ homebrewStatus: { state: 'deprecated', reason: 'unmaintained' } }), t)).toEqual({
      state: 'deprecated',
      notice: 'Homebrew has deprecated this package: unmaintained'
    });
  });

  it('returns no notice for an active package', () => {
    expect(installLifecycle(pkg({ homebrewStatus: { state: 'active' } }), t)).toEqual({ state: 'active' });
  });
});
