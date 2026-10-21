import assert from 'node:assert/strict';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { pattern, shardTests, testsByPackage } from './go-shard.mjs';

test('shards cover every test once and spread each package', () => {
  const packages = new Map([
    ['internal/http', ['TestA', 'TestB', 'TestC', 'TestD', 'TestE', 'TestF']],
    ['internal/store', ['TestG', 'TestH']],
    ['cmd/seed', ['TestI']]
  ]);
  const shards = [0, 1, 2].map(index => shardTests(packages, index, 3));
  assert.deepEqual(shards.flat().sort(), [...packages.values()].flat().sort());
  for (const shard of shards)
    assert.equal(shard.filter(name => packages.get('internal/http').includes(name)).length, 2);
  assert.equal(pattern(['TestA', 'TestB']), '^(TestA|TestB)$');
  assert.equal(pattern([]), '^$');
});

test('the server module is fully covered by six shards', () => {
  const root = fileURLToPath(new URL('../../apps/server', import.meta.url));
  const packages = testsByPackage(root);
  const all = new Set([...packages.values()].flat());
  const covered = new Set([0, 1, 2, 3, 4, 5].flatMap(index => shardTests(packages, index, 6)));
  assert.ok(all.size > 500, `found only ${all.size} tests`);
  assert.deepEqual(
    [...all].filter(name => !covered.has(name)),
    []
  );
  const http = packages.get('internal/http');
  for (const index of [0, 1, 2, 3, 4, 5]) {
    const share = shardTests(packages, index, 6).filter(name => http.includes(name)).length;
    assert.ok(
      Math.abs(share - http.length / 6) <= 1,
      `shard ${index} has ${share} of ${http.length} internal/http tests`
    );
  }
});
