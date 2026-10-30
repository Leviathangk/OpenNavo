import { ApiError } from '@opennavo/api';
import type { InstalledItem } from '@/ipc/bindings';
import type { LocalItem } from '@/ipc/client';

/** A catalog entry may stop being public while its installed copy still works. Other failures remain retryable. */
export async function optionalPublicPackage<T>(pending: Promise<T>): Promise<T | undefined> {
  try {
    return await pending;
  } catch (error) {
    if (error instanceof ApiError && (error.isNotFound || error.status === 404)) return undefined;
    throw error;
  }
}

/** Inventory is sufficient for the local header and actions even after catalog sync drops a private entry. */
export function installedPackage(item: InstalledItem): LocalItem {
  return {
    kind: item.kind,
    token: item.token,
    name: item.name,
    names: [item.name],
    sourceLocale: 'en-US',
    displayName: {},
    summary: {},
    version: item.actualVersion ?? item.installedVersion,
    homepage: null,
    autoUpdates: item.autoUpdates,
    deprecated: false,
    disabled: false,
    hidden: true,
    isFont: false,
    isLibrary: false,
    categories: [],
    tags: [],
    installs30d: 0,
    rank30d: null,
    popularity: null,
    iconUrl: null,
    accentColor: null,
    apps: item.appPaths,
    binaries: [],
    pinyin: null,
    versionChangedAt: new Date(item.installedAt ?? 0).toISOString(),
    downloadSize: null,
    onRequest: null
  };
}
