import { loadShedConfig, loadShedState } from '../utils/loadShed';
import { sitemapFontsEnabled } from '../utils/sitemapFonts';
import { warmSitemaps } from '../utils/sitemapWarmup';

export default defineNitroPlugin(nitroApp => {
  if (import.meta.dev || import.meta.prerender || process.env.OPENNAVO_SITEMAP_WARMUP === 'false') return;
  const capacity = loadShedConfig(useRuntimeConfig().loadShed);
  let stopped = false;
  // Defer until all plugins and routes are installed; never await a minute-long cold rebuild during startup.
  const timer = setTimeout(() => {
    void warmSitemaps(
      path => nitroApp.localFetch(path, { headers: { accept: 'application/xml' }, redirect: 'manual' }),
      () => stopped,
      () => loadShedState.inFlight === 0 && loadShedState.lagMs < capacity.lowLagMs,
      sitemapFontsEnabled(useRuntimeConfig().sitemapFonts)
    );
  }, 0);
  timer.unref?.();
  nitroApp.hooks.hook('close', () => {
    stopped = true;
    clearTimeout(timer);
  });
});
