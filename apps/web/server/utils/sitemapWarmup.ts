import { packageSitemaps } from '../../shared/utils/sitemapBuckets';

export const SITEMAP_WARMUP_PATHS = [
  '/sitemap_index.xml',
  ...Object.keys(packageSitemaps).map(name => `/__sitemap__/${name}.xml`)
];

export interface WarmupOptions {
  signal?: AbortSignal;
  ready?: (signal: AbortSignal) => Promise<boolean>;
}

/** Abort both waits and our ownership of in-flight work, even if a transport ignores cancellation. */
function cancellable<T>(work: Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const abort = () => reject(new Error('Warmup cancelled'));
    work.then(resolve, reject).finally(() => signal.removeEventListener('abort', abort));
    if (signal.aborted) abort();
    else signal.addEventListener('abort', abort, { once: true });
  });
}

function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const abort = () => {
      clearTimeout(timer);
      reject(new Error('Warmup cancelled'));
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', abort);
      resolve();
    }, ms);
    signal.addEventListener('abort', abort, { once: true });
    if (signal.aborted) abort();
  });
}

/** Readiness probes bypass Nitro; XML rendering remains sequential and yields to foreground work. */
export async function warmSitemaps(
  fetch: (path: string, signal?: AbortSignal) => Promise<{ status: number }>,
  stopped: () => boolean = () => false,
  hasCapacity: () => boolean = () => true,
  includeFonts: boolean | (() => Promise<boolean>) = true,
  options: WarmupOptions = {}
): Promise<void> {
  const signal = options.signal ?? new AbortController().signal;
  const started = Date.now();
  let paths = SITEMAP_WARMUP_PATHS;
  let success = 0;
  let failed = 0;
  let outcome = 'complete';
  const cancelled = () => signal.aborted || stopped();
  try {
    if (options.ready) {
      const deadline = started + 10 * 60_000;
      let ready = false;
      while (!cancelled() && Date.now() < deadline) {
        const probe = new AbortController();
        const abort = () => probe.abort();
        signal.addEventListener('abort', abort, { once: true });
        const timeout = setTimeout(abort, Math.min(5000, deadline - Date.now()));
        try {
          ready = await cancellable(options.ready(probe.signal), probe.signal);
        } catch {
          /* Connection refusal and timeout are expected while the API starts. */
        } finally {
          clearTimeout(timeout);
          signal.removeEventListener('abort', abort);
        }
        if (ready) break;
        await pause(Math.min(15_000, Math.max(0, deadline - Date.now())), signal);
      }
      if (!ready && !cancelled()) {
        outcome = 'readiness_timeout';
        failed =
          typeof includeFonts === 'boolean' && !includeFonts
            ? paths.filter(path => !path.includes('/fonts-bucket-')).length
            : paths.length;
        return;
      }
    }
    if (cancelled()) return;
    const fontsEnabled = typeof includeFonts === 'function' ? await cancellable(includeFonts(), signal) : includeFonts;
    paths = paths.filter(path => fontsEnabled || !path.includes('/fonts-bucket-'));
    for (const path of paths) {
      if (cancelled()) return;
      let ok = false;
      for (let attempt = 0; attempt < 3; attempt++) {
        await pause(attempt ? 15_000 : 0, signal);
        while (!cancelled() && !hasCapacity()) await pause(100, signal);
        if (cancelled()) return;
        try {
          const response = await cancellable(fetch(path, signal), signal);
          if (cancelled()) return;
          ok = response.status === 200;
          if (ok || response.status < 500) break;
        } catch {
          if (cancelled()) return;
        }
      }
      if (ok) success++;
      else {
        failed++;
        console.warn(`[opennavo] sitemap warmup failed: ${path}`);
      }
    }
  } catch {
    if (!cancelled()) outcome = 'error';
  } finally {
    if (cancelled()) outcome = 'cancelled';
    console.info(
      `[opennavo] sitemap warmup summary: success=${success} failed=${failed} durationMs=${Date.now() - started} outcome=${outcome}`
    );
  }
}
