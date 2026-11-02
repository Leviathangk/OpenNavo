import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import test from 'node:test';
import { baseline, classify, greenCommits, plan } from './changes.mjs';

const none = { server: false, mcp: false, frontend: false, desktop: false, e2e: false };

test('a server change runs server checks and full-stack E2E, not desktop builds', () => {
  assert.deepEqual(classify(['apps/server/internal/articles/evidence.go']), { ...none, server: true, e2e: true });
  // frontend: the spec is generated into packages/api, see the generator test below.
  assert.deepEqual(classify(['apps/server/api/admin.openapi.yaml']), {
    ...none,
    server: true,
    mcp: true,
    frontend: true,
    e2e: true
  });
});

// Every spec a generator reads, discovered from the scripts themselves rather than
// listed here: a third spec added to a generator must reach the frontend jobs on its
// own, without anyone remembering to come back to this file.
function generatedSpecs() {
  const found = new Set();
  for (const directory of readdirSync(new URL('../../packages/', import.meta.url))) {
    let pkg;
    try {
      pkg = JSON.parse(readFileSync(new URL(`../../packages/${directory}/package.json`, import.meta.url), 'utf8'));
    } catch {
      continue; // not a package
    }
    for (const script of Object.values(pkg.scripts ?? {})) {
      for (const [, spec] of script.matchAll(/(apps\/server\/api\/[\w.-]+\.openapi\.yaml)/g)) found.add(spec);
    }
  }
  return [...found].sort();
}

test('every API spec a generator reads reaches the frontend jobs', () => {
  const specs = generatedSpecs();
  // The discovery must find the package that is known to generate types, not
  // merely something: a broken pattern would otherwise assert nothing and pass.
  assert.ok(specs.includes('apps/server/api/public.openapi.yaml'), 'the discovery finds the public generator');
  for (const spec of specs) {
    assert.equal(classify([spec]).frontend, true, `${spec} is generated into packages/api, so the frontend jobs must run`);
  }
});

test('spec files no generator reads do not reach the frontend jobs', () => {
  // Derived for the same reason as generatedSpecs(): when a generator starts
  // reading one of these it moves to the list above by itself, rather than
  // leaving a stale entry here that only looks like coverage.
  const generated = new Set(generatedSpecs());
  const unread = readdirSync(new URL('../../apps/server/api/', import.meta.url))
    .filter(name => name.endsWith('.openapi.yaml') && !generated.has(`apps/server/api/${name}`))
    .map(name => `apps/server/api/${name}`);
  // Config files, not specs: no generator can read them, so they never move.
  const files = [...unread, 'apps/server/api/oapi-codegen.yaml', 'apps/server/api/redocly.yaml'];
  for (const file of files) {
    assert.equal(classify([file]).frontend, false, `${file} is not generated into packages/, so the frontend jobs must not run`);
  }
});

test('shared packages reach the desktop only through frontend jobs', () => {
  assert.deepEqual(classify(['packages/ui/src/Button.vue']), { ...none, frontend: true, e2e: true });
  assert.deepEqual(classify(['apps/desktop/src-tauri/src/main.rs']), { ...none, frontend: true, desktop: true });
  assert.deepEqual(classify(['apps/web/app/pages/index.vue']), { ...none, frontend: true, e2e: true });
});

test('documentation changes need no build', () => {
  assert.deepEqual(classify(['docs/guide.md', 'README.md']), none);
  assert.deepEqual(classify(['docs/design/logo/source/mark.svg']), { ...none, frontend: true });
});

test('workspace files and workflow edits reach the areas they configure', () => {
  assert.deepEqual(classify(['pnpm-lock.yaml']), { ...none, server: true, frontend: true, e2e: true });
  assert.deepEqual(classify(['.github/workflows/ci-frontend.yml']), { ...none, frontend: true, e2e: true });
  assert.deepEqual(classify(['.github/workflows/ci-desktop.yml']), { ...none, desktop: true });
  assert.deepEqual(classify(['.github/workflows/ci-server.yml']), { ...none, server: true, mcp: true });
  assert.deepEqual(classify(['apps/mcp/src/opennavo_mcp/resources.py']), { ...none, mcp: true });
});

test('depth: pull requests are fast unless labelled, main and scheduled runs are full', () => {
  const files = ['apps/server/main.go'];
  assert.equal(plan('pull_request', { pull_request: { labels: [] } }, files).full, false);
  assert.equal(plan('pull_request', { pull_request: { labels: [{ name: 'full-ci' }] } }, files).full, true);
  assert.deepEqual(plan('push', {}, files), { ...none, server: true, e2e: true, full: true });
  assert.deepEqual(plan('schedule', {}, null), {
    server: true,
    mcp: true,
    frontend: true,
    desktop: true,
    e2e: true,
    full: true
  });
  assert.equal(
    plan('pull_request', { pull_request: { labels: [] } }, null).desktop,
    true,
    'unlisted changes run every area'
  );
});

test('main compares with the newest commit on which every gate workflow passed', () => {
  const run = (sha, conclusion = 'success', branch = 'main') => ({
    head_sha: sha,
    head_branch: branch,
    status: 'completed',
    conclusion
  });
  const server = [run('c3', 'failure'), run('c2'), run('c1')];
  const frontend = [run('c3'), run('c2', 'failure'), run('c1')];
  const security = [run('c3'), run('c2'), run('c1'), run('x', 'success', 'feature')];
  assert.deepEqual(greenCommits([server, frontend, security]), ['c1']);
  assert.deepEqual(greenCommits([[run('c2'), run('c1')], [run('c2'), run('c1')], [run('c1')]]), ['c1']);
  assert.deepEqual(greenCommits([[], [], []]), []);
});

test('Desktop CI compares main pushes with its own green commits, the gate with the gate', () => {
  assert.deepEqual(baseline('opennavo/OpenNavo/.github/workflows/ci-desktop.yml@refs/heads/main'), ['ci-desktop.yml']);
  assert.deepEqual(baseline('opennavo/OpenNavo/.github/workflows/ci-frontend.yml@refs/heads/main'), [
    'ci-server.yml',
    'ci-frontend.yml',
    'ci-security.yml'
  ]);
  assert.deepEqual(baseline(undefined), ['ci-server.yml', 'ci-frontend.yml', 'ci-security.yml']);
});
