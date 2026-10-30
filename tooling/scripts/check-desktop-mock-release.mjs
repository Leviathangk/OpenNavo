import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const root = resolve(new URL('../..', import.meta.url).pathname);
const dist = join(root, 'apps/desktop/dist');
const forbidden = 'deeplink-approve';

if (!existsSync(dist)) {
  console.error(`Missing desktop build output: ${dist}`);
  process.exit(1);
}

function files(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? files(path) : [path];
  });
}

const matches = files(dist).filter(path => readFileSync(path).includes(forbidden));
if (matches.length) {
  console.error(`Debug-only deep-link mock switch leaked into the release bundle: ${matches.join(', ')}`);
  process.exit(1);
}
console.log('Desktop release bundle contains no debug-only deep-link mock approve switch.');
