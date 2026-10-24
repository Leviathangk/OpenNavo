// Node only exports and writes results; only Go reads gateway and secret configuration.
import { mkdirSync, writeFileSync, readFileSync, rmSync, existsSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { generateTraditional } from './derive-traditional.mjs';
import { exportPlan, selection, writeResult } from './interface-i18n.mjs';
function main() {
  if (!process.argv.includes('--dry-run') && !process.argv.includes('--replay')) generateTraditional();
  const args = process.argv.slice(2),
    names = selection(args),
    plan = exportPlan(names);
  mkdirSync('apps/server/tmp/i18n-sync', { recursive: true });
  const input = `tmp/i18n-sync/input-${process.pid}.json`,
    output = `tmp/i18n-sync/output-${process.pid}.json`;
  writeFileSync(`apps/server/${input}`, JSON.stringify(plan), { mode: 0o600 });
  const flags = args.filter(
    (x, i) => !x.startsWith('--targets=') && x !== '--targets' && (i === 0 || args[i - 1] !== '--targets')
  );
  try {
    const result = spawnSync('go', ['run', './cmd/i18n-sync', '--input', input, '--output', output, ...flags], {
      cwd: 'apps/server',
      stdio: 'inherit'
    });
    if (result.status !== 0) process.exitCode = result.status ?? 1;
    // Do not write failed batches, but persist completed batches to avoid duplicate charges on retries.
    if (!args.includes('--dry-run') && existsSync(`apps/server/${output}`))
      writeResult(plan, JSON.parse(readFileSync(`apps/server/${output}`, 'utf8')), true);
  } finally {
    for (const path of [input, output]) rmSync(`apps/server/${path}`, { force: true });
  }
}
try {
  main();
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
