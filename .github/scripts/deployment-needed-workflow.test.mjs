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

// The job body runs to the next key at the same indent, or to the end of the file when it is the
// last job - which `deploy` is. Anchoring on `\n  [a-z]` alone would return nothing for a trailing
// job and make the assertion below vacuous, so the end of input is an explicit alternative.
function job(name) {
  const pattern = new RegExp(`\\n  ${name}:\\n([\\s\\S]*?)(?=\\n  [a-z][\\w-]*:\\n|$)`);
  return workflow.match(pattern)?.[1] ?? '';
}

test('the deploy job cannot run when the revision needs no deployment', () => {
  // images and admin skip on needed=false, and deploy inherits that only through GitHub's skip
  // propagation across `needs`. That is invisible in this file: an `if: always()` or a narrowed
  // `needs` would run the deploy against a revision that needs none, and nothing else would say so.
  const deploy = job('deploy');
  assert.ok(deploy, 'the deploy job was found');
  assert.match(deploy, /if:\s*needs\.source\.outputs\.needed\s*==\s*'true'/,
    'the deploy job states the needed gate itself');
});

test('the job extractor does not silently return another job or nothing', () => {
  // Guards the test above: a pattern that stopped matching would make its assertion pass on an
  // empty string. Each job must be found, contain its own first step, and not contain a sibling.
  const names = workflow.match(/^  ([a-z][\w-]*):$/gm).map(line => line.slice(2, -1));
  for (const name of names) {
    const body = job(name);
    assert.ok(body.trim(), `the ${name} job was found`);
    assert.doesNotMatch(body, /^  [a-z][\w-]*:$/m, `the ${name} job does not bleed into the next`);
  }
  assert.match(job('images'), /matrix\.name/, 'the extractor reaches the images job body');
  assert.match(job('admin'), /production-admin/, 'the extractor reaches the admin job body');
  assert.match(job('deploy'), /RELEASE_SHA/, 'the extractor reaches the deploy job body');
});
