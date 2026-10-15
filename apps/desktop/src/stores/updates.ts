import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import type { Kind, OutdatedItem, Task, TaskOp, TaskTarget, UpdatesStatus } from '@/ipc/bindings';
import { commands, packageKey, unwrap } from '@/ipc/client';

// Cask-only catalog (ADR-018): neither show nor count command-line tools from brew outdated.
const onlyCasks = (list: OutdatedItem[]) => list.filter(item => item.kind === 'cask');
// Operations after which a target no longer has the update brew outdated listed before them.
const SETTLING: readonly TaskOp[] = ['upgrade', 'reinstall', 'uninstall'];

/**
 * Available updates (06 §6.8): updates_list reads cache; updates_check reruns brew outdated; reread after updates:changed.
 * Last/next check times come from updates_status (Rust schedules checks; the UI does not calculate them).
 */
export const useUpdatesStore = defineStore('updates', () => {
  const items = ref<OutdatedItem[]>([]);
  const loaded = ref(false);
  // Targets whose upgrade, reinstall or uninstall just succeeded, with the task's finish time. Rust drops them
  // from its list right after the task, but that event trails the task's own, so a list read can still carry
  // them for a moment. They stay out until a read no longer lists them or a brew outdated check ran after.
  const settled = new Map<string, number>();
  // Pins and unpins that just succeeded: the pinned flag to show until a read or a later check agrees.
  const pins = new Map<string, { pinned: boolean; at: number }>();
  const status = ref<UpdatesStatus | null>(null);
  const requested = ref(false);
  const checking = computed(() => requested.value || Boolean(status.value?.checking));
  const checkedAt = computed(() => status.value?.checkedAt ?? null);
  const nextCheckAt = computed(() => status.value?.nextCheckAt ?? null);

  const byKey = computed(() => new Map(items.value.map(item => [packageKey(item.kind, item.token), item])));
  /** Items eligible for immediate updates, excluding pinned versions. */
  const actionable = computed(() => items.value.filter(item => !item.pinned && !item.ignored));
  const find = (kind: Kind, token: string) => byKey.value.get(packageKey(kind, token));

  async function loadStatus() {
    status.value = await unwrap(commands.updatesStatus());
  }

  function settle(task: Task) {
    if (task.state !== 'succeeded' || !task.target) return;
    const key = packageKey(task.target.kind, task.target.token);
    const at = task.finishedAt ?? Date.now();
    if (task.op === 'pin' || task.op === 'unpin') {
      const pinned = task.op === 'pin';
      pins.set(key, { pinned, at });
      items.value = items.value.map(item => (packageKey(item.kind, item.token) === key ? { ...item, pinned } : item));
      return;
    }
    if (!SETTLING.includes(task.op)) return;
    pins.delete(key);
    settled.set(key, at);
    items.value = items.value.filter(item => packageKey(item.kind, item.token) !== key);
  }

  /** checkTime: when the brew outdated run behind the list happened; it accounts for tasks finished before. */
  function accept(list: OutdatedItem[], checkTime: number | null) {
    const listed = new Map(list.map(item => [packageKey(item.kind, item.token), item]));
    const checkedSince = (at: number) => checkTime !== null && checkTime >= at;
    for (const [key, finishedAt] of settled) {
      if (!listed.has(key) || checkedSince(finishedAt)) settled.delete(key);
    }
    for (const [key, pin] of pins) {
      const item = listed.get(key);
      if (!item || item.pinned === pin.pinned || checkedSince(pin.at)) pins.delete(key);
    }
    items.value = onlyCasks(list)
      .filter(item => !settled.has(packageKey(item.kind, item.token)))
      .map(item => {
        const pin = pins.get(packageKey(item.kind, item.token));
        return pin ? { ...item, pinned: pin.pinned } : item;
      });
    loaded.value = true;
  }

  async function load() {
    // Status first: a list read after it is at least as new as the check the status reports.
    await loadStatus().catch(() => undefined);
    accept(await unwrap(commands.updatesList()), status.value?.checkedAt ?? null);
  }

  async function check(runBrewUpdate: boolean) {
    const startedAt = Date.now();
    requested.value = true;
    try {
      accept(await unwrap(commands.updatesCheck(runBrewUpdate)), startedAt);
    } finally {
      requested.value = false;
      await loadStatus().catch(() => undefined);
    }
  }

  async function ignore(target: TaskTarget, version: string | null) {
    await unwrap(commands.updatesIgnore(target, version, null));
    await load();
  }

  return {
    items,
    loaded,
    status,
    checking,
    checkedAt,
    nextCheckAt,
    byKey,
    actionable,
    find,
    load,
    loadStatus,
    check,
    ignore,
    settle
  };
});
