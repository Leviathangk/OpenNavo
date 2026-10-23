import assert from 'node:assert/strict';
import test from 'node:test';
import { MODULE, createLog } from './go-test-log.mjs';

const event = (action, extra = {}) => JSON.stringify({ Action: action, Package: `${MODULE}internal/http`, ...extra });

test('prints what plain go test prints and records top-level durations', () => {
  const out = [];
  const log = createLog(text => out.push(text));
  for (const line of [
    event('run', { Test: 'TestPass' }),
    event('output', { Test: 'TestPass', Output: '=== RUN   TestPass\n' }),
    event('pass', { Test: 'TestPass/sub', Elapsed: 1 }),
    event('pass', { Test: 'TestPass', Elapsed: 12.5 }),
    event('output', { Test: 'TestFail', Output: '    handler_test.go:10: boom\n' }),
    event('fail', { Test: 'TestFail', Elapsed: 3 }),
    event('output', { Output: 'FAIL\tgithub.com/opennavo/opennavo/server/internal/http\t15.6s\n' }),
    'go: downloading example.com/x v1.0.0'
  ]) {
    log.line(line);
  }
  log.end();
  assert.deepEqual(log.durations, { 'internal/http TestPass': 12.5, 'internal/http TestFail': 3 });
  const text = out.join('');
  assert.match(text, /boom/);
  assert.match(text, /FAIL\tgithub.com/);
  assert.match(text, /go: downloading/);
  assert.doesNotMatch(text, /=== RUN   TestPass/, 'passing tests stay quiet');
  assert.equal(log.failed, true);
});

test('fails only when a test or package failed', () => {
  const passing = createLog(() => {});
  passing.line(event('pass', { Test: 'TestPass', Elapsed: 1 }));
  passing.line(event('pass', { Elapsed: 1 }));
  passing.line(event('skip', { Test: 'TestSkip' }));
  passing.end();
  assert.equal(passing.failed, false);
  // A package that does not build fails without any failing test.
  const broken = createLog(() => {});
  broken.line(JSON.stringify({ ImportPath: `${MODULE}internal/http`, Action: 'build-output', Output: 'syntax error\n' }));
  broken.line(JSON.stringify({ ImportPath: `${MODULE}internal/http`, Action: 'build-fail' }));
  broken.line(event('fail', { Elapsed: 0 }));
  broken.end();
  assert.equal(broken.failed, true);
});

test('the command exits 1 when a test failed, so a pipe without pipefail still fails', async () => {
  const { spawnSync } = await import('node:child_process');
  const script = new URL('./go-test-log.mjs', import.meta.url).pathname;
  const run = input => spawnSync(process.execPath, [script], { input, encoding: 'utf8' }).status;
  assert.equal(run(`${event('pass', { Test: 'TestPass', Elapsed: 1 })}\n`), 0);
  assert.equal(run(`${event('fail', { Test: 'TestFail', Elapsed: 1 })}\n${event('fail', { Elapsed: 1 })}\n`), 1);
});

test('a test still running when the package dies shows its output', () => {
  const out = [];
  const log = createLog(text => out.push(text));
  log.line(event('output', { Test: 'TestHang', Output: 'waiting for container\n' }));
  log.line(event('output', { Output: 'panic: test timed out after 25m0s\n' }));
  log.end();
  assert.match(out.join(''), /waiting for container/);
  assert.match(out.join(''), /panic: test timed out/);
});
