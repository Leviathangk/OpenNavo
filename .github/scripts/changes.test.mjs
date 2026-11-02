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

test('every config file in the API directory stays out of the frontend jobs', () => {
  // Derived from the directory, not listed: oapi-public.yaml and oapi-admin.yaml are
  // real oapi-codegen configs and look like specs, so they are the first thing someone
  // widening the pattern adds. A fixed list left them uncovered, and a mutation that
  // pulled them into `frontend` passed the suite. Every yaml here that no generator
  // reads is asserted, so a new one is covered the moment it lands.
  const generated = new Set(generatedSpecs());
  const directory = new URL('../../apps/server/api/', import.meta.url);
  const files = readdirSync(directory)
    .filter(name => /\.ya?ml$/.test(name) && !generated.has(`apps/server/api/${name}`))
    .map(name => `apps/server/api/${name}`);
  // Guards a vacuous pass: the directory must yield the configs known to be there.
  for (const known of ['apps/server/api/oapi-public.yaml', 'apps/server/api/oapi-admin.yaml',
    'apps/server/api/redocly.yaml', 'apps/server/api/public-external.openapi.yaml']) {
    assert.ok(files.includes(known), `the discovery finds ${known}`);
  }
  // Paths the spec rules must reject although no generator reads them either. The
  // directory walk above cannot produce them: it lists what is there, and these are
  // the near-misses of what is there. Without them an anchor is unverified - dropping
  // `$` makes every path ending in the spec name match, dropping `^` makes every path
  // containing it match, and both leave the walk's own assertions green.
  files.push(
    'apps/server/api/public.openapi.yaml.bak',
    'apps/server/api/public.openapi.yamlx',
    'sub/apps/server/api/public.openapi.yaml',
    'vendor/apps/server/api/public.openapi.yaml',
    // A directory between `api/` and the name, as opposed to the prefix forms above. Only this
    // shape catches a rule that kept both anchors and inserted `.*` after `api/`; `.bak`,
    // `.yamlx` and a `sub/` prefix all survive that widening, because they differ from the spec
    // name at its edges, not in the middle.
    'apps/server/api/sub/public.openapi.yaml',
    'apps/server/api/sub/admin.openapi.yaml',
    'apps/server/api/admin.openapi.yaml.bak',
    'apps/server/api/admin.openapi.yamlx',
    'sub/apps/server/api/admin.openapi.yaml',
    'vendor/apps/server/api/admin.openapi.yaml'
  );
  for (const file of files) {
    assert.equal(classify([file]).frontend, false, `${file} is not generated into packages/, so the frontend jobs must not run`);
  }
});

test('the mcp spec rule matches the admin spec and nothing around it', () => {
  // The mcp area has its own rule for the same file. It must reject the same
  // near-misses, plus a longer name: `xadmin.openapi.yaml` differs from the spec only
  // before `admin`, so a rule that lost its `^` would classify it as mcp.
  assert.equal(classify(['apps/server/api/admin.openapi.yaml']).mcp, true, 'the spec reaches mcp');
  for (const file of ['apps/server/api/admin.openapi.yaml.bak',
    'apps/server/api/admin.openapi.yamlx',
    'sub/apps/server/api/admin.openapi.yaml',
    'vendor/apps/server/api/admin.openapi.yaml',
    // The mid-directory form: it survives a rule that kept `^` and `$` but inserted `.*`
    // after `api/`, which is the widening none of the edge forms above can see.
    'apps/server/api/sub/admin.openapi.yaml',
    'apps/server/api/xadmin.openapi.yaml']) {
    assert.equal(classify([file]).mcp, false, `${file} is not the admin spec, so it must not reach mcp`);
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

// A list of near-miss paths is still a list: it holds the forms its author thought of. The
// regex is what decides, so it is checked against the whole space of paths it could wrongly
// accept, generated rather than chosen. A rule widens in three directions, and this test covers
// the two a path can reach: an insertion before the file name (`.*`, `[^/]*`, `\w*` all do it)
// and a dropped `^` that accepts any leading path. The third - a generalised name - cannot be
// reached by any path, so it has its own test below.
test('the spec rules reject an insertion before the name and a leading path', () => {
  const fillers = ['x', '1', '.', '_', '-', '/', 'A'];
  const prefixes = ['sub/', 'vendor/', 'x/', 'foo/bar/', 'apps/', './'];
  for (const [spec, area] of [['public', 'frontend'], ['admin', 'frontend'], ['admin', 'mcp']]) {
    const rejected = [];
    for (const first of ['', ...fillers]) {
      for (const second of ['', ...fillers]) {
        if (!first && !second) continue;
        rejected.push(`apps/server/api/${first}${second}${spec}.openapi.yaml`);
      }
    }
    for (const prefix of prefixes) rejected.push(`${prefix}apps/server/api/${spec}.openapi.yaml`);
    for (const prefix of prefixes) rejected.push(`${prefix}apps/server/api/${spec}.openapi.yaml.bak`);
    // A generator that produced nothing makes the assertion below vacuously true: it
    // would pass with every rule widened. Emptying fillers and prefixes must fail here,
    // not pass, so the guard is the length check rather than the comparison.
    assert.ok(rejected.length > 0, 'the probe must generate at least one path');
    const accepted = rejected.filter(path => classify([path])[area]);
    assert.deepEqual(accepted, [], `the ${area} rule widened and accepts these`);
  }
  assert.equal(classify(['apps/server/api/public.openapi.yaml']).frontend, true);
  assert.equal(classify(['apps/server/api/admin.openapi.yaml']).frontend, true);
  assert.equal(classify(['apps/server/api/admin.openapi.yaml']).mcp, true);
});

// The third direction, which no constructed path reaches: the rule can stop insisting on the
// exact file name - `.yaml` -> `ya?ml`, a `/i` flag, another spec folded into the alternation,
// or the suffix relaxed to `.*`. A path shaped like a near-miss cannot expose that, because the
// widened rule would accept a name that is not a near-miss at all (`.yml`, `.YAML`, a sibling).
// So this asks the rule what it accepts instead: the real name must match, every generalised
// spelling must not.
test('the spec rules accept the exact name and no generalised spelling of it', () => {
  const real = {
    frontend: ['apps/server/api/public.openapi.yaml', 'apps/server/api/admin.openapi.yaml'],
    mcp: ['apps/server/api/admin.openapi.yaml']
  };
  // If a rule gained /i, `ya?ml`, or another alternative, one of these would start matching.
  const generalised = {
    frontend: [
      'apps/server/api/public.openapi.yml',
      'apps/server/api/admin.openapi.yml',
      'apps/server/api/public.openapi.YAML',
      'apps/server/api/admin.openapi.YAML',
      'apps/server/api/public-external.openapi.yaml',
      'apps/server/api/admin-external.openapi.yaml'
    ],
    mcp: [
      'apps/server/api/admin.openapi.yml',
      'apps/server/api/admin.openapi.YAML',
      'apps/server/api/public-external.openapi.yaml',
      'apps/server/api/public.openapi.yaml'
    ]
  };
  for (const [area, paths] of Object.entries(real)) {
    for (const path of paths) {
      assert.equal(classify([path])[area], true, `the ${area} rule matches ${path}`);
    }
    for (const path of generalised[area]) {
      assert.equal(classify([path])[area], false, `the ${area} rule generalised and matches ${path}`);
    }
  }
});
