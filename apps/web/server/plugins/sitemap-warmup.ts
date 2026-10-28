import { loadShedConfig, loadShedState } from '../utils/loadShed';
import { warmSitemaps } from '../utils/sitemapWarmup';

export default defineNitroPlugin(nitroApp => {
  if (import.meta.dev || import.meta.prerender || process.env.OPENNAVO_SITEMAP_WARMUP === 'false') return;
  const capacity = loadShedConfig(useRuntimeConfig().loadShed);
  const config = useRuntimeConfig();
  const controller = new AbortController();
  let stopped = false;
  // Defer until all plugins and routes are installed; never await a minute-long cold rebuild during startup.
  const timer = setTimeout(() => {
    void warmSitemaps(
      (path, signal) =>
        nitroApp.localFetch(path, { headers: { accept: 'application/xml' }, redirect: 'manual', signal }),
      () => stopped,
      () => loadShedState.inFlight === 0 && loadShedState.lagMs < capacity.lowLagMs,
      async () => (await getSiteSettings()).pages.fontsSitemap,
      {
        signal: controller.signal,
        ready: async signal => {
          const url = new URL('/readyz', config.apiBaseInternal || config.public.apiBase);
          const response = await globalThis.fetch(url, { signal, redirect: 'manual' });
          await response.body?.cancel();
          return response.status === 200;
        }
      }
    );
  }, 0);
  timer.unref?.();
  nitroApp.hooks.hook('close', () => {
    stopped = true;
    controller.abort();
    clearTimeout(timer);
  });
});
