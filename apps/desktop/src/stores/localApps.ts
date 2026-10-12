import { computed, onScopeDispose, ref } from 'vue';
import { defineStore } from 'pinia';
import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import type {
  AppError,
  AppSource,
  Footprint,
  FootprintProgress,
  LocalApp,
  LocalAppsChangedReason
} from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';

/** Days without opening an app before Installed suggests it under "Unused". */
export const UNUSED_DAYS = 90;
const DAY = 86_400_000;
/** Bursts of local-apps:changed (a footprint scan sends two per app) are read once. */
const COALESCE_MS = 150;

/** Source filter on Installed: App Store keeps its own segment; drag-installed and .pkg apps share "Other". */
export type SourceFilter = 'all' | 'homebrew' | 'app_store' | 'other';

export function matchesSource(app: LocalApp, filter: SourceFilter): boolean {
  if (filter === 'all') return true;
  if (filter === 'other') return app.source === 'direct' || app.source === 'pkg';
  return app.source === (filter satisfies AppSource);
}

/**
 * Unused = known last-used time (Unix ms) older than UNUSED_DAYS; unknown times never count (Spotlight may be off),
 * and a running app is in use whatever Spotlight says (login items rarely update it).
 */
export function isUnused(app: LocalApp, now = Date.now()): boolean {
  return app.removable && !app.running && app.lastUsedAt !== null && now - app.lastUsedAt >= UNUSED_DAYS * DAY;
}

/** Icon exported by Rust into the asset-protocol cache; browser mode (dev:web) falls back to letter tiles. */
export function localAppIcon(app: LocalApp | undefined): string | null {
  return app?.iconPath && isTauri() ? convertFileSrc(app.iconPath) : null;
}

/** Every app on this Mac (Installed, uninstall): local_apps_list returns the cache immediately; reread on local-apps:changed. */
export const useLocalAppsStore = defineStore('localApps', () => {
  const items = ref<LocalApp[]>([]);
  const loaded = ref(false);
  const refreshing = ref(false);
  let sequence = 0;
  const footprints = ref<Record<string, Footprint>>({});
  const footprintErrors = ref<Record<string, AppError>>({});
  const scanProgress = ref<FootprintProgress | null>(null);
  let footprintGeneration = 0;
  let cacheSequence = 0;
  let disposed = false;
  const retrying = new Set<string>();
  const pending = new Map<string, Promise<Footprint | null>>();
  const occupiedBytes = (id: string): number | null => {
    const value = footprints.value[id];
    return value ? value.appBytes + value.dataBytes + value.cacheBytes : null;
  };
  /** Space an app takes, as the Installed size column shows it: its footprint, or the bundle until that is known. */
  const spaceOf = (app: LocalApp): number => occupiedBytes(app.id) ?? app.sizeBytes ?? 0;

  async function footprint(id: string): Promise<Footprint | null> {
    if (disposed || footprintErrors.value[id]) return null;
    if (footprints.value[id]) return footprints.value[id];
    const generation = footprintGeneration;
    const key = `${generation}:${id}`;
    const existing = pending.get(key);
    if (existing) return existing;
    const request = unwrap(commands.localAppFootprint(id))
      .catch(() => null)
      .then(result => {
        if (result && !disposed && generation === footprintGeneration && find(id)) footprints.value[id] = result;
        return generation === footprintGeneration ? result : null;
      })
      .finally(() => pending.delete(key));
    pending.set(key, request);
    return request;
  }

  // Native code owns scheduling and retry. Events only fetch its current report.
  async function loadFootprints() {
    const current = footprintGeneration;
    const request = ++cacheSequence;
    const result = await unwrap(commands.localAppFootprints()).catch(() => null);
    if (result && !disposed && current === footprintGeneration && request === cacheSequence) {
      footprints.value = result.values;
      footprintErrors.value = result.errors;
      scanProgress.value = result.progress;
    }
  }

  /**
   * Start measuring footprints; `force` recalculates every app. Values on screen stay until new ones replace them:
   * blanking the whole column made the table reshuffle for a minute on every visit.
   */
  async function scan(force = false) {
    if (refreshing.value) return;
    refreshing.value = true;
    // Answers of single-app requests issued before this point would be older than the scan's.
    if (force) footprintGeneration++;
    try {
      await unwrap(commands.localAppsScanFootprints(force));
    } finally {
      refreshing.value = false;
      await loadFootprints();
    }
  }
  async function retryFootprint(id: string) {
    if (disposed || retrying.has(id) || !footprintErrors.value[id]) return;
    retrying.add(id);
    try {
      await unwrap(commands.localAppRetryFootprint(id));
      if (!disposed) {
        delete footprintErrors.value[id];
        await loadFootprints();
      }
    } finally {
      retrying.delete(id);
    }
  }
  async function enterInstalled() {
    await load();
    await scan();
  }

  const byId = computed(() => new Map(items.value.map(app => [app.id, app])));
  const byCask = computed(() => new Map(items.value.flatMap(app => (app.cask ? [[app.cask, app] as const] : []))));
  const find = (id: string) => byId.value.get(id);
  const findCask = (token: string) => byCask.value.get(token);

  function accept(current: number, result: LocalApp[]) {
    if (current !== sequence) return;
    items.value = result;
    loaded.value = true;
    for (const id of Object.keys(footprints.value)) {
      if (!result.some(app => app.id === id)) delete footprints.value[id];
    }
    void loadFootprints();
  }

  async function load() {
    const current = ++sequence;
    accept(current, await unwrap(commands.localAppsList()));
  }

  /** Re-enumerate the Applications folders; sizes keep arriving through local-apps:changed. */
  async function refresh() {
    if (refreshing.value) return;
    const current = ++sequence;
    refreshing.value = true;
    try {
      accept(current, await unwrap(commands.localAppsRefresh()));
    } finally {
      refreshing.value = false;
    }
  }

  // local-apps:changed: a footprint change needs only the report; anything else rereads the list (and the report).
  let listTimer: ReturnType<typeof setTimeout> | undefined;
  let reportTimer: ReturnType<typeof setTimeout> | undefined;
  function changed(reason: LocalAppsChangedReason) {
    if (disposed) return;
    if (reason === 'footprints') {
      reportTimer ??= setTimeout(() => {
        reportTimer = undefined;
        void loadFootprints();
      }, COALESCE_MS);
      return;
    }
    listTimer ??= setTimeout(() => {
      listTimer = undefined;
      void load().catch(() => undefined);
    }, COALESCE_MS);
  }

  onScopeDispose(() => {
    disposed = true;
    footprintGeneration++;
    pending.clear();
    clearTimeout(listTimer);
    clearTimeout(reportTimer);
  });

  return {
    items,
    loaded,
    refreshing,
    footprints,
    footprintErrors,
    scanProgress,
    scan,
    retryFootprint,
    enterInstalled,
    occupiedBytes,
    spaceOf,
    footprint,
    loadFootprints,
    byId,
    find,
    findCask,
    load,
    refresh,
    changed
  };
});
