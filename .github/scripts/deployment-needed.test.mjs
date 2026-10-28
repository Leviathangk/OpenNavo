import assert from 'node:assert/strict';
import test from 'node:test';
import { deploymentNeeded } from './deployment-needed.mjs';
const base = 'a'.repeat(40);
const head = 'b'.repeat(40);
const success = {
  run_number: 1,
  status: 'completed',
  conclusion: 'success',
  display_title: `Deploy production ${base}`
};
test('manual desktop-only dispatch keeps web, build identity and release purge untouched', () => {
  assert.equal(
    deploymentNeeded([success], head, () => ['apps/desktop/src/main.ts', 'docs/desktop/release.md']),
    false
  );
  assert.equal(
    deploymentNeeded([success], head, () => ['apps/web/app/app.vue']),
    true
  );
  assert.equal(
    deploymentNeeded([success], head, () => ['apps/server/internal/seo/page.go']),
    true
  );
  assert.equal(
    deploymentNeeded([success], head, () => ['pnpm-lock.yaml']),
    true
  );
  assert.equal(
    deploymentNeeded([success], head, () => Array(1000).fill('apps/desktop/src/main.ts')),
    false
  );
});
test('first deploy runs, failed and ongoing deployments do not become the baseline', () => {
  assert.equal(
    deploymentNeeded([], head, () => {
      throw new Error('no baseline');
    }),
    true
  );
  assert.equal(
    deploymentNeeded([success], base, () => {
      throw new Error('same revision');
    }),
    false
  );
  const failed = {
    ...success,
    run_number: 2,
    conclusion: 'failure',
    display_title: `Deploy production ${head}`
  };
  assert.equal(
    deploymentNeeded([success, failed], head, (from, to) => {
      assert.equal(from, base);
      assert.equal(to, head);
      return ['apps/web/app/app.vue'];
    }),
    true
  );
});
