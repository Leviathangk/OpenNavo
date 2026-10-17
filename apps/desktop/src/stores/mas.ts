import { ref, shallowRef } from 'vue';
import { defineStore } from 'pinia';
import type { AppStoreChangeReason, MasApp, MasInstalled, MasStatus } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';

const SEARCH_TTL = 10 * 60 * 1000;
const LOOKUP_TTL = 6 * 60 * 60 * 1000;
const SEARCH_CACHE_SIZE = 50;

/**
 * Mac App Store (store/security plan §5): everything goes straight to Apple from this Mac, never through our server.
 * Rust rate-limits the iTunes API (about 20 calls a minute, shared by search and lookup); this store avoids spending
 * that budget twice by caching searches for 10 minutes and records for 6 hours, misses included.
 */
export const useMasStore = defineStore('mas', () => {
  const status = ref<MasStatus | null>(null);
  const installed = shallowRef<MasInstalled[]>([]);
  const searches = new Map<string, { at: number; items: MasApp[] }>();
  const records = new Map<number, { at: number; app: MasApp }>();
  const bundles = new Map<string, { at: number; adamId: number | null }>();

  let statusLoad: Promise<MasStatus> | null = null;
  let statusRevision = 0;
  function loadStatus(): Promise<MasStatus> {
    if (statusLoad) return statusLoad;
    const revision = statusRevision;
    const request = unwrap(commands.masStatus())
      .then(value => {
        if (revision === statusRevision) status.value = value;
        return value;
      })
      .finally(() => {
        if (statusLoad === request) statusLoad = null;
      });
    statusLoad = request;
    return request;
  }
  async function ensureStatus(): Promise<MasStatus> {
    for (;;) {
      if (status.value) return status.value;
      const revision = statusRevision;
      const value = await loadStatus();
      if (revision === statusRevision) return value;
    }
  }

  function remember(apps: MasApp[], at = Date.now()) {
    for (const app of apps) {
      records.set(app.adamId, { at, app });
      bundles.set(app.bundleId, { at, adamId: app.adamId });
    }
  }

  /** Search the storefront; the key includes the country so a region change never shows another store's results. */
  async function search(term: string, limit = 20): Promise<MasApp[]> {
    const q = term.trim().toLowerCase();
    if (!q) return [];
    const revision = statusRevision;
    const country = (await ensureStatus()).country;
    if (revision !== statusRevision) return search(term, limit);
    const key = `${country}|${limit}|${q}`;
    const hit = searches.get(key);
    if (hit && Date.now() - hit.at < SEARCH_TTL) return hit.items;
    const items = await unwrap(commands.masSearch(q, limit));
    if (revision !== statusRevision) return search(term, limit);
    searches.delete(key);
    searches.set(key, { at: Date.now(), items });
    // Oldest entries go first (Map keeps insertion order).
    while (searches.size > SEARCH_CACHE_SIZE) searches.delete(searches.keys().next().value as string);
    remember(items);
    return items;
  }

  async function get(adamId: number): Promise<MasApp | null> {
    const hit = records.get(adamId);
    if (hit && Date.now() - hit.at < LOOKUP_TTL) return hit.app;
    const revision = statusRevision;
    const [app] = await unwrap(commands.masLookup([adamId]));
    if (revision !== statusRevision) return get(adamId);
    if (app) remember([app]);
    return app ?? null;
  }

  /** Store records for catalog apps by bundle ID; misses are cached too, so a detail page asks Apple once. */
  async function byBundles(bundleIds: string[]): Promise<Map<string, MasApp>> {
    const revision = statusRevision;
    const now = Date.now();
    const fresh = (id: string) => {
      const entry = bundles.get(id);
      return entry && now - entry.at < LOOKUP_TTL ? entry : null;
    };
    const missing = [...new Set(bundleIds)].filter(id => !fresh(id));
    if (missing.length) {
      const found = await unwrap(commands.masLookupBundles(missing));
      if (revision !== statusRevision) return byBundles(bundleIds);
      for (const id of missing) {
        const app = found[id];
        if (app) remember([app], now);
        else bundles.set(id, { at: now, adamId: null });
      }
    }
    const result = new Map<string, MasApp>();
    for (const id of bundleIds) {
      const adamId = fresh(id)?.adamId;
      const app = adamId == null ? undefined : records.get(adamId)?.app;
      if (app) result.set(id, app);
    }
    return result;
  }

  async function loadInstalled() {
    installed.value = await unwrap(commands.masInstalled());
  }

  const installedFor = (adamId: number) => installed.value.find(app => app.adamId === adamId) ?? null;
  /** A record already fetched (search, lookup or bundle lookup), without asking Apple; task cards use it. */
  const peek = (adamId: number) => records.get(adamId)?.app ?? null;

  async function openStore(adamId: number) {
    await unwrap(commands.masOpenStore(adamId));
  }

  /** Region changes invalidate everything that came from the old storefront. */
  function reset() {
    searches.clear();
    records.clear();
    bundles.clear();
    ++statusRevision;
    statusLoad = null;
    status.value = null;
  }

  /** appstore:changed (plan §11.3): an install finished or the storefront changed; reread what it affects. */
  function changed(reason: AppStoreChangeReason) {
    if (reason === 'region') {
      reset();
      void loadStatus().catch(() => undefined);
    }
    if (reason !== 'updates') void loadInstalled().catch(() => undefined);
  }

  return {
    status,
    installed,
    loadStatus,
    ensureStatus,
    search,
    get,
    byBundles,
    loadInstalled,
    installedFor,
    peek,
    openStore,
    reset,
    changed
  };
});
