import { mkdir, rm, utimes, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { NUXT_ASSET_CACHE_CONTROL, resolveNuxtAsset } from '../server/utils/nuxtAsset';

const roots: string[] = [];
afterEach(async () => {
  await Promise.all(roots.splice(0).map(root => rm(root, { recursive: true, force: true })));
});

it('resolves an old hashed file and rejects missing or unsafe names', async () => {
  const root = join(tmpdir(), `nuxt-assets-${Date.now()}-${Math.random()}`);
  roots.push(root);
  await mkdir(root, { recursive: true });
  await writeFile(join(root, 'BFUXrmnu.js'), 'export default 1;');
  await utimes(join(root, 'BFUXrmnu.js'), new Date(1), new Date(1));
  expect(await resolveNuxtAsset(root, '/_nuxt/BFUXrmnu.js')).toBe(join(root, 'BFUXrmnu.js'));
  expect(await resolveNuxtAsset(root, '/_nuxt/missing.js')).toBeNull();
  expect(await resolveNuxtAsset(root, '/_nuxt/../server/index.mjs')).toBeNull();
  expect(await resolveNuxtAsset(root, '/_nuxt/sub/file.js')).toBeNull();
  expect(NUXT_ASSET_CACHE_CONTROL).toBe('public, max-age=31536000, immutable');
});
