import { describe, expect, it } from 'vitest';
import { disabledByHomebrew } from '../src/lifecycle';

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
