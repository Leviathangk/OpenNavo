import { computed, onScopeDispose, ref } from 'vue';
import { defineStore } from 'pinia';
import { convertFileSrc, isTauri } from '@tauri-apps/api/core';
import type {
  AppError,
  AppSource,
  Footprint,
  FootprintProgress,
  LocalApp,
  LocalAppsChangedReason,
  Task
} from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { SETTLE_LIMIT } from './library';

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

/** Cask task targets carry a site alias or the complete Homebrew installation identity. */
export function matchesCask(app: LocalApp, token: string): boolean {
  return Boolean((app.cask || app.source === 'homebrew') && (app.cask === token || app.catalogToken === token));
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
  // Apps whose uninstall or adoption just succeeded: the list Rust rereads after the task (for casks, after the
  // Homebrew inventory) lands seconds later, so the change applies here until then. Rust announces that reread,
  // and any undo, as a "task" change; `signal` counts those, and a read started after one ends the override.
  let signal = 0;
  const removals = new Map<string, { at: number; signal: number }>();
  const adoptions = new Map<string, { cask: string; at: number; signal: number }>();
  const adopted = (app: LocalApp): LocalApp => {
    const adoption = adoptions.get(app.id);
    // The alias is known now; the full Homebrew identity arrives with the inventory reread.
    return adoption ? { ...app, catalogToken: adoption.cask, homebrewCask: null, source: 'homebrew' } : app;
  };
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
  const byCask = computed(
    () =>
      new Map(
        items.value.flatMap(app =>
          app.cask || app.source === 'homebrew'
            ? [app.cask, app.catalogToken]
                .filter((token): token is string => Boolean(token))
                .map(token => [token, app] as const)
            : []
        )
      )
  );
  const find = (id: string) => byId.value.get(id);
  const findCask = (token: string) => byCask.value.get(token);

  /** seen: the task changes announced when the read started, so it reflects them. */
  function accept(current: number, result: LocalApp[], seen: number) {
    if (current !== sequence) return;
    const listed = new Map(result.map(app => [app.id, app]));
    const now = Date.now();
    const settled = (entry: { at: number; signal: number }) => seen > entry.signal || now - entry.at > SETTLE_LIMIT;
    for (const [id, removal] of removals) if (!listed.has(id) || settled(removal)) removals.delete(id);
    for (const [id, adoption] of adoptions) {
      const app = listed.get(id);
      if (!app || matchesCask(app, adoption.cask) || settled(adoption)) adoptions.delete(id);
    }
    const list = result.filter(app => !removals.has(app.id)).map(adopted);
    items.value = list;
    loaded.value = true;
    for (const id of Object.keys(footprints.value)) {
      if (!list.some(app => app.id === id)) delete footprints.value[id];
    }
    void loadFootprints();
  }

  /**
   * Apply a finished uninstall (drop the app, or a cask's apps) or adoption (the app turns Homebrew-managed)
   * before Rust's reread shows it.
   */
  function settle(task: Task) {
    if (task.state !== 'succeeded' || !task.target) return;
    const { kind, token } = task.target;
    const at = task.finishedAt ?? Date.now();
    if (task.op === 'install' && kind === 'cask' && task.options.adopt) {
      const taken = items.value.filter(app => !app.cask && app.catalogToken === token && app.source !== 'app_store');
      for (const app of taken) adoptions.set(app.id, { cask: token, at, signal });
      if (taken.length) items.value = items.value.map(adopted);
      return;
    }
    if (task.op !== 'uninstall') return;
    const gone = items.value.filter(app =>
      kind === 'app' ? app.id === token : kind === 'cask' && matchesCask(app, token)
    );
    if (!gone.length) return;
    for (const app of gone) removals.set(app.id, { at, signal });
    items.value = items.value.filter(app => !removals.has(app.id));
  }

  async function load() {
    const current = ++sequence;
    const seen = signal;
    accept(current, await unwrap(commands.localAppsList()), seen);
  }

  /** Re-enumerate the Applications folders; sizes keep arriving through local-apps:changed. */
  async function refresh() {
    if (refreshing.value) return;
    const current = ++sequence;
    const seen = signal;
    refreshing.value = true;
    try {
      accept(current, await unwrap(commands.localAppsRefresh()), seen);
    } finally {
      refreshing.value = false;
    }
  }

  // local-apps:changed: a footprint change needs only the report; anything else rereads the list (and the report).
  let listTimer: ReturnType<typeof setTimeout> | undefined;
  let reportTimer: ReturnType<typeof setTimeout> | undefined;
  function changed(reason: LocalAppsChangedReason) {
    if (disposed) return;
    if (reason === 'task') signal++;
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
    changed,
    settle
  };
});
