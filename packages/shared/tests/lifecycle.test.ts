import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { disabledByHomebrew, lifecycleReasonOf, packageInstallState } from '../src/lifecycle';

describe('disabledByHomebrew', () => {
  it('Is false for packages the catalog has not disabled', () => {
    expect(disabledByHomebrew({ disabled: false, homebrewStatus: { state: 'disabled' } })).toBe(false);
    expect(disabledByHomebrew({})).toBe(false);
  });

  it('Tells Homebrew disabling apart from the 90-day pause', () => {
    expect(disabledByHomebrew({ disabled: true, homebrewStatus: { state: 'disabled' } })).toBe(true);
    expect(disabledByHomebrew({ disabled: true, homebrewStatus: { state: 'active' } })).toBe(false);
    expect(disabledByHomebrew({ disabled: true, homebrewStatus: { state: 'deprecated' } })).toBe(false);
  });

  it('Keeps the flag when Homebrew’s status is unknown', () => {
    expect(disabledByHomebrew({ disabled: true })).toBe(true);
    expect(disabledByHomebrew({ disabled: true, homebrewStatus: null })).toBe(true);
  });
});

describe('Package install availability', () => {
  it('blocks Homebrew disabled and legacy disabled apps, including a missing disable object', () => {
    expect(packageInstallState({ homebrewStatus: { state: 'disabled' } })).toBe('disabled');
    expect(packageInstallState({ disabled: true })).toBe('disabled');
    expect(packageInstallState({ disable: { reason: 'discontinued' } })).toBe('disabled');
  });

  it('keeps deprecated and recommendation-paused apps installable with distinct warnings', () => {
    expect(packageInstallState({ deprecation: { reason: 'unmaintained' } })).toBe('deprecated');
    expect(packageInstallState({ homebrewStatus: { state: 'deprecated' }, disabled: true })).toBe('deprecated');
    expect(
      packageInstallState({ homebrewStatus: { state: 'active' }, disabled: true, disable: { reason: 'stale' } })
    ).toBe('paused');
    expect(packageInstallState({ recommendationPaused: true })).toBe('paused');
    expect(packageInstallState({ homebrewStatus: { state: 'active' } })).toBe('active');
  });

  it('gives disabled priority over deprecated and paused', () => {
    expect(
      packageInstallState({ homebrewStatus: { state: 'disabled' }, deprecated: true, recommendationPaused: true })
    ).toBe('disabled');
  });
});

const lifecycleCases = JSON.parse(
  readFileSync(new URL('./fixtures/packageInstallStates.json', import.meta.url), 'utf8')
) as {
  name: string;
  fields: Parameters<typeof packageInstallState>[0];
  expected: ReturnType<typeof packageInstallState>;
}[];

it.each(lifecycleCases)('shares the lifecycle priority for $name', ({ fields, expected }) => {
  expect(packageInstallState(fields)).toBe(expected);
});

describe('lifecycleReasonOf', () => {
  // The 90-day pause writes its copy into `disable` and leaves it behind when Homebrew later disables the app.
  const stalePauseCopy = 'No version update for more than 90 days';

  it('prefers Homebrew’s current reason over a stale pause reason when disabled', () => {
    expect(
      lifecycleReasonOf(
        {
          homebrewStatus: { state: 'disabled', reason: 'fails_gatekeeper_check' },
          disable: { reason: stalePauseCopy }
        },
        'disabled'
      )
    ).toBe('fails_gatekeeper_check');
  });

  it('falls back to the disable reason when disabled and Homebrew states none', () => {
    expect(lifecycleReasonOf({ disable: { reason: 'discontinued' } }, 'disabled')).toBe('discontinued');
  });

  it('keeps the catalog pause reason for a package OpenNavo paused', () => {
    expect(
      lifecycleReasonOf(
        { homebrewStatus: { state: 'active', reason: 'homebrew note' }, disable: { reason: stalePauseCopy } },
        'paused'
      )
    ).toBe(stalePauseCopy);
  });

  it('prefers the deprecation reason and falls back to Homebrew’s', () => {
    expect(
      lifecycleReasonOf(
        { deprecation: { reason: 'unmaintained' }, homebrewStatus: { state: 'deprecated', reason: 'other' } },
        'deprecated'
      )
    ).toBe('unmaintained');
    expect(lifecycleReasonOf({ homebrewStatus: { state: 'deprecated', reason: 'other' } }, 'deprecated')).toBe('other');
  });

  it('returns null when every source is silent, so the caller writes its own text', () => {
    expect(lifecycleReasonOf({}, 'disabled')).toBeNull();
    expect(lifecycleReasonOf({}, 'paused')).toBeNull();
  });
});
