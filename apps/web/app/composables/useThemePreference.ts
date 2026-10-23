import { applyThemePreference, isThemePreference } from '@opennavo/ui';
import type { ThemePreference } from '@opennavo/ui';
import { THEME_STORAGE_KEY } from '~~/shared/utils/theme';

/** Saved theme choice, or dark when none is saved or storage is unavailable. */
export function readThemePreference(): ThemePreference {
  try {
    const value = localStorage.getItem(THEME_STORAGE_KEY);
    return isThemePreference(value) ? value : 'dark';
  } catch {
    return 'dark';
  }
}

// Color theme switch (08 §3.7): dark unless the visitor picks another preference. The server renders dark so the
// HTML stays shareable; plugins/theme.client.ts reads the saved choice after hydration.
export function useThemePreference() {
  const preference = useState<ThemePreference>('onv:theme', () => 'dark');

  function set(next: ThemePreference) {
    preference.value = next;
    try {
      localStorage.setItem(THEME_STORAGE_KEY, next);
    } catch {
      // Private browsing may refuse storage; the choice then lasts for this page only.
    }
    applyThemePreference(next);
  }

  return { preference: readonly(preference), set };
}
