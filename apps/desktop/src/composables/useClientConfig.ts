import { unwrap } from '@opennavo/api';
import type { PublicComponents } from '@opennavo/api';
import { api } from '@/api';
import { i18n } from '@/i18n';
import { resourceCache } from './resourceCache';

export type ClientConfig = PublicComponents['schemas']['ClientConfig'];

// Isolate configuration by locale and reload after fifteen minutes; manual refresh can invalidate it sooner.
const cached = resourceCache<ClientConfig>(15 * 60_000);

export function fetchClientConfig(): Promise<ClientConfig> {
  const locale = i18n.global.locale.value;
  return cached(locale, () =>
    unwrap(
      api.GET('/config/client', {
        params: {
          query: { platform: 'desktop', version: import.meta.env.VITE_APP_VERSION ?? '0.0.0' }
        },
        headers: { 'Accept-Language': locale }
      })
    )
  );
}
