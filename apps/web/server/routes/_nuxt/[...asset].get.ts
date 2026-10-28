import { createReadStream } from 'node:fs';
import { extname } from 'node:path';
import { createError, defineEventHandler, sendStream, setHeader, type H3Event } from 'h3';
import { NUXT_ASSET_CACHE_CONTROL, resolveNuxtAsset } from '../../utils/nuxtAsset';

const CONTENT_TYPES: Record<string, string> = {
  '.css': 'text/css; charset=utf-8',
  '.gif': 'image/gif',
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.json': 'application/json; charset=utf-8',
  '.map': 'application/json; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.wasm': 'application/wasm',
  '.webp': 'image/webp',
  '.woff': 'font/woff',
  '.woff2': 'font/woff2'
};

export default defineEventHandler(async (event: H3Event) => {
  const asset = await resolveNuxtAsset(`${process.cwd()}/.output/public/_nuxt`, event.path);
  if (!asset) {
    setHeader(event, 'Cache-Control', 'no-store');
    throw createError({ statusCode: 404, statusMessage: 'Asset not found' });
  }
  setHeader(event, 'Cache-Control', NUXT_ASSET_CACHE_CONTROL);
  setHeader(event, 'Content-Type', CONTENT_TYPES[extname(asset).toLowerCase()] ?? 'application/octet-stream');
  return sendStream(event, createReadStream(asset));
});
