import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const workflow = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), '../workflows/deploy-production.yml'),
  'utf8'
);

test('manual deploy runs deployment-needed from the workflow commit, not only the target revision', () => {
  assert.match(workflow, /ref:\s*\$\{\{\s*github\.sha\s*\}\}/);
  assert.match(workflow, /\.workflow-ci\/\.github\/scripts\/deployment-needed\.mjs/);
  assert.match(workflow, /sparse-checkout:[\s\S]*deployment-needed\.mjs/);
});
