#!/usr/bin/env node
/** Package the prepared runtime for the optional in-app installer/CDN. */
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const desktop = resolve(here, '..');
const source = join(desktop, 'src-tauri', 'resources', 'hermes');
const outputDir = join(desktop, 'hermes-dist');
const manifestPath = join(source, 'manifest.json');
const manifest = existsSync(manifestPath) ? JSON.parse(readFileSync(manifestPath, 'utf8')) : {};
const version = manifest.hermesAgent ?? '0.23.0';
const output = join(outputDir, `runtime-${version}-macos-arm64.tar.gz`);

if (!existsSync(join(source, 'bin', 'hermes'))) {
  console.error('Runtime is not prepared. Run hermes:prepare first.');
  process.exit(1);
}
mkdirSync(outputDir, { recursive: true });
execFileSync('tar', ['-czf', output, '-C', source, '.'], { stdio: 'inherit' });
const digest = createHash('sha256').update(readFileSync(output)).digest('hex');
writeFileSync(`${output}.sha256`, `${digest}  ${output.split('/').pop()}\n`);
console.log(output);
console.log(`sha256 ${digest}`);
