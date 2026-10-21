import assert from 'node:assert/strict';
import test from 'node:test';
import { deployed, flaky, lastDeployed, passed, ships } from './ship.mjs';

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
});

test('the live revision comes from the newest successful deploy', () => {
  const sha = 'd1cb7edb9d954798ca63cb765077284073294f52';
  const runs = [
    { run_number: 3, conclusion: 'failure', display_title: 'Deploy production ' + 'f'.repeat(40), head_sha: 'x' },
    { run_number: 2, conclusion: 'success', display_title: `Deploy production ${sha}`, head_sha: 'y' },
    { run_number: 1, conclusion: 'success', display_title: 'Deploy production main', head_sha: 'z' }
  ];
  assert.equal(lastDeployed(runs), sha);
  assert.equal(lastDeployed(runs.slice(2)), 'z', 'a deploy of "main" ran on its head commit');
  assert.equal(lastDeployed([]), null);
});
