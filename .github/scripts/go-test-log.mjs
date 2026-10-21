// Reads `go test -json` on stdin and prints what plain `go test` prints: build
// output, package results and the output of failing tests (and of tests still
// running when a package times out). Writes each top-level test's elapsed
// seconds to the JSON file named on the command line, so the next full run can
// balance its shards (go-shard.mjs).
//
//   go test -json ... | node go-test-log.mjs DURATIONS.json
import { writeFileSync } from 'node:fs';
import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';

export const MODULE = 'github.com/opennavo/opennavo/server/';

export function createLog(write) {
  const durations = {};
  const buffers = new Map();
  return {
    durations,
    line(text) {
      let event;
      try {
        event = JSON.parse(text);
      } catch {
        write(`${text}\n`);
        return;
      }
      const { Action: action, Package: pkg = '', Test: test, Output: output = '', Elapsed: elapsed } = event;
      if (action === 'build-output') return write(output);
      if (!test) {
        if (action === 'output') write(output);
        return;
      }
      const top = test.split('/')[0];
      const key = `${pkg}\t${top}`;
      if (action === 'output') {
        if (!buffers.has(key)) buffers.set(key, []);
        buffers.get(key).push(output);
        return;
      }
      if (test !== top || !['pass', 'fail', 'skip'].includes(action)) return;
      if (action !== 'skip') durations[`${pkg.replace(MODULE, '')} ${top}`] = elapsed;
      if (action === 'fail') write((buffers.get(key) ?? []).join(''));
      buffers.delete(key);
    },
    // Tests that never finished (a package timed out or crashed) still show their output.
    end() {
      for (const lines of buffers.values()) write(lines.join(''));
      buffers.clear();
    }
  };
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
  const log = createLog(text => process.stdout.write(text));
  for await (const text of createInterface({ input: process.stdin, crlfDelay: Infinity })) log.line(text);
  log.end();
  if (process.argv[2]) writeFileSync(process.argv[2], JSON.stringify(log.durations));
}
