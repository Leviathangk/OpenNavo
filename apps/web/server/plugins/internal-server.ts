import { createServer, type IncomingMessage } from 'node:http';
import { getHeapStatistics } from 'node:v8';
import { RequestCounter, countResponse } from '../utils/crawlerMetrics';
import { InFlightCounter, localReadinessStatus, readCgroupMemoryLimit } from '../utils/internalHealth';
import { loadShedState } from '../utils/loadShed';
import { clearSitemapStorage } from '../utils/sitemapStorage';
import { cacheBuildIdentity } from './cache-storage';

const MAX_BODY_BYTES = 1024 * 1024;
const MAX_PATHS = 10000;

function readJSON(request: IncomingMessage): Promise<unknown> {
  return new Promise((resolve, reject) => {
    let size = 0;
    const chunks: Buffer[] = [];
    request.on('data', (chunk: Buffer) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error('body too large'));
        request.destroy();
      } else chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        resolve(JSON.parse(Buffer.concat(chunks).toString('utf8')));
      } catch (error) {
        reject(error);
      }
    });
    request.on('error', reject);
  });
}

// Internal port (NUXT_METRICS_PORT), reachable only inside the Docker network; Caddy never proxies
// it. GET /metrics serves crawler counters (WP8); POST /cache/invalidate and /cache/probe let the Go
// worker's URL change feed (WP4) drop cached pages and check them, and GET /cache/build tells it when
// a new release is live so it purges the long edge-cached pages once. All but /metrics are
// authenticated with NUXT_INTERNAL_SECRET.
export default defineNitroPlugin(nitroApp => {
  nitroApp.hooks.hook('close', () => loadShedState.stop());
  nitroApp.hooks.hook('afterResponse', event => {
    event.context.releaseLoadShed?.();
    event.context.releaseInternalHTTP?.();
  });
  const config = useRuntimeConfig();
  const port = Number(config.metricsPort);
  if (import.meta.dev || !Number.isInteger(port) || port <= 0) return;
  const activeHTTP = new InFlightCounter();
  nitroApp.hooks.hook('request', event => {
    const response = event.node.res;
    const release = activeHTTP.acquire();
    const finish = () => {
      release();
      response.removeListener('close', close);
    };
    const close = () => {
      release();
      response.removeListener('finish', finish);
    };
    event.context.releaseInternalHTTP = () => {
      release();
      response.removeListener('finish', finish);
      response.removeListener('close', close);
    };
    response.once('finish', finish);
    response.once('close', close);
  });
  const counter = new RequestCounter();
  nitroApp.hooks.hook('afterResponse', event => {
    countResponse(counter, event);
  });
  const server = createServer(async (request, response) => {
    const reply = (status: number, body?: unknown) => {
      response.writeHead(status, {
        'Content-Type': body === undefined ? 'text/plain' : 'application/json'
      });
      response.end(body === undefined ? '' : JSON.stringify(body));
    };
    if (request.method === 'GET' && request.url === '/healthz') return reply(200);
    if (request.method === 'GET' && request.url === '/readyz') {
      const memory = process.memoryUsage();
      const heap = getHeapStatistics();
      return reply(
        localReadinessStatus({
          rss: memory.rss,
          heapUsed: memory.heapUsed,
          heapLimit: heap.heap_size_limit,
          lagMs: loadShedState.lagMs,
          cgroupLimit: readCgroupMemoryLimit()
        })
      );
    }
    if (request.method === 'GET' && request.url === '/metrics') {
      response.writeHead(200, { 'Content-Type': 'text/plain; version=0.0.4; charset=utf-8' });
      response.end(counter.render() + loadShedState.render() + `opennavo_web_http_in_flight ${activeHTTP.value}\n`);
      return;
    }
    if (request.method === 'GET' && request.url === '/cache/build') {
      if (!authorized(request.headers.authorization, String(config.internalSecret ?? ''))) return reply(401);
      return reply(200, { build: cacheBuildIdentity(config).build });
    }
    if (request.method === 'POST' && request.url === '/cache/invalidate') {
      if (!authorized(request.headers.authorization, String(config.internalSecret ?? ''))) return reply(401);
      try {
        const body = (await readJSON(request)) as { paths?: unknown; settings?: unknown };
        const paths = Array.isArray(body.paths)
          ? body.paths.filter((path): path is string => typeof path === 'string')
          : [];
        if (!paths.length || paths.length > MAX_PATHS) return reply(400);
        // S6: a settings change reloads the process copy first, so the pages rebuilt after this purge
        // render with the new settings; the API keeps its notification until settingsCleared is true.
        const settings = body.settings === true;
        const settingsCleared = settings ? await reloadSiteSettings() : undefined;
        if (settings && !settingsCleared) return reply(503, { removed: 0, settingsCleared: false });
        if (settings) await clearSitemapStorage();
        // getKeys takes a namespace, not a string prefix: list the route caches and filter.
        const storage = useStorage('cache');
        const matches = pageCacheMatcher(paths);
        let removed = 0;
        for (const key of await storage.getKeys(PAGE_CACHE_BASE)) {
          if (!matches(key)) continue;
          await storage.removeItem(key);
          removed += 1;
        }
        return reply(200, settings ? { removed, settingsCleared: true } : { removed });
      } catch {
        return reply(400);
      }
    }
    // Renders a few changed pages in-process before IndexNow is told about them: it confirms the
    // public state (200, 301 or 410 rather than 5xx) and warms the page cache.
    if (request.method === 'POST' && request.url === '/cache/probe') {
      if (!authorized(request.headers.authorization, String(config.internalSecret ?? ''))) return reply(401);
      try {
        const body = (await readJSON(request)) as { paths?: unknown };
        const paths = Array.isArray(body.paths)
          ? body.paths.filter((path): path is string => typeof path === 'string' && path.startsWith('/'))
          : [];
        if (!paths.length || paths.length > 10) return reply(400);
        const statuses: Record<string, number> = {};
        for (const path of paths) {
          const result = await nitroApp.localFetch(path, {
            headers: { accept: 'text/html', 'user-agent': 'OpenNavo-Probe/1.0' },
            redirect: 'manual'
          });
          statuses[path] = result.status;
        }
        return reply(200, { statuses });
      } catch {
        return reply(400);
      }
    }
    reply(404);
  });
  server.on('error', error => console.error('[opennavo] internal server failed', error));
  server.listen(port, '0.0.0.0');
  nitroApp.hooks.hook('close', () => new Promise<void>(resolve => server.close(() => resolve())));
});
