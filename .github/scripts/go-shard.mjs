// Plans `go test -run` patterns for COUNT integration shards and prints them as
// a JSON array. With recorded durations (go-test-log.mjs files from the last
// green main run) the longest tests go first to the least-loaded shard; tests
// without a recording count as the median one. Without recordings, tests are
// dealt in turn, package by package. Either way all shards together run every
// top-level test.
//
//   node go-shard.mjs ROOT COUNT [DURATIONS_DIR]
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { pathToFileURL } from 'node:url';

export function testsByPackage(root) {
  const packages = new Map();
  const walk = dir => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.name.startsWith('.') || ['node_modules', 'testdata', 'vendor'].includes(entry.name)) continue;
      const path = join(dir, entry.name);
      if (entry.isDirectory()) {
        walk(path);
      } else if (entry.name.endsWith('_test.go')) {
        const names = [...readFileSync(path, 'utf8').matchAll(/^func (Test\w+)\(\w+ \*testing\.T\)/gm)].map(
          match => match[1]
        );
        const pkg = relative(root, dirname(path)) || '.';
        packages.set(pkg, [...(packages.get(pkg) ?? []), ...names]);
      }
    }
  };
  walk(root);
  return packages;
}

// Recorded seconds keyed "<package dir> <TestName>", merged from every JSON file under dir.
export function readDurations(dir) {
  const durations = {};
  if (!dir || !existsSync(dir)) return durations;
  const walk = path => {
    if (statSync(path).isDirectory()) return readdirSync(path).forEach(entry => walk(join(path, entry)));
    if (path.endsWith('.json')) Object.assign(durations, JSON.parse(readFileSync(path, 'utf8')));
  };
  walk(dir);
  return durations;
}

export function planShards(packages, count, durations = {}) {
  const tests = [];
  for (const pkg of [...packages.keys()].sort()) {
    for (const name of [...new Set(packages.get(pkg))].sort())
      tests.push({ name, weight: durations[`${pkg} ${name}`] });
  }
  const shards = Array.from({ length: count }, () => ({ load: 0, names: [] }));
  const known = tests
    .map(test => test.weight)
    .filter(weight => typeof weight === 'number')
    .sort((a, b) => a - b);
  if (!known.length) {
    tests.forEach((test, turn) => shards[turn % count].names.push(test.name));
    return shards.map(shard => shard.names);
  }
  // A test recorded at 0 s stays light; only tests never recorded take the median.
  const median = known[Math.floor(known.length / 2)];
  for (const test of tests) test.weight = typeof test.weight === 'number' ? Math.max(test.weight, 0.01) : median;
  for (const test of [...tests].sort((a, b) => b.weight - a.weight)) {
    const shard = shards.reduce((least, candidate) => (candidate.load < least.load ? candidate : least));
    shard.load += test.weight;
    shard.names.push(test.name);
  }
  return shards.map(shard => shard.names);
}

export const pattern = names => (names.length ? `^(${[...new Set(names)].join('|')})$` : '^$');

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [root, count, durationsDir] = process.argv.slice(2);
  if (!root || !(Number(count) > 0)) throw new Error('usage: node go-shard.mjs ROOT COUNT [DURATIONS_DIR]');
  const durations = readDurations(durationsDir);
  console.error(`${Object.keys(durations).length} recorded test durations`);
  console.log(JSON.stringify(planShards(testsByPackage(root), Number(count), durations).map(pattern)));
}
