import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import { extname } from 'node:path';
import { createError, defineEventHandler, send, sendStream, setHeader, type H3Event } from 'h3';
import { NUXT_ASSET_CACHE_CONTROL, resolveNuxtAsset } from '../../utils/nuxtAsset';

const CONTENT_TYPES: Record<string, string> = {
  '.avif': 'image/avif',
  '.css': 'text/css; charset=utf-8',
  '.eot': 'application/vnd.ms-fontobject',
  '.gif': 'image/gif',
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.jpeg': 'image/jpeg',
  '.json': 'application/json; charset=utf-8',
  '.jpg': 'image/jpeg',
  '.map': 'application/json; charset=utf-8',
  '.mjs': 'text/javascript; charset=utf-8',
  '.otf': 'font/otf',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.ttf': 'font/ttf',
  '.wasm': 'application/wasm',
  '.webp': 'image/webp',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2'
};

export default defineEventHandler(async (event: H3Event) => {
  if (event.method !== 'GET' && event.method !== 'HEAD') {
    throw createError({ statusCode: 405, statusMessage: 'Method not allowed' });
  }
  const asset = await resolveNuxtAsset(`${process.cwd()}/.output/public/_nuxt`, event.path);
  if (!asset) {
    setHeader(event, 'Cache-Control', 'no-store');
    throw createError({ statusCode: 404, statusMessage: 'Asset not found' });
  }
  const details = await stat(asset).catch(() => null);
  if (!details?.isFile()) {
    setHeader(event, 'Cache-Control', 'no-store');
    throw createError({ statusCode: 404, statusMessage: 'Asset not found' });
  }
  setHeader(event, 'Cache-Control', NUXT_ASSET_CACHE_CONTROL);
  setHeader(event, 'Content-Type', CONTENT_TYPES[extname(asset).toLowerCase()] ?? 'application/octet-stream');
  setHeader(event, 'Content-Length', details.size);
  if (event.method === 'HEAD') return send(event, null);
  return sendStream(event, createReadStream(asset));
});
