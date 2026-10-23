import { applyThemePreference } from '@opennavo/ui';
import type { ThemePreference } from '@opennavo/ui';
import { THEME_STORAGE_KEY } from '~~/shared/utils/theme';

// The head script already applied a saved theme to the page; after hydration the switch shows it too, and a
// choice made in another tab applies here as well.
export default defineNuxtPlugin(nuxtApp => {
  const preference = useState<ThemePreference>('onv:theme', () => 'dark');
  nuxtApp.hook('app:mounted', () => {
    preference.value = readThemePreference();
  });
  window.addEventListener('storage', event => {
    if (event.key !== null && event.key !== THEME_STORAGE_KEY) return;
    preference.value = readThemePreference();
    applyThemePreference(preference.value);
  });
});
