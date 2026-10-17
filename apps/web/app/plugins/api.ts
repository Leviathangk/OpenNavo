import { createPublicClient } from '@opennavo/api';

// Public API client (05 §5): internal URL for SSR, public URL in browser; language follows i18n. The web
// always understands link apps (store/security plan §4), so lists without a kind filter mix them in.
export default defineNuxtPlugin(nuxtApp => {
  const config = useRuntimeConfig();
  const baseUrl = import.meta.server ? config.apiBaseInternal || config.public.apiBase : config.public.apiBase;
  const api = createPublicClient({
    baseUrl,
    locale: () => toAppLocale(nuxtApp.$i18n.locale.value),
    platform: 'web',
    version: config.public.appVersion,
    capabilities: ['link']
  });
  if (import.meta.server) {
    const clientIP = useRequestEvent()?.context.publicApiClientIP;
    if (typeof clientIP === 'string') {
      api.use({
        onRequest({ request }) {
          request.headers.set('X-Forwarded-For', clientIP);
          return request;
        }
      });
    }
  }
  return { provide: { api } };
});
