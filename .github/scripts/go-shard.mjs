// Prints a `go test -run` pattern for shard INDEX of COUNT. Tests are dealt in
// turn, package by package, so every shard gets about the same share of every
// package, including the long internal/http suite, and no list of timings has
// to be kept up to date. All shards together run every top-level test.
//
//   node go-shard.mjs ROOT INDEX COUNT
import { readdirSync, readFileSync } from 'node:fs';
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

export function shardTests(packages, index, count) {
  const selected = new Set();
  let turn = 0;
  for (const pkg of [...packages.keys()].sort()) {
    for (const name of [...packages.get(pkg)].sort()) {
      if (turn++ % count === index) selected.add(name);
    }
  }
  return [...selected];
}

export const pattern = names => (names.length ? `^(${names.join('|')})$` : '^$');

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [root, index, count] = process.argv.slice(2);
  if (!root || !(Number(count) > 0) || !(Number(index) >= 0 && Number(index) < Number(count))) {
    throw new Error('usage: node go-shard.mjs ROOT INDEX COUNT');
  }
  console.log(pattern(shardTests(testsByPackage(root), Number(index), Number(count))));
}
