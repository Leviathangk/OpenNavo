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

// go test runs a shard's packages side by side (-p, the runner's 4 cores) but
// one package's tests one after another, so a shard takes about as long as its
// longest package chain or a quarter of its total, whichever is larger.
export const PARALLEL = 4;
const cost = shard => Math.max(shard.longest, shard.total / PARALLEL);

export function planShards(packages, count, durations = {}) {
  const tests = [];
  for (const pkg of [...packages.keys()].sort()) {
    for (const name of [...new Set(packages.get(pkg))].sort())
      tests.push({ pkg, name, weight: durations[`${pkg} ${name}`] });
  }
  const shards = Array.from({ length: count }, () => ({ chains: new Map(), longest: 0, total: 0, names: [] }));
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
  // Longest first, each to the shard whose estimated time grows least.
  for (const test of [...tests].sort((a, b) => b.weight - a.weight)) {
    let best;
    let bestCost = Infinity;
    for (const shard of shards) {
      const chain = (shard.chains.get(test.pkg) ?? 0) + test.weight;
      const after = Math.max(shard.longest, chain, (shard.total + test.weight) / PARALLEL);
      if (after < bestCost || (after === bestCost && shard.total < best.total)) {
        best = shard;
        bestCost = after;
      }
    }
    const chain = (best.chains.get(test.pkg) ?? 0) + test.weight;
    best.chains.set(test.pkg, chain);
    best.longest = Math.max(best.longest, chain);
    best.total += test.weight;
    best.names.push(test.name);
  }
  return shards.map(shard => shard.names);
}

// Estimated seconds per shard for a plan, by the same model.
export function estimate(packages, plan, durations) {
  const pkgOf = new Map();
  for (const [pkg, names] of packages) for (const name of names) pkgOf.set(name, pkg);
  return plan.map(names => {
    const shard = { chains: new Map(), longest: 0, total: 0 };
    for (const name of names) {
      const weight = durations[`${pkgOf.get(name)} ${name}`] ?? 0;
      const chain = (shard.chains.get(pkgOf.get(name)) ?? 0) + weight;
      shard.chains.set(pkgOf.get(name), chain);
      shard.longest = Math.max(shard.longest, chain);
      shard.total += weight;
    }
    return cost(shard);
  });
}

export const pattern = names => (names.length ? `^(${[...new Set(names)].join('|')})$` : '^$');

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [root, count, durationsDir] = process.argv.slice(2);
  if (!root || !(Number(count) > 0)) throw new Error('usage: node go-shard.mjs ROOT COUNT [DURATIONS_DIR]');
  const durations = readDurations(durationsDir);
  console.error(`${Object.keys(durations).length} recorded test durations`);
  console.log(JSON.stringify(planShards(testsByPackage(root), Number(count), durations).map(pattern)));
}
