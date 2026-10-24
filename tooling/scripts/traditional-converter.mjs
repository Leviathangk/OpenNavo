import { spawnSync } from 'node:child_process';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = resolve(fileURLToPath(new URL('../..', import.meta.url)));

export function convertTraditional(fields) {
  const result = spawnSync('go', ['run', './cmd/derive-traditional'], {
    cwd: resolve(root, 'apps/server'),
    input: JSON.stringify(fields),
    encoding: 'utf8',
    maxBuffer: 16 * 1024 * 1024
  });
  if (result.status !== 0) throw new Error(`Traditional Chinese generation failed: ${result.stderr}`);
  const converted = JSON.parse(result.stdout);
  const ordered = (source, target) => {
    if (Array.isArray(source)) return source.map((value, index) => ordered(value, target[index]));
    if (source && typeof source === 'object')
      return Object.fromEntries(Object.entries(source).map(([key, value]) => [key, ordered(value, target[key])]));
    return target;
  };
  return ordered(fields, converted);
}
