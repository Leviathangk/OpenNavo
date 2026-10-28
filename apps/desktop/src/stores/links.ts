import { ref } from 'vue';
import { defineStore } from 'pinia';
import { usePermissionGateStore } from './permissionGate';
import type { LinkOpenTarget } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import type { LocalItem } from '@/ipc/client';
import { useLocalAppsStore } from './localApps';

/**
 * Link apps (store/security plan §4): catalog entries offered only through their official site or GitHub. Their
 * download info and bundle IDs come from the local catalog; an app counts as installed when a local app carries one of
 * those bundle IDs, since nothing installs it through OpenNavo.
 */
export const useLinksStore = defineStore('links', () => {
  const items = ref<Record<string, LocalItem>>({});
  const requested = new Set<string>();
  const pending = new Map<string, Promise<LocalItem | null>>();
  let revision = 0;
  const localApps = useLocalAppsStore();

  function remember(list: readonly LocalItem[]) {
    const next = { ...items.value };
    let changed = false;
    for (const item of list) {
      if (item.kind !== 'link') continue;
      next[item.token] = item;
      changed = true;
    }
    if (changed) items.value = next;
  }

  function load(token: string): Promise<LocalItem | null> {
    requested.add(token);
    const cached = items.value[token];
    if (cached) return Promise.resolve(cached);
    const active = pending.get(token);
    if (active) return active;
    const started = revision;
    const request = unwrap(commands.catalogGet('link', token))
      .then(item => {
        if (started !== revision) return load(token);
        if (item) remember([item]);
        return item;
      })
      .finally(() => {
        if (pending.get(token) === request) pending.delete(token);
      });
    pending.set(token, request);
    return request;
  }

  /** Refresh every observed token once per catalog revision, including earlier misses and failures. */
  async function refresh() {
    const tokens = [...new Set([...requested, ...Object.keys(items.value)])];
    ++revision;
    pending.clear();
    items.value = {};
    // Mark attempts before reactive consumers rerender. Explicit load() can always retry a miss.
    tokens.forEach(token => requested.add(token));
    await Promise.allSettled(tokens.map(load));
  }

  /** Read-through for reactive callers: returns what is cached and starts a load once otherwise. */
  function get(token: string): LocalItem | undefined {
    const item = items.value[token];
    if (!item && !requested.has(token)) void load(token).catch(() => undefined);
    return item;
  }

  /** The local copy of a link app, matched by bundle ID. */
  function installed(token: string) {
    const ids = get(token)?.bundleIds ?? [];
    return ids.length ? localApps.items.find(app => app.bundleId && ids.includes(app.bundleId)) : undefined;
  }

  /** Where Download goes: the official download page first, then GitHub releases, then the homepage. */
  function target(item: LocalItem | null | undefined): LinkOpenTarget | null {
    const link = item?.link;
    if (!link) return null;
    if (link.downloadUrl) return 'download';
    if (link.github) return 'github';
    return link.homepage ? 'homepage' : null;
  }

  async function open(token: string, destination?: LinkOpenTarget) {
    if (
      destination !== 'homepage' &&
      !(await usePermissionGateStore().require({ feature: 'install', targets: [{ kind: 'link', token }] }))
    )
      return;
    const chosen = destination ?? target(await load(token));
    if (!chosen) throw new Error(`No download address for ${token}`);
    await unwrap(commands.linkOpen(token, chosen));
  }

  return { items, remember, load, get, installed, target, open, refresh };
});
