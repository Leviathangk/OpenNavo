import assert from 'node:assert/strict';
import test from 'node:test';
import { deployed, flaky, lastDeployed, passed, ships, superseded } from './ship.mjs';

test('only known infrastructure or timing failures rerun', () => {
  assert.equal(flaky(['ERROR: 429 Too Many Requests - Server message: toomanyrequests: Data limit exceeded']), true);
  assert.equal(flaky(['--- FAIL: sixteen_thousand_import_and_short_queries']), true);
  assert.equal(flaky(['429 Too Many Requests', 'assertion failed: expected 2, got 3']), false);
  assert.equal(flaky([]), false);
});

test('a commit deploys once, unless its earlier deploy failed', () => {
  const sha = 'd1cb7edb9d954798ca63cb765077284073294f52';
  const run = (status, conclusion) => ({ display_title: `Deploy production ${sha}`, status, conclusion });
  assert.equal(deployed([], sha), false);
  assert.equal(deployed([run('in_progress', null)], sha), true);
  assert.equal(deployed([run('completed', 'success')], sha), true);
  assert.equal(deployed([run('completed', 'failure')], sha), false);
  assert.equal(
    deployed([{ display_title: 'Deploy production main', status: 'completed', conclusion: 'success' }], sha),
    false
  );
});

test('only a completed, successful run counts', () => {
  assert.equal(passed({ status: 'completed', conclusion: 'success' }), true);
  assert.equal(passed({ status: 'completed', conclusion: 'failure' }), false);
  assert.equal(passed({ status: 'in_progress', conclusion: null }), false);
  assert.equal(passed(undefined), false);
});

test('only changes that reach production deploy', () => {
  assert.equal(ships(['apps/server/internal/http/router.go']), true);
  assert.equal(ships(['packages/ui/src/Button.vue']), true);
  assert.equal(ships(['ops/docker-compose.prod.yml']), true);
  assert.equal(
    ships(['docs/guide.md', '.github/workflows/ci-frontend.yml', 'apps/desktop/src-tauri/src/main.rs']),
    false
  );
  assert.equal(ships(null), true, 'an unlisted comparison deploys');
  for (const file of [
    'ops/e2e.py',
    'ops/test_e2e.py',
    'ops/docker-compose.e2e.yml',
    'ops/docker-compose.e2e.ci.yml',
    'ops/docker-compose.dev.yml',
    'ops/Dockerfile.minio'
  ]) {
    assert.equal(ships([file]), false, `${file} only serves the dev or E2E stack`);
  }
  assert.equal(ships(['ops/github-deploy.py', 'ops/GUIDE.md']), false, 'operators install the receiver by hand');
  assert.equal(ships(['ops/docker-compose.prod.yml']), true);
  assert.equal(ships(['ops/caddy/Caddyfile']), true);
  assert.equal(ships(['apps/server/internal/http/server_test.go']), false, 'Go tests never reach a binary');
  assert.equal(ships(['apps/server/internal/http/server_test.go', 'apps/server/internal/http/router.go']), true);
  assert.equal(ships(['apps/server/internal/testutil/contract.go']), true, 'helpers are ordinary packages');
});

test('the live revision comes from the newest successful deploy', () => {
  const sha = 'd1cb7edb9d954798ca63cb765077284073294f52';
  const runs = [
    { run_number: 3, status: 'completed', conclusion: 'failure', display_title: 'Deploy production ' + 'f'.repeat(40), head_sha: 'x' },
    { run_number: 2, status: 'completed', conclusion: 'success', display_title: `Deploy production ${sha}`, head_sha: 'y' },
    { run_number: 1, status: 'completed', conclusion: 'success', display_title: 'Deploy production main', head_sha: 'z' }
  ];
  assert.equal(lastDeployed(runs), sha);
  assert.equal(lastDeployed(runs.slice(2)), 'z', 'a deploy of "main" ran on its head commit');
  assert.equal(lastDeployed([]), null);
  const running = 'a'.repeat(40);
  assert.equal(
    lastDeployed([
      { run_number: 4, status: 'in_progress', conclusion: null, display_title: `Deploy production ${running}`, head_sha: 'w' },
      ...runs
    ]),
    running,
    'a deploy still in flight is what production is getting'
  );
});

test('only older entries of a known cache kind are superseded', () => {
  const sha = n => String(n).repeat(40).slice(0, 40);
  const hash = n => String(n).repeat(64).slice(0, 64);
  const cache = (id, key, created_at, ref = 'refs/heads/main') => ({ id, key, created_at, ref });
  const caches = [
    cache(1, `gobuild-Linux-unit-${hash(1)}-${sha(1)}`, '2026-10-21T10:00:00Z'),
    cache(2, `gobuild-Linux-unit-${hash(1)}-${sha(2)}`, '2026-10-21T12:00:00Z'),
    cache(3, `gobuild-Linux-e2e-${hash(1)}-${sha(1)}`, '2026-10-21T10:00:00Z'),
    cache(4, `gomod-Linux-${hash(1)}`, '2026-10-20T10:00:00Z'),
    cache(5, `gomod-Linux-${hash(2)}`, '2026-10-21T10:00:00Z'),
    cache(6, `e2e-images-Linux-2026-09-${hash(1)}`, '2026-09-30T10:00:00Z'),
    cache(7, `e2e-images-Linux-2026-10-${hash(1)}`, '2026-10-01T10:00:00Z'),
    cache(8, 'v0-rust-desktop-bundle-Darwin-arm64-8c8d7eed-adf85098', '2026-10-01T10:00:00Z'),
    cache(9, 'buildkit-blob-1-sha256:abc', '2026-10-01T10:00:00Z'),
    cache(10, `gobuild-Linux-unit-${hash(1)}-${sha(3)}`, '2026-10-21T13:00:00Z', 'refs/pull/9/merge'),
    cache(11, `gobuild-Linux-new-job-${hash(1)}-${sha(1)}`, '2026-10-01T10:00:00Z'),
    cache(12, `gobuild-Linux-new-job-${hash(1)}-${sha(2)}`, '2026-10-02T10:00:00Z')
  ];
  assert.deepEqual(
    superseded(caches).map(entry => entry.id).sort((a, b) => a - b),
    [1, 4, 6],
    'one per kind and ref survives; other refs and unknown keys are untouched'
  );
});
