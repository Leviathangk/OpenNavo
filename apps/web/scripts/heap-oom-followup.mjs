#!/usr/bin/env node
import { once } from 'node:events';
import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import { mkdtemp, writeFile, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawn } from 'node:child_process';
import { execFileSync } from 'node:child_process';

const entry = process.argv[2];
if (!entry)
  throw new Error('usage: node --expose-gc apps/web/scripts/heap-oom-followup.mjs /path/to/.output/server/index.mjs');
const total = Number(process.env.OOM_REQUESTS ?? 20000);
const batchSize = 2000;
const concurrency = 20;
const locales = ['en', 'zh', 'ja', 'ko', 'de', 'fr', 'es', 'pt', 'ru'];
const redisUrl = process.env.OOM_REDIS_URL ?? '';
const imageRedisUrl = process.env.OOM_IMAGE_REDIS_URL ?? redisUrl;
const pauseRedisPort = process.env.OOM_PAUSE_REDIS_PORT ?? '';

const api = createServer((_request, response) => {
  response.writeHead(200, { 'content-type': 'application/json' });
  response.end(JSON.stringify({ code: '0000', msg: 'fixture', data: {} }));
});
api.listen(0, '127.0.0.1');
await once(api, 'listening');
const apiBase = `http://127.0.0.1:${api.address().port}/api/v1`;

const temp = await mkdtemp(join(tmpdir(), 'opennavo-oom-'));
const preload = join(temp, 'signals.cjs');
const snapshotDir = join(temp, 'snapshots');
const memoryFile = join(temp, 'memory.jsonl');
await writeFile(
  preload,
  `const fs = require('node:fs'); const path = require('node:path'); const v8 = require('node:v8');\nprocess.on('SIGUSR2', () => { if (global.gc) global.gc(); fs.appendFileSync(${JSON.stringify(memoryFile)}, JSON.stringify(process.memoryUsage()) + '\\n'); });\nprocess.on('SIGUSR1', () => { v8.writeHeapSnapshot(path.join(${JSON.stringify(snapshotDir)}, 'heap-' + Date.now() + '.heapsnapshot')); });\n`
);
await import('node:fs/promises').then(fs => fs.mkdir(snapshotDir));

function reservePort() {
  return new Promise((resolve, reject) => {
    const server = createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
  });
}

const port = await reservePort();
const metricsPort = await reservePort();
const secret = randomUUID();
const env = {
  ...process.env,
  NODE_ENV: 'production',
  HOST: '127.0.0.1',
  NITRO_HOST: '127.0.0.1',
  PORT: String(port),
  NITRO_PORT: String(port),
  NUXT_API_BASE_INTERNAL: apiBase,
  NUXT_PUBLIC_API_BASE: apiBase,
  NUXT_REDIS_URL: redisUrl,
  NUXT_IMAGE_REDIS_URL: imageRedisUrl,
  NUXT_PUBLIC_SITE_URL: 'https://example.test',
  NUXT_PUBLIC_I18N_BASE_URL: 'https://example.test',
  NUXT_METRICS_PORT: String(metricsPort),
  NUXT_INTERNAL_SECRET: secret,
  NUXT_SITEMAP_FONTS: 'false',
  OPENNAVO_SITEMAP_WARMUP: 'false'
};
const child = spawn(process.execPath, ['--expose-gc', '--require', preload, entry], {
  env,
  stdio: ['ignore', 'pipe', 'pipe']
});
let logs = '';
child.stdout.on('data', chunk => {
  logs += chunk;
});
child.stderr.on('data', chunk => {
  logs += chunk;
});
const origin = await new Promise((resolve, reject) => {
  const timeout = setTimeout(() => reject(new Error(`server did not start\n${logs}`)), 30000);
  child.once('error', reject);
  child.once('exit', code => reject(new Error(`server exited ${code}\n${logs}`)));
  const check = () => {
    const match = /Listening on (http:\/\/127\.0\.0\.1:\d+)/.exec(logs);
    if (match) {
      clearTimeout(timeout);
      resolve(match[1]);
    }
  };
  child.stdout.on('data', check);
});

function urlFor(i) {
  const locale = locales[i % locales.length];
  const slug = `oom-app-${i}`;
  switch (i % 10) {
    case 0:
      return `/${locale}/apps/${slug}`;
    case 1:
      return `/${locale}/apps/${slug}/versions?page=${(i % 20) + 1}&source=${i}`;
    case 2:
      return `/${locale}/apps/${slug}/details?tab=${i % 7}`;
    case 3:
      return `/${locale}/apps/${slug}/feed.xml?cursor=${i}`;
    case 4:
      return `/${locale}/background-items/item-${i}`;
    case 5:
      return `/${locale}/categories/category-${i}`;
    case 6:
      return `/${locale}/collections/collection-${i}`;
    case 7:
      return `/${locale}/articles/article-${i}`;
    case 8:
      return `/${locale}/apps/missing-${i}`;
    default:
      return `/${locale}/apps/${slug}/versions/_payload.json?page=${(i % 20) + 1}&v=${i}`;
  }
}

async function request(path) {
  const response = await fetch(`${origin}${path}`, {
    headers: { accept: 'text/html', 'user-agent': 'OOM-Followup/1.0' }
  });
  await response.arrayBuffer();
}

async function metrics() {
  const text = await (await fetch(`http://127.0.0.1:${metricsPort}/metrics`)).text();
  const value = name => Number(new RegExp(`^opennavo_web_${name} ([0-9]+)$`, 'm').exec(text)?.[1] ?? 0);
  const memory = (await readFile(memoryFile, 'utf8')).trim().split('\n').filter(Boolean).at(-1);
  const usage = memory ? JSON.parse(memory) : {};
  return {
    heapUsed: value('heap_used_bytes') || usage.heapUsed || 0,
    heapLimit: value('heap_limit_bytes') || usage.heapTotal || 0,
    rss: value('rss_bytes') || usage.rss || 0,
    external: usage.external || 0,
    arrayBuffers: usage.arrayBuffers || 0
  };
}

const samples = [];
process.kill(child.pid, 'SIGUSR2');
await new Promise(resolve => setTimeout(resolve, 250));
process.kill(child.pid, 'SIGUSR1');
for (let start = 0; start < total; start += batchSize) {
  const paths = Array.from({ length: Math.min(batchSize, total - start) }, (_, offset) => urlFor(start + offset));
  for (let i = 0; i < paths.length; i += concurrency) await Promise.all(paths.slice(i, i + concurrency).map(request));
  if (pauseRedisPort && start < 10000 && start + batchSize >= 10000) {
    execFileSync('redis-cli', ['-p', pauseRedisPort, 'CLIENT', 'PAUSE', '2000'], { stdio: 'ignore' });
  }
  process.kill(child.pid, 'SIGUSR2');
  await new Promise(resolve => setTimeout(resolve, 250));
  const sample = { requests: Math.min(start + batchSize, total), ...(await metrics()) };
  samples.push(sample);
  console.log(JSON.stringify(sample));
}
process.kill(child.pid, 'SIGUSR1');
await new Promise(resolve => setTimeout(resolve, 500));
console.log(JSON.stringify({ entry, total, samples, snapshots: snapshotDir }));
child.kill('SIGTERM');
await once(child, 'exit');
api.close();
const snapshots = (await import('node:fs/promises')).readdir(snapshotDir);
await writeFile(join(temp, 'result.json'), JSON.stringify({ entry, total, samples, snapshots: await snapshots }));
console.error(`RESULT_FILE=${join(temp, 'result.json')}`);
console.error(`SNAPSHOT_DIR=${snapshotDir}`);
