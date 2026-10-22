// Production build identity, computed from the Docker context rather than Git history or mtimes.
// Keep this input list in sync with Dockerfile's workspace dependencies and build-time imports.
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const directories = ['apps/web', 'packages/api', 'packages/shared', 'packages/tokens', 'packages/ui', 'patches'];
const files = [
  'package.json',
  'pnpm-lock.yaml',
  'pnpm-workspace.yaml',
  '.dockerignore',
  'tooling/config/tsconfig.base.json'
];
const excludedDirectories = new Set([
  'node_modules',
  'node-compile-cache',
  '.nuxt',
  '.output',
  '.nitro',
  '.cache',
  '.git',
  'coverage',
  'playwright-report',
  'test-results',
  'tests',
  '__tests__',
  '__snapshots__',
  'e2e'
]);

export function fingerprintInputs(root: string): string[] {
  const inputs = [...files];
  const walk = (directory: string) => {
    for (const name of readdirSync(resolve(root, directory))) {
      const path = `${directory}/${name}`;
      const stat = lstatSync(resolve(root, path));
      if (excludedDirectories.has(name) || name === '.DS_Store' || name.endsWith('.log')) continue;
      // Match the Docker context's generated-output exclusions; token dist is committed build input.
      if (name === 'dist' && path !== 'packages/tokens/dist') continue;
      if (name === '.env' || name.startsWith('.env.')) continue;
      if (/\.(?:test|spec)\.[cm]?[jt]sx?$/.test(name)) continue;
      if (stat.isDirectory()) walk(path);
      else if (stat.isFile()) inputs.push(path);
      else throw new Error(`Unsupported build input: ${path}`);
    }
  };
  directories.forEach(walk);
  return inputs.sort();
}

export function buildFingerprint(root: string): string {
  const hash = createHash('sha256');
  const add = (value: string | Buffer) => {
    const bytes = Buffer.isBuffer(value) ? value : Buffer.from(value);
    hash.update(`${bytes.length}:`).update(bytes);
  };
  add('opennavo-web-build-v1');
  // The Dockerfile pins the base image; also separate local builds by the actual Node toolchain.
  add(JSON.stringify({ versions: process.versions, platform: process.platform, arch: process.arch }));
  for (const path of fingerprintInputs(root)) {
    add(path);
    add(readFileSync(resolve(root, path)));
  }
  return hash.digest('hex');
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.stdout.write(`${buildFingerprint(fileURLToPath(new URL('../../../', import.meta.url)))}\n`);
}
