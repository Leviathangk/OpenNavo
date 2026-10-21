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
