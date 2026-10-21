import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { estimate, pattern, planShards, testsByPackage } from './go-shard.mjs';

const packages = new Map([
  ['internal/http', ['TestA', 'TestB', 'TestC', 'TestD', 'TestE', 'TestF']],
  ['internal/store', ['TestG', 'TestH']],
  ['cmd/seed', ['TestI']]
]);
const all = [...packages.values()].flat().sort();

test('without recordings, tests are dealt in turn and all of them run', () => {
  const shards = planShards(packages, 3);
  assert.deepEqual(shards.flat().sort(), all);
  for (const shard of shards) assert.equal(shard.length, 3);
  assert.equal(pattern(['TestA', 'TestB', 'TestA']), '^(TestA|TestB)$');
  assert.equal(pattern([]), '^$');
});

test('recorded durations put the longest tests on separate shards', () => {
  const durations = {
    'internal/http TestA': 120,
    'internal/http TestB': 110,
    'internal/http TestC': 100,
    'internal/http TestD': 4,
    'internal/http TestE': 3,
    'internal/store TestG': 5,
    'internal/store TestH': 2
  };
  const shards = planShards(packages, 3, durations);
  assert.deepEqual(shards.flat().sort(), all, 'tests without a recording still run');
  const heavy = ['TestA', 'TestB', 'TestC'];
  for (const shard of shards) assert.equal(shard.filter(name => heavy.includes(name)).length, 1);
});

test('the server module is fully covered by six shards', () => {
  const root = fileURLToPath(new URL('../../apps/server', import.meta.url));
  const tests = new Set([...testsByPackage(root).values()].flat());
  assert.ok(tests.size > 500, `found only ${tests.size} tests`);
  const covered = new Set(planShards(testsByPackage(root), 6).flat());
  assert.deepEqual(
    [...tests].filter(name => !covered.has(name)),
    []
  );
});

test('tests recorded at 0 s stay light instead of taking the median', () => {
  const durations = { 'internal/http TestA': 60, 'internal/http TestB': 50, 'internal/http TestC': 40 };
  for (const name of ['TestD', 'TestE', 'TestF']) durations[`internal/http ${name}`] = 0;
  for (const name of ['TestG', 'TestH']) durations[`internal/store ${name}`] = 0;
  durations['cmd/seed TestI'] = 0;
  const shards = planShards(packages, 3, durations);
  for (const shard of shards) assert.equal(shard.filter(name => ['TestA', 'TestB', 'TestC'].includes(name)).length, 1);
});

test('tests of one package run one after another, so its long chain is split', () => {
  // http: three 90 s tests in one package; store: twelve 20 s tests that run beside it.
  const chained = new Map([
    ['internal/http', ['TestH1', 'TestH2', 'TestH3']],
    ['internal/store', Array.from({ length: 12 }, (_, i) => `TestS${i}`)]
  ]);
  const durations = { 'internal/http TestH1': 90, 'internal/http TestH2': 90, 'internal/http TestH3': 90 };
  for (let i = 0; i < 12; i++) durations[`internal/store TestS${i}`] = 20;
  const plan = planShards(chained, 3, durations);
  for (const shard of plan) assert.equal(shard.filter(name => name.startsWith('TestH')).length, 1);
  assert.ok(Math.max(...estimate(chained, plan, durations)) <= 90, 'no shard waits on two http tests');
});
