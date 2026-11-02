import assert from 'node:assert/strict';
import test from 'node:test';
import { deployMain, deployed, dispatch, flaky, lastDeployed, passed, ships, superseded } from './ship.mjs';

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
  // The bootstrap installs the receiver, so it is never part of what a deployment
  // installs: counting it as shipped would dispatch a deploy that changes nothing on
  // the host but the payload. Its own test file is excluded by the test_ rule anyway.
  assert.equal(ships(['ops/deploy-bootstrap.py']), false, 'operators install the bootstrap by hand');
  assert.equal(ships(['ops/test_deploy_bootstrap.py']), false, 'bootstrap tests never reach a host');
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

// The rest of this file drives deployMain() through a fake api(): it is the only
// code that starts a production deploy, and every mistake there is either a
// deploy of a superseded commit or a second deploy of the same one.
const SHA = 'd1cb7edb9d954798ca63cb765077284073294f52';
const LIVE = 'a'.repeat(40);

// Records every request so a test can assert what was dispatched, not merely that
// nothing threw.
function fakeApi({ tip = SHA, gate = {}, deploys = [], files = [], exists = false } = {}) {
  const calls = [];
  const api = async (path, init = {}) => {
    calls.push({ path, method: init.method ?? 'GET', body: init.body });
    if (path === '/branches/main') return { commit: { sha: tip } };
    const gateMatch = path.match(/^\/actions\/workflows\/([^/]+)\/runs\?head_sha=/);
    if (gateMatch) {
      const runs = gate[gateMatch[1]];
      if (runs === undefined) return { workflow_runs: [] };
      return { workflow_runs: runs };
    }
    if (path.startsWith('/actions/workflows/deploy-production.yml/runs')) {
      return { workflow_runs: exists ? [{ display_title: `Deploy production ${SHA}`, status: 'completed', conclusion: 'success', run_number: 1 }, ...deploys] : deploys };
    }
    if (path.startsWith('/compare/')) {
      return files === null ? { files: null } : { files: files.map(filename => ({ filename })) };
    }
    if (path === '/actions/workflows/deploy-production.yml/dispatches') return null;
    throw new Error(`unexpected ${init.method ?? 'GET'} ${path}`);
  };
  const dispatched = () => calls.filter(call => call.path.endsWith('/dispatches'));
  const requested = () => calls.map(call => call.path);
  return { api, calls, dispatched, requested };
}

const greenGate = () =>
  Object.fromEntries(['ci-server.yml', 'ci-frontend.yml', 'ci-security.yml'].map(name => [
    name,
    [{ head_branch: 'main', status: 'completed', conclusion: 'success', run_number: 1 }]
  ]));

test('a commit that is no longer the tip of main is not deployed', async () => {
  const { api, dispatched, requested } = fakeApi({ tip: 'b'.repeat(40) });
  await deployMain(api, SHA);
  assert.deepEqual(dispatched(), [], 'the newer commit ships itself');
  assert.deepEqual(requested(), ['/branches/main'], 'it stops before asking about CI');
});

test('a commit whose gate has not all passed is not deployed', async () => {
  const gate = greenGate();
  gate['ci-frontend.yml'] = [{ head_branch: 'main', status: 'completed', conclusion: 'failure', run_number: 1 }];
  const { api, dispatched } = fakeApi({ gate });
  await deployMain(api, SHA);
  assert.deepEqual(dispatched(), [], 'one red workflow is enough to hold the deploy');
});

test('only push runs of the newest attempt on main count as a passing gate', async () => {
  // A green run left over from an earlier attempt must not release a later one.
  const gate = greenGate();
  gate['ci-security.yml'] = [
    { head_branch: 'main', status: 'completed', conclusion: 'success', run_number: 1 },
    { head_branch: 'main', status: 'completed', conclusion: 'failure', run_number: 2 }
  ];
  const { api, dispatched } = fakeApi({ gate });
  await deployMain(api, SHA);
  assert.deepEqual(dispatched(), [], 'the newest attempt for the workflow decides');

  const other = greenGate();
  other['ci-server.yml'] = [{ head_branch: 'feature', status: 'completed', conclusion: 'success', run_number: 9 }];
  const second = fakeApi({ gate: other });
  await deployMain(second.api, SHA);
  assert.deepEqual(second.dispatched(), [], 'a run on another branch is not this commit\'s gate');
});

test('a commit already deploying or deployed returns before comparing revisions', async () => {
  // This guard is a second line of defence: without it the compare would still be
  // empty (a revision against itself) and ships([]) would stop the deploy. What it
  // buys is one API call fewer, so that is what this asserts.
  const { api, dispatched, requested } = fakeApi({ gate: greenGate(), exists: true });
  await deployMain(api, SHA);
  assert.deepEqual(dispatched(), [], 'the deploy would be a duplicate');
  assert.equal(requested().some(path => path.startsWith('/compare/')), false, 'it stops before comparing');
});

test('a commit that ships nothing new since the live revision is not deployed', async () => {
  const deploys = [
    { display_title: `Deploy production ${LIVE}`, status: 'completed', conclusion: 'success', run_number: 1 }
  ];
  const { api, dispatched, requested } = fakeApi({
    gate: greenGate(),
    deploys,
    files: ['docs/guide.md', 'apps/desktop/src-tauri/src/main.rs']
  });
  await deployMain(api, SHA);
  assert.deepEqual(dispatched(), [], 'nothing that ships changed since the live revision');
  assert.ok(requested().includes(`/compare/${LIVE}...${SHA}`), 'it compared against the live revision');
});

test('a commit that ships something new is dispatched exactly once with its own revision', async () => {
  const deploys = [
    { display_title: `Deploy production ${LIVE}`, status: 'completed', conclusion: 'success', run_number: 1 }
  ];
  const { api, dispatched } = fakeApi({ gate: greenGate(), deploys, files: ['apps/server/internal/http/router.go'] });
  await deployMain(api, SHA);
  assert.equal(dispatched().length, 1, 'one dispatch, never two');
  assert.equal(dispatched()[0].method, 'POST');
  assert.deepEqual(JSON.parse(dispatched()[0].body), { ref: 'main', inputs: { revision: SHA } });
});

test('a first deploy with no live revision is dispatched, and an over-long comparison still deploys', async () => {
  const first = fakeApi({ gate: greenGate(), deploys: [] });
  await deployMain(first.api, SHA);
  assert.equal(first.dispatched().length, 1, 'nothing is live yet, so everything ships');

  // 300 files or more makes the comparison truncate; ships(null) treats it as
  // "unknown, so assume it ships" rather than silently skipping the deploy.
  const many = Array.from({ length: 300 }, (_, index) => `docs/${index}.md`);
  const truncated = fakeApi({
    gate: greenGate(),
    deploys: [{ display_title: `Deploy production ${LIVE}`, status: 'completed', conclusion: 'success', run_number: 1 }],
    files: many
  });
  await deployMain(truncated.api, SHA);
  assert.equal(truncated.dispatched().length, 1, 'a truncated comparison must not skip the deploy');
});

test('a failing run is rerun only when every failed job log shows a known flake', async () => {
  const run = {
    name: 'Backend CI',
    conclusion: 'failure',
    event: 'push',
    head_branch: 'main',
    head_sha: SHA,
    run_attempt: 1,
    id: 7
  };
  const apiFor = logs => {
    const calls = [];
    return {
      calls,
      api: async (path, init = {}) => {
        calls.push({ path, method: init.method ?? 'GET' });
        if (path === '/actions/runs/7/jobs?per_page=100') {
          return { jobs: logs.map((_, index) => ({ id: index, conclusion: 'failure' })) };
        }
        if (path.startsWith('/actions/jobs/')) return logs[Number(path.split('/')[3])];
        if (path === '/actions/runs/7/rerun-failed-jobs') return null;
        throw new Error(`unexpected ${path}`);
      }
    };
  };
  const flaked = apiFor(['429 Too Many Requests', 'toomanyrequests']);
  await dispatch(flaked.api, run);
  assert.deepEqual(flaked.calls.at(-1), { path: '/actions/runs/7/rerun-failed-jobs', method: 'POST' });

  const real = apiFor(['429 Too Many Requests', 'assertion failed: expected 2, got 3']);
  await dispatch(real.api, run);
  assert.equal(real.calls.some(call => call.path.endsWith('/rerun-failed-jobs')), false, 'one real failure blocks the rerun');

  const exhausted = { ...run, run_attempt: 3 };
  const capped = apiFor(['429 Too Many Requests']);
  await dispatch(capped.api, exhausted);
  assert.equal(capped.calls.some(call => call.path.endsWith('/rerun-failed-jobs')), false, 'MAX_ATTEMPTS stops the loop');
});

test('only a successful push to main reaches the deploy path', async () => {
  const cases = [
    { conclusion: 'success', event: 'push', head_branch: 'main', expected: true },
    { conclusion: 'success', event: 'pull_request', head_branch: 'main', expected: false },
    { conclusion: 'success', event: 'push', head_branch: 'feature', expected: false },
    { conclusion: 'cancelled', event: 'push', head_branch: 'main', expected: false }
  ];
  for (const { expected, ...fields } of cases) {
    const { api, requested } = fakeApi({ gate: greenGate(), files: ['apps/server/main.go'] });
    await dispatch(api, { name: 'CI', run_attempt: 1, id: 1, head_sha: SHA, ...fields });
    const deployedIt = requested().includes('/actions/workflows/deploy-production.yml/dispatches');
    assert.equal(deployedIt, expected, JSON.stringify(fields));
  }
});
