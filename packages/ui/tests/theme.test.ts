import { afterEach, describe, expect, it, vi } from 'vitest';
import { applyThemePreference, isThemePreference, resolveTheme, themeBootScript } from '../src/utils/theme';

afterEach(() => {
  delete document.documentElement.dataset.theme;
  document.documentElement.removeAttribute('data-theme-switching');
  localStorage.clear();
  vi.useRealTimers();
});

describe('Theme preference', () => {
  it('Recognizes only the three preferences', () => {
    expect(['system', 'light', 'dark'].every(isThemePreference)).toBe(true);
    expect(isThemePreference('auto')).toBe(false);
    expect(isThemePreference(null)).toBe(false);
  });

  it('Sets data-theme for light and dark and removes it for system', () => {
    applyThemePreference('light');
    expect(document.documentElement.dataset.theme).toBe('light');
    applyThemePreference('dark');
    expect(document.documentElement.dataset.theme).toBe('dark');
    applyThemePreference('system');
    expect(document.documentElement.dataset.theme).toBeUndefined();
  });

  it('Turns transitions off only for the switching frame', () => {
    vi.useFakeTimers();
    applyThemePreference('light');
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(true);
    vi.runAllTimers();
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(false);
    // Reapplying the current choice changes nothing, so no frame is suppressed.
    applyThemePreference('light');
    expect(document.documentElement.hasAttribute('data-theme-switching')).toBe(false);
  });

  it('Resolves system from prefers-color-scheme and defaults to dark', () => {
    expect(resolveTheme('light')).toBe('light');
    const matchMedia = vi.fn().mockReturnValue({ matches: true });
    vi.stubGlobal('matchMedia', matchMedia);
    expect(resolveTheme('system')).toBe('light');
    matchMedia.mockReturnValue({ matches: false });
    expect(resolveTheme('system')).toBe('dark');
    vi.unstubAllGlobals();
  });

  it('Boot script defaults to dark and preserves explicit preferences', () => {
    const run = (stored: string | null) => {
      document.documentElement.dataset.theme = 'dark';
      if (stored === null) localStorage.removeItem('onv:theme');
      else localStorage.setItem('onv:theme', stored);
      new Function(themeBootScript('onv:theme'))();
      return document.documentElement.dataset.theme;
    };
    expect(run('light')).toBe('light');
    expect(run('dark')).toBe('dark');
    expect(run('system')).toBeUndefined();
    expect(run('<script>')).toBe('dark');
    expect(run(null)).toBe('dark');
  });
});
