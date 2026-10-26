import { test } from 'node:test';
import { generateKeyPairSync } from 'node:crypto';
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const scanner = fileURLToPath(new URL('./check-secrets.mjs', import.meta.url));
const token = ['ghp', 'Ab3dE5gH7jK9mN2pQ4sT6vW8xY0zA1bC3dE5'].join('_');
function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'opennavo-scanner-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  execFileSync('git', ['init', '--quiet', root]);
  return {
    put(path, value) {
      mkdirSync(dirname(join(root, path)), { recursive: true });
      writeFileSync(join(root, path), value);
    },
    commit() {
      execFileSync(
        'git',
        [
          '-c',
          'user.name=Scanner Test',
          '-c',
          'user.email=scanner@example.invalid',
          '-c',
          'commit.gpgsign=false',
          'commit',
          '--quiet',
          '--no-verify',
          '-m',
          'Synthetic scanner fixture'
        ],
        { cwd: root }
      );
    },
    stage() {
      execFileSync('git', ['add', '.'], { cwd: root });
    },
    scan(mode) {
      return spawnSync(process.execPath, [scanner, mode], { cwd: root, encoding: 'utf8', timeout: 120000 });
    }
  };
}
function result(scan, expected) {
  assert.equal(scan.status, expected, scan.stderr);
  assert.ok(!scan.stderr.includes(token), 'findings must not print secret values');
}

test('generated-data exemptions require both exact path and exact line shape', t => {
  const f = fixture(t);
  const hash = 'abcdef0123456789'.repeat(4);
  f.put('tooling/i18n/i18n.lock.json', `{"sources": {\n  "apiKey": "${hash}"\n}}\n`);
  f.put(
    'apps/server/gen/publicapi/publicapi.gen.go',
    `package publicapi\nvar schema = []string{\n"${'aBcD0123'.repeat(10)}",\n}\n`
  );
  f.stage();
  result(f.scan('--staged'), 0);
  result(f.scan('--worktree'), 0);
  f.put('tooling/i18n/i18n.lock.json', `{"apiKey": "${token}"}\n`);
  result(f.scan('--worktree'), 1);
  result(f.scan('--staged'), 0); // Scan index bytes, not an unstaged edit.
  f.put('tooling/i18n/i18n.lock.json', '{}\n');
  f.put('nested/i18n.lock.json', `  "apiKey": "${hash}"\n`);
  result(f.scan('--worktree'), 1); // Never exempt similarly named nested files.
});

test('lock hashes stay exempt when a scan chunk boundary cuts their line', t => {
  const f = fixture(t);
  const hash = 'abcdef0123456789'.repeat(4);
  // Megabytes of hash lines guarantee findings that start mid-line at a chunk boundary.
  const lines = Array.from({ length: 30000 }, (_, i) => `    "page.system.seo.key${i}Token": "${hash}",`);
  f.put('tooling/i18n/i18n.lock.json', `{"sources": {\n${lines.join('\n')}\n    "last": "${hash}"\n}}\n`);
  result(f.scan('--worktree'), 0);
  f.put('tooling/i18n/i18n.lock.json', `{"sources": {\n${lines.join('\n')}\n    "apiKey": "${token}"\n}}\n`);
  result(f.scan('--worktree'), 1);
  // Hex that is not a whole hash, such as a 32-character key, fails the lock structure check.
  f.put('tooling/i18n/i18n.lock.json', `{"sources": {\n    "indexNowKey": "${'0123456789abcdef'.repeat(2)}"\n}}\n`);
  result(f.scan('--worktree'), 1);
});

test('About source revision exemptions reject tokens and unrelated paths or fields', t => {
  const f = fixture(t);
  const hash = 'abcdef0123456789'.repeat(2) + 'abcdef01';
  const path = 'apps/server/seeds/about.json';
  const seed = `{"name": "sourcegraph",\n  "version": "${hash}",\n  "url": "https://github.com/example/project/tree/${hash}"\n}\n`;
  f.put(path, seed);
  result(f.scan('--worktree'), 0);
  f.put('other.json', seed);
  result(f.scan('--worktree'), 1);
  f.put('other.json', '{}');
  f.put(path, seed.replace('"version"', '"token"'));
  result(f.scan('--worktree'), 1);
  f.put(path, seed.replace(hash, ['sgp', hash].join('_')));
  result(f.scan('--worktree'), 1);
});

test('representative tokens and private-key headers are rejected even with inline allow comments', t => {
  const f = fixture(t);
  const values = [
    `github_token=${token} # gitleaks:allow\n`,
    `aws_access_key_id=${'AK' + 'IA' + '7Q2W9E4R6T8Y3U5I'}\n`,
    generateKeyPairSync('rsa', {
      modulusLength: 1024,
      privateKeyEncoding: { type: 'pkcs1', format: 'pem' },
      publicKeyEncoding: { type: 'spki', format: 'pem' }
    }).privateKey
  ];
  for (const [index, value] of values.entries()) {
    f.put('credentials.txt', value);
    f.stage();
    const staged = f.scan('--staged');
    assert.equal(staged.status, 1, `synthetic credential case ${index}: ${staged.stderr}`);
    result(staged, 1);
    result(f.scan('--worktree'), 1);
  }
});

test('forbidden environment files fail even without a recognizable credential', t => {
  const f = fixture(t);
  f.put('apps/server/.env.local', 'LLM_ENABLED=false\n');
  f.stage();
  result(f.scan('--staged'), 1);
  result(f.scan('--worktree'), 1);
});

test('history detects a committed secret after removal from the current tree', t => {
  const f = fixture(t);
  f.put('fixture.txt', `token=${token}\n`);
  f.stage();
  f.commit();
  f.put('fixture.txt', 'removed\n');
  f.stage();
  f.commit();
  result(f.scan('--staged'), 0);
  result(f.scan('--history'), 1);
});
