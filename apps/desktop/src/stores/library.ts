import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { InstalledItem, Kind, StorageSummary, Task } from '@/ipc/bindings';
import { IpcError, commands, packageKey, unwrap } from '@/ipc/client';

// Cask-only catalog (ADR-018): Homebrew command-line tools are outside OpenNavo's management scope and filtered immediately after reading.
// Sidebar counts, installed tables, and detail states all use the same list.
const onlyCasks = (list: InstalledItem[]) => list.filter(item => item.kind === 'cask');
/** Longest a just-finished task overrides a list, e.g. when brew records the result under a renamed token. */
export const SETTLE_LIMIT = 60_000;

/** Installed packages (06 §6.7): library_list immediately returns memory cache; reread on library:changed. */
export const useLibraryStore = defineStore('library', () => {
  const items = ref<InstalledItem[]>([]);
  const loaded = ref(false);
  const refreshing = ref(false);
  let pendingRefresh: Promise<InstalledItem[]> | undefined;
  let listSequence = 0;
  let storageSequence = 0;
  // Cask installs, uninstalls and pins that just succeeded, with their finish time. Rust rereads the whole
  // Homebrew inventory after a task, which takes seconds and lands after the task's own event; until a list
  // shows the change it is applied here, so no button falls back to its old state for a finished task.
  const installs = new Map<string, { item: InstalledItem; at: number }>();
  const removals = new Map<string, number>();
  const pins = new Map<string, { pinned: boolean; at: number }>();
  const storage = ref<StorageSummary | null>(null);

  const byKey = computed(() => new Map(items.value.map(item => [packageKey(item.kind, item.token), item])));
  const find = (kind: Kind, token: string) =>
    byKey.value.get(packageKey(kind, token)) ??
    items.value.find(item => item.kind === kind && item.fullToken === token);

  function accept(list: InstalledItem[]) {
    const listed = new Map(list.map(item => [packageKey(item.kind, item.token), item]));
    const now = Date.now();
    for (const [key, entry] of installs) if (listed.has(key) || now - entry.at > SETTLE_LIMIT) installs.delete(key);
    for (const [key, at] of removals) if (!listed.has(key) || now - at > SETTLE_LIMIT) removals.delete(key);
    for (const [key, pin] of pins) {
      const item = listed.get(key);
      if (!item || item.pinned === pin.pinned || now - pin.at > SETTLE_LIMIT) pins.delete(key);
    }
    items.value = [
      ...list
        .filter(item => !removals.has(packageKey(item.kind, item.token)))
        .map(item => {
          const pin = pins.get(packageKey(item.kind, item.token));
          return pin ? { ...item, pinned: pin.pinned } : item;
        }),
      ...[...installs.values()].map(entry => entry.item)
    ];
    loaded.value = true;
  }

  /** Apply a finished cask install, uninstall, pin or unpin before Rust's inventory reread shows it. */
  function settle(task: Task) {
    if (task.state !== 'succeeded' || task.target?.kind !== 'cask') return;
    const token = find('cask', task.target.token)?.token ?? task.target.token;
    const key = packageKey('cask', token);
    const at = task.finishedAt ?? Date.now();
    if (task.op === 'pin' || task.op === 'unpin') {
      const pinned = task.op === 'pin';
      pins.set(key, { pinned, at });
      items.value = items.value.map(item => (packageKey(item.kind, item.token) === key ? { ...item, pinned } : item));
    } else if (task.op === 'install') {
      removals.delete(key);
      if (byKey.value.has(key)) return;
      // Stands in for the brew record until the reread brings the real one.
      const item: InstalledItem = {
        kind: 'cask',
        token,
        name: task.targetName ?? token,
        installedVersion: task.toVersion ?? '',
        actualVersion: null,
        latestVersion: null,
        status: 'up_to_date',
        onRequest: true,
        requiredBy: [],
        installedAt: at,
        sizeBytes: null,
        appPaths: [],
        iconPath: null,
        autoUpdates: false,
        pinned: false
      };
      installs.set(key, { item, at });
      items.value = [...items.value, item];
    } else if (task.op === 'uninstall') {
      installs.delete(key);
      removals.set(key, at);
      items.value = items.value.filter(item => packageKey(item.kind, item.token) !== key);
    }
  }

  async function load() {
    const current = ++listSequence;
    const result = onlyCasks(await unwrap(commands.libraryList()));
    if (current === listSequence) accept(result);
  }

  /** Rerun brew info --json=v2 --installed. */
  function refresh(): Promise<InstalledItem[]> {
    if (pendingRefresh) return pendingRefresh;
    const current = ++listSequence;
    refreshing.value = true;
    pendingRefresh = unwrap(commands.libraryRefresh())
      .then(result => {
        if (current === listSequence) accept(onlyCasks(result));
        return result;
      })
      .finally(() => {
        refreshing.value = false;
        pendingRefresh = undefined;
      });
    return pendingRefresh;
  }

  /** Native app commands need the real inventory, not a just-installed stand-in. */
  async function ensureInstalledRecord(kind: Kind, token: string) {
    if (!installs.has(packageKey(kind, token))) return;
    // An in-flight refresh may have read inventory before this install completed.
    // Inspect native results, never the optimistic list (or an expired stand-in).
    const contains = (list: InstalledItem[]) => list.some(item => item.kind === kind && item.token === token);
    if (contains(await refresh())) return;
    if (contains(await refresh())) return;
    throw new IpcError({
      code: 'E_NOT_FOUND',
      message: 'Installed package is not in the native inventory',
      detail: null
    });
  }

  async function loadStorage() {
    const current = ++storageSequence;
    const result = await unwrap(commands.libraryStorage());
    if (current === storageSequence) storage.value = result;
  }

  return {
    items,
    loaded,
    refreshing,
    storage,
    byKey,
    find,
    load,
    refresh,
    loadStorage,
    settle,
    ensureInstalledRecord
  };
});
