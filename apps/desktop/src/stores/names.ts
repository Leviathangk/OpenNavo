import { ref } from 'vue';
import { defineStore } from 'pinia';
import type { Kind, LocalizedText, Locale } from '@/ipc/bindings';
import { commands, packageKey, unwrap } from '@/ipc/client';
import type { LocalItem } from '@/ipc/client';

export interface PackageIdentity {
  sourceLocale: Locale;
  name: string;
  displayName: LocalizedText;
  iconUrl: string | null;
  accentColor: string | null;
  /** Third-party tap of the cask, for brew commands shown before an install. */
  tap?: string | null;
  brewToken?: string | null;
}

/**
 * Package display cache: tasks, history, and installed records contain only kind/token; names and icons come from the local catalog.
 * Cache read entries through remember; fetch unseen entries with catalog_get on demand. Callers fall back to the token until loaded.
 */
export const useNamesStore = defineStore('names', () => {
  const entries = ref<Record<string, PackageIdentity>>({});
  const requested = new Set<string>();
  let generation = 0;

  async function load(kind: Kind, token: string) {
    const current = generation;
    const key = packageKey(kind, token);
    requested.add(key);
    try {
      const item = await unwrap(commands.catalogGet(kind, token));
      if (current !== generation) return;
      if (item) remember([item]);
      else delete entries.value[key];
    } catch {
      // Retain cached names on offline read failure; later sync can retry.
    }
  }

  async function refresh() {
    const keys = new Set([...Object.keys(entries.value), ...requested]);
    generation += 1;
    requested.clear();
    await Promise.all(
      [...keys].map(key => {
        const [kind, token] = key.split('/');
        return load(kind as Kind, token ?? '');
      })
    );
  }

  function remember(items: readonly LocalItem[]) {
    for (const item of items) {
      entries.value[packageKey(item.kind, item.token)] = {
        name: item.name,
        sourceLocale: item.sourceLocale,
        displayName: item.displayName,
        iconUrl: item.iconUrl,
        accentColor: item.accentColor,
        tap: item.tap ?? null,
        brewToken: item.brewToken ?? null
      };
    }
  }

  function lookup(kind: Kind, token: string): PackageIdentity | undefined {
    if (token.includes('/')) return undefined;
    const key = packageKey(kind, token);
    const entry = entries.value[key];
    if (!entry && !requested.has(key)) {
      void load(kind, token);
    }
    return entry;
  }

  return { entries, remember, lookup, refresh };
});
