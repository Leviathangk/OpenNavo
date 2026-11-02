// The gate that decides whether a commit may reach production. It runs inside the
// deploy workflow with the target commit checked out, so a mistake here either
// releases untested code or blocks every release; it had no test.
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test, { after } from 'node:test';

// The script runs at import time, so each case runs it as a child process with a
// fake `git` first on PATH and a stubbed fetch. Anything else would need the
// module to be restructured, and the point is to test it as it runs in CI.
const gitdir = mkdtempSync(join(tmpdir(), 'require-ci-'));
// Every run of this file made a new directory and left it behind: a full suite
// accumulates one per invocation. Removed once, after the last test.
after(() => rmSync(gitdir, { recursive: true, force: true }));
const fakeGit = join(gitdir, 'git');
writeFileSync(
  fakeGit,
  `#!/bin/sh
case "$1" in
  rev-parse) echo "${'a'.repeat(40)}" ;;
  fetch) exit 0 ;;
  merge-base) exit ${'${MERGE_BASE_EXIT:-0}'} ;;
  *) exit 1 ;;
esac
`,
  { mode: 0o755 }
);

const runner = `
const responses = JSON.parse(process.env.FAKE_RESPONSES);
// Record every request so a test can assert what the real call sends. A stub that
// only answers would accept a request with no Authorization header, which is a
// silent downgrade to the 60/hour anonymous quota rather than a visible failure.
globalThis.fetch = async (url, init = {}) => {
  console.log('REQUEST ' + JSON.stringify({ url, headers: init.headers ?? null }));
  const workflow = url.match(/workflows\\/([^/]+)\\/runs/)[1];
  const body = responses[workflow] ?? { workflow_runs: [] };
  if (body.status) return { ok: false, status: body.status, json: async () => ({}) };
  return { ok: true, status: 200, json: async () => body };
};
await import(${JSON.stringify(new URL('./require-ci.mjs', import.meta.url).href)});
`;

function runRequireCi(options = {}) {
  return execRequireCi(options, 'test-token');
}

// A separate entry so a test can leave the variable out entirely. Setting it to
// '' would still be falsy in-process, which would make the guard untestable.
function runRequireCiWithToken({ token, ...options }) {
  return execRequireCi(options, token);
}

function execRequireCi({ responses = {}, mergeBaseExit = 0, desktop = false } = {}, token) {
  const env = {
    ...process.env,
    PATH: `${gitdir}:${process.env.PATH}`,
    GITHUB_REPOSITORY: 'opennavo/OpenNavo',
    FAKE_RESPONSES: JSON.stringify(responses),
    MERGE_BASE_EXIT: String(mergeBaseExit),
    ...(desktop ? { REQUIRE_DESKTOP_CI: 'true' } : {})
  };
  delete env.GITHUB_TOKEN;
  if (token !== undefined) env.GITHUB_TOKEN = token;
  try {
    const stdout = execFileSync(process.execPath, ['--input-type=module', '-e', runner], {
      encoding: 'utf8',
      env,
      stdio: 'pipe',
      // Without this a child that never exits hangs the job until the workflow's
      // own timeout: this gate runs inside a deploy, so it must fail, not stall.
      timeout: 30_000
    });
    return { code: 0, stdout, stderr: '' };
  } catch (error) {
    return { code: error.status ?? 1, stdout: error.stdout ?? '', stderr: error.stderr ?? '' };
  }
}

const green = { workflow_runs: [{ head_branch: 'main', status: 'completed', conclusion: 'success', run_number: 1 }] };
const allGreen = () => ({ 'ci-server.yml': green, 'ci-frontend.yml': green, 'ci-security.yml': green });

test('a commit whose three gate workflows passed is allowed through', () => {
  const result = runRequireCi({ responses: allGreen() });
  assert.equal(result.code, 0, result.stderr);
  for (const workflow of ['ci-server.yml', 'ci-frontend.yml', 'ci-security.yml']) {
    assert.match(result.stdout, new RegExp(`${workflow}: passed`));
  }
});

test('one failing gate workflow blocks the release', () => {
  const responses = allGreen();
  responses['ci-frontend.yml'] = {
    workflow_runs: [{ head_branch: 'main', status: 'completed', conclusion: 'failure', run_number: 1 }]
  };
  const result = runRequireCi({ responses });
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /ci-frontend\.yml must pass/);
});

test('a workflow that has not finished, or never ran, blocks the release', () => {
  const running = allGreen();
  running['ci-server.yml'] = {
    workflow_runs: [{ head_branch: 'main', status: 'in_progress', conclusion: null, run_number: 1 }]
  };
  assert.notEqual(runRequireCi({ responses: running }).code, 0, 'an unfinished run is not a pass');

  const missing = allGreen();
  delete missing['ci-security.yml'];
  const result = runRequireCi({ responses: missing });
  assert.notEqual(result.code, 0, 'a workflow with no run must not be skipped silently');
  assert.match(result.stderr, /ci-security\.yml must pass/);
});

test('the newest attempt decides, and only runs on main count', () => {
  const newer = allGreen();
  newer['ci-server.yml'] = {
    workflow_runs: [
      { head_branch: 'main', status: 'completed', conclusion: 'success', run_number: 1 },
      { head_branch: 'main', status: 'completed', conclusion: 'failure', run_number: 2 }
    ]
  };
  assert.notEqual(runRequireCi({ responses: newer }).code, 0, 'a later failure must not be masked by an earlier pass');

  const otherBranch = allGreen();
  otherBranch['ci-frontend.yml'] = {
    workflow_runs: [{ head_branch: 'feature', status: 'completed', conclusion: 'success', run_number: 9 }]
  };
  assert.notEqual(runRequireCi({ responses: otherBranch }).code, 0, 'a green run on another branch is not this commit\'s');
});

test('the commit must be on main, checked before any workflow is queried', () => {
  const result = runRequireCi({ responses: allGreen(), mergeBaseExit: 1 });
  assert.notEqual(result.code, 0);
  assert.equal(result.stdout.includes('passed'), false, 'it stops before reporting any workflow');
});

test('desktop CI is required only when asked for', () => {
  const withoutDesktop = allGreen();
  withoutDesktop['ci-desktop.yml'] = {
    workflow_runs: [{ head_branch: 'main', status: 'completed', conclusion: 'failure', run_number: 1 }]
  };
  assert.equal(runRequireCi({ responses: withoutDesktop }).code, 0, 'a server release ignores desktop CI');

  const withDesktop = runRequireCi({ responses: withoutDesktop, desktop: true });
  assert.notEqual(withDesktop.code, 0, 'a desktop release requires it');
  assert.match(withDesktop.stderr, /ci-desktop\.yml must pass/);
});

test('a GitHub API error is not read as a pass', () => {
  const result = runRequireCi({ responses: { 'ci-server.yml': { status: 403 } } });
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /Cannot check ci-server\.yml: HTTP 403/);
});

test('a missing token fails instead of querying anonymously', () => {
  assert.equal(runRequireCi({ responses: allGreen() }).code, 0, 'the baseline authenticates');
  const result = runRequireCiWithToken({ responses: allGreen(), token: undefined });
  assert.notEqual(result.code, 0, 'no token must not be treated as authenticated');
  assert.match(result.stderr, /GitHub authentication is required/);
});

test('every request carries the bearer token', () => {
  // Deleting the header from the real module left this file 8/8 green before this
  // test existed: the stub answered regardless, so the request was never observed.
  // The effect is not fail-open -- an anonymous 403 still blocks the release --
  // but the 60/hour anonymous quota turns a missing credential into a rate-limit
  // error, which reads like flakiness instead of a broken gate.
  const result = runRequireCi({ responses: allGreen() });
  assert.equal(result.code, 0, result.stderr);
  const requests = result.stdout
    .split('\n')
    .filter(line => line.startsWith('REQUEST '))
    .map(line => JSON.parse(line.slice('REQUEST '.length)));
  assert.equal(requests.length, 3, 'one request per gate workflow');
  for (const request of requests) {
    assert.equal(request.headers?.Authorization, 'Bearer test-token', `${request.url} went out unauthenticated`);
  }
});
