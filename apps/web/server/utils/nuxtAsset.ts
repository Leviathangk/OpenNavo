import { realpath, stat } from 'node:fs/promises';
import { join } from 'node:path';

const ASSET_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;
export const NUXT_ASSET_CACHE_CONTROL = 'public, max-age=31536000, immutable';

/** Resolve one ordinary file directly under the runtime _nuxt directory. */
export async function resolveNuxtAsset(root: string, path: string) {
  const match = /^\/_nuxt\/([^/]+)$/.exec(path);
  const name = match?.[1];
  if (!name || !ASSET_NAME.test(name)) return null;
  const candidate = join(root, name);
  try {
    const [realRoot, realCandidate, details] = await Promise.all([realpath(root), realpath(candidate), stat(candidate)]);
    return details.isFile() && (realCandidate === realRoot || realCandidate.startsWith(`${realRoot}/`)) ? candidate : null;
  } catch {
    return null;
  }
}
