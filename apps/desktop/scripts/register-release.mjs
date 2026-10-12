// Register draft desktop releases (03 §13.3): merge target manifests written by release-artifacts.mjs,
// then POST to {ADMIN_API_BASE}/desktop-releases with the CI token. latest.json is generated only after an administrator publishes the release.
// Environment: ADMIN_API_BASE (e.g. https://admin.opennavo.example/admin-api), CI_RELEASE_TOKEN.
import { readFileSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { loadReleaseNotes } from './release-notes.mjs';
import { TARGETS, releaseBody } from './release-lib.mjs';

const { values } = parseArgs({
  options: {
    dir: { type: 'string', default: 'release-artifacts' },
    version: { type: 'string' },
    channel: { type: 'string', default: 'stable' },
    'notes-en': { type: 'string' },
    'notes-dir': { type: 'string' }
  }
});
if (values['notes-dir'] && values['notes-en'] !== undefined) {
  throw new Error('Use --notes-dir or inline --notes-en, not both');
}
const notes = values['notes-dir'] ? loadReleaseNotes(values['notes-dir'], values.version ?? '') : {};
const base = process.env.ADMIN_API_BASE;
const token = process.env.CI_RELEASE_TOKEN;
if (!base || !token) throw new Error('ADMIN_API_BASE and CI_RELEASE_TOKEN are required');

const dir = resolve(values.dir);
const artifacts = readdirSync(dir)
  .filter(name => name.endsWith('.json') && name.replace(/\.json$/, '') in TARGETS)
  .map(name => JSON.parse(readFileSync(join(dir, name), 'utf8')));
const body = releaseBody({
  version: values.version ?? '',
  channel: values.channel,
  artifacts,
  notes: values['notes-dir'] ? notes : undefined,
  notesEn: values['notes-en']
});

const response = await fetch(`${base.replace(/\/+$/, '')}/desktop-releases`, {
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    Authorization: `Bearer ${token}`
  },
  body: JSON.stringify(body),
  redirect: 'error',
  signal: AbortSignal.timeout(30_000)
});
// Admin API business errors use HTTP 200 plus a business code (04 §3).
const envelope = await response.json().catch(() => null);
if (!response.ok || envelope?.code !== '0000') {
  throw new Error(`register failed: HTTP ${response.status} ${envelope?.code ?? ''}`);
}
console.log(`draft desktop release ${body.version} (${body.channel}) registered as #${envelope.data?.id}`);
