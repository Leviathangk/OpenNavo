// Color theme (08 §3.7): dark is the default; light follows prefers-color-scheme unless html carries data-theme.
// @opennavo/tokens/tokens.css defines both themes, so applying a preference only sets or removes the attribute.

export type ThemePreference = 'system' | 'light' | 'dark';

export const THEME_PREFERENCES: readonly ThemePreference[] = ['system', 'light', 'dark'];

export function isThemePreference(value: unknown): value is ThemePreference {
  return typeof value === 'string' && (THEME_PREFERENCES as readonly string[]).includes(value);
}

/** The theme the page shows for a preference: system reads prefers-color-scheme (dark when unknown, as on the server). */
export function resolveTheme(preference: ThemePreference): 'light' | 'dark' {
  if (preference !== 'system') return preference;
  if (typeof window === 'undefined' || !window.matchMedia) return 'dark';
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}

/**
 * Apply a preference to the document: light/dark set data-theme, system removes it so the media query decides.
 * Transitions stay off for one frame (base.css [data-theme-switching]) so every color changes at once
 * instead of fading control by control.
 */
export function applyThemePreference(preference: ThemePreference, root?: HTMLElement): void {
  if (typeof document === 'undefined') return;
  const element = root ?? document.documentElement;
  const next = preference === 'system' ? undefined : preference;
  if (element.dataset.theme === next) return;
  element.setAttribute('data-theme-switching', '');
  if (next) element.dataset.theme = next;
  else delete element.dataset.theme;
  // Flush styles while transitions are disabled, then restore them after the new colors have painted.
  void window.getComputedStyle(element).color;
  window.setTimeout(() => element.removeAttribute('data-theme-switching'), 0);
}

/**
 * Inline script for the document head that applies a saved preference before first paint, so a forced
 * theme never flashes the other one. Keep it tiny and dependency-free; it runs before any bundle loads.
 */
export function themeBootScript(storageKey: string): string {
  return `var t="dark";try{var s=localStorage.getItem(${JSON.stringify(storageKey)});if(s==="light"||s==="dark"||s==="system")t=s}catch(e){}if(t==="system")delete document.documentElement.dataset.theme;else document.documentElement.dataset.theme=t`;
}
