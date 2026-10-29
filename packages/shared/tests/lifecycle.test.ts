import { describe, expect, it } from 'vitest';
import { disabledByHomebrew, packageInstallState } from '../src/lifecycle';

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
