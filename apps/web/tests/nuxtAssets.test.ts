import { mkdir, rm, utimes, writeFile } from 'node:fs/promises';
import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createEvent, getResponseHeader, getResponseStatus } from 'h3';
import { afterEach, expect, it, vi } from 'vitest';
import assetRoute from '../server/routes/_nuxt/[...asset]';
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
  expect(await resolveNuxtAsset(root, '/_nuxt/BFUXrmnu.js?v=1#cache-buster')).toBe(join(root, 'BFUXrmnu.js'));
  expect(await resolveNuxtAsset(root, '/_nuxt/missing.js')).toBeNull();
  expect(await resolveNuxtAsset(root, '/_nuxt/../server/index.mjs')).toBeNull();
  expect(await resolveNuxtAsset(root, '/_nuxt/sub/file.js')).toBeNull();
  expect(NUXT_ASSET_CACHE_CONTROL).toBe('public, max-age=31536000, immutable');
});

it('serves old assets for GET and HEAD with matching immutable headers and MIME types', async () => {
  const root = join(tmpdir(), `nuxt-assets-${Date.now()}-${Math.random()}`);
  const assetRoot = join(root, '.output/public/_nuxt');
  roots.push(root);
  await mkdir(assetRoot, { recursive: true });
  const asset = join(assetRoot, 'legacy.png');
  await writeFile(asset, 'png-fixture');
  const cwd = vi.spyOn(process, 'cwd').mockReturnValue(root);
  const request = (method: string) => {
    const socket = new Socket();
    const req = new IncomingMessage(socket);
    req.method = method;
    req.url = '/_nuxt/legacy.png?v=1';
    const res = new ServerResponse(req);
    return { event: createEvent(req, res), socket };
  };
  try {
    const get = request('GET');
    const head = request('HEAD');
    try {
      await assetRoute(get.event);
      await assetRoute(head.event);
      expect(getResponseStatus(get.event)).toBe(200);
      expect(getResponseStatus(head.event)).toBe(200);
      for (const header of ['cache-control', 'content-type', 'content-length']) {
        expect(getResponseHeader(head.event, header)).toBe(getResponseHeader(get.event, header));
      }
      expect(getResponseHeader(head.event, 'cache-control')).toBe(NUXT_ASSET_CACHE_CONTROL);
      expect(getResponseHeader(head.event, 'content-type')).toBe('image/png');
      expect(head.event.node.res.writableEnded).toBe(true);
    } finally {
      get.socket.destroy();
      head.socket.destroy();
    }
  } finally {
    cwd.mockRestore();
  }
});

it('rejects missing old assets', async () => {
  const root = join(tmpdir(), `nuxt-assets-${Date.now()}-${Math.random()}`);
  const assetRoot = join(root, '.output/public/_nuxt');
  roots.push(root);
  await mkdir(assetRoot, { recursive: true });
  const cwd = vi.spyOn(process, 'cwd').mockReturnValue(root);
  const socket = new Socket();
  const req = new IncomingMessage(socket);
  req.method = 'GET';
  req.url = '/_nuxt/missing.js';
  try {
    const event = createEvent(req, new ServerResponse(req));
    await expect(assetRoute(event)).rejects.toMatchObject({ statusCode: 404 });
  } finally {
    socket.destroy();
    cwd.mockRestore();
  }
});

it.each([
  ['jpg', 'image/jpeg'],
  ['jpeg', 'image/jpeg'],
  ['avif', 'image/avif'],
  ['webp', 'image/webp'],
  ['gif', 'image/gif'],
  ['ttf', 'font/ttf'],
  ['otf', 'font/otf'],
  ['mjs', 'text/javascript; charset=utf-8'],
  ['map', 'application/json; charset=utf-8']
])('sets the standard MIME type for .%s assets', async (extension, mime) => {
  const root = join(tmpdir(), `nuxt-assets-${Date.now()}-${Math.random()}`);
  const assetRoot = join(root, '.output/public/_nuxt');
  roots.push(root);
  await mkdir(assetRoot, { recursive: true });
  await writeFile(join(assetRoot, `legacy.${extension}`), 'fixture');
  const cwd = vi.spyOn(process, 'cwd').mockReturnValue(root);
  const socket = new Socket();
  const req = new IncomingMessage(socket);
  req.method = 'HEAD';
  req.url = `/_nuxt/legacy.${extension}`;
  try {
    const event = createEvent(req, new ServerResponse(req));
    await assetRoute(event);
    expect(getResponseStatus(event)).toBe(200);
    expect(getResponseHeader(event, 'content-type')).toBe(mime);
    expect(event.node.res.writableEnded).toBe(true);
  } finally {
    socket.destroy();
    cwd.mockRestore();
  }
});
