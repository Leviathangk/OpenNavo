import type { SiteSettings } from '@opennavo/api';

// Admin SEO settings (WP24), filled during SSR by app/plugins/site-settings.ts.
export function useSiteSettings() {
  return useState<SiteSettings | null>('site-settings', () => null);
}
