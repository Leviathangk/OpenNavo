import assert from 'node:assert/strict';
import test from 'node:test';
import { classify, plan } from './changes.mjs';

const none = { server: false, mcp: false, frontend: false, desktop: false, e2e: false };

test('a server change runs server checks and full-stack E2E, not desktop builds', () => {
  assert.deepEqual(classify(['apps/server/internal/articles/evidence.go']), { ...none, server: true, e2e: true });
  assert.deepEqual(classify(['apps/server/api/admin.openapi.yaml']), { ...none, server: true, mcp: true, e2e: true });
});

test('shared packages reach the desktop only through frontend jobs', () => {
  assert.deepEqual(classify(['packages/ui/src/Button.vue']), { ...none, frontend: true, e2e: true });
  assert.deepEqual(classify(['apps/desktop/src-tauri/src/main.rs']), { ...none, frontend: true, desktop: true });
  assert.deepEqual(classify(['apps/web/app/pages/index.vue']), { ...none, frontend: true, e2e: true });
});

test('documentation changes need no build', () => {
  assert.deepEqual(classify(['docs/guide.md', 'README.md']), none);
  assert.deepEqual(classify(['docs/design/logo/source/mark.svg']), { ...none, frontend: true });
});

test('workspace files and workflow edits reach the areas they configure', () => {
  assert.deepEqual(classify(['pnpm-lock.yaml']), { ...none, server: true, frontend: true, e2e: true });
  assert.deepEqual(classify(['.github/workflows/ci-frontend.yml']), {
    ...none,
    frontend: true,
    desktop: true,
    e2e: true
  });
  assert.deepEqual(classify(['.github/workflows/ci-server.yml']), { ...none, server: true, mcp: true });
  assert.deepEqual(classify(['apps/mcp/src/opennavo_mcp/resources.py']), { ...none, mcp: true });
});

test('depth: pull requests are fast unless labelled, main and scheduled runs are full', () => {
  const files = ['apps/server/main.go'];
  assert.equal(plan('pull_request', { pull_request: { labels: [] } }, files).full, false);
  assert.equal(plan('pull_request', { pull_request: { labels: [{ name: 'full-ci' }] } }, files).full, true);
  assert.deepEqual(plan('push', {}, files), { ...none, server: true, e2e: true, full: true });
  assert.deepEqual(plan('schedule', {}, null), {
    server: true,
    mcp: true,
    frontend: true,
    desktop: true,
    e2e: true,
    full: true
  });
  assert.equal(
    plan('pull_request', { pull_request: { labels: [] } }, null).desktop,
    true,
    'unlisted changes run every area'
  );
});
