import { convertTraditional } from './traditional-converter.mjs';
import { readFileSync, writeFileSync, readdirSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';
import { loadTarget } from './interface-i18n.mjs';

export function generateTraditional(check = false) {
  const inputs = {};
  for (const name of ['web', 'desktop', 'shared', 'native']) {
    const target = loadTarget(name);
    inputs[target.path('zh-Hant')] = target.source;
  }
  const directory = 'apps/desktop/release-notes';
  for (const version of readdirSync(directory)) {
    const source = `${directory}/${version}/zh-CN.md`;
    // Historical six-language releases need no new files; already existing
    // Traditional notes and every release after 0.1.16 are derived.
    const parts = version.split('.').map(Number);
    const current = parts[0] > 0 || parts[1] > 1 || parts[2] > 16;
    if (existsSync(source) && (current || existsSync(`${directory}/${version}/zh-Hant.md`)))
      inputs[`${directory}/${version}/zh-Hant.md`] = readFileSync(source, 'utf8');
  }
  const outputs = convertTraditional(inputs);
  for (const [path, value] of Object.entries(outputs)) {
    const text = typeof value === 'string' ? value : JSON.stringify(value, null, 2) + '\n';
    if (existsSync(path) && readFileSync(path, 'utf8') === text) continue;
    if (check)
      throw new Error(
        `Derived Traditional Chinese is outdated: ${path}; run node tooling/scripts/derive-traditional.mjs`
      );
    writeFileSync(path, text);
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href)
  generateTraditional(process.argv.includes('--check'));
