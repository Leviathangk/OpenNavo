import { mkdtempSync, mkdirSync, rmSync, writeFileSync, renameSync, utimesSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { buildFingerprint, fingerprintInputs } from '../scripts/build-fingerprint';

const roots: string[] = [];
afterEach(() => roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true })));

function fixture() {
  const root = mkdtempSync(join(tmpdir(), 'onv-fingerprint-'));
  roots.push(root);
  const write = (path: string, value = path) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), value);
  };
  for (const path of [
    'package.json',
    'pnpm-lock.yaml',
    'pnpm-workspace.yaml',
    '.dockerignore',
    'tooling/config/tsconfig.base.json',
    'apps/web/Dockerfile',
    'apps/web/app/page.vue',
    'apps/web/i18n/locales/en-US.json',
    'apps/web/public/icon.svg',
    'apps/web/scripts/build-fingerprint.ts',
    'packages/api/src/index.ts',
    'packages/shared/src/robots.ts',
    'packages/tokens/dist/tokens.css',
    'packages/ui/src/index.ts',
    'patches/dependency.patch'
  ])
    write(path);
  return { root, write };
}

describe('web production build fingerprint', () => {
  it('ignores checkout location, timestamps, tests and generated output', () => {
    const a = fixture();
    const b = fixture();
    const expected = buildFingerprint(a.root);
    expect(buildFingerprint(b.root)).toBe(expected);
    for (const path of fingerprintInputs(a.root)) utimesSync(join(a.root, path), 1, 1);
    for (const path of [
      'apps/web/tests/ssr-seo.test.mjs',
      'apps/web/app/page.test.ts',
      'packages/shared/tests/robots.test.ts',
      'packages/ui/src/button.spec.ts',
      'apps/web/.nuxt/manifest.json',
      'apps/web/.output/server/index.mjs',
      'apps/web/node_modules/nuxt/index.mjs',
      'apps/web/node-compile-cache/v24/example',
      'packages/api/dist/client.js',
      'apps/web/.env',
      'apps/web/.env.local',
      'apps/web/coverage/report.json',
      'apps/web/build.log',
      'apps/web/.DS_Store',
      'apps/server/main.go',
      'apps/desktop/src/main.ts',
      'apps/admin/src/main.ts',
      'docs/design/notes.md'
    ])
      a.write(path, 'unrelated change');
    expect(buildFingerprint(a.root)).toBe(expected);
  });

  it('invalidates for every input class, including lockfile, Docker image, assets and token dist', () => {
    const { root, write } = fixture();
    for (const path of fingerprintInputs(root)) {
      const previous = buildFingerprint(root);
      write(path, `${path}: changed`);
      expect(buildFingerprint(root), path).not.toBe(previous);
    }
  });

  it('tracks additions, deletions and renames, even when bytes do not change', () => {
    const { root, write } = fixture();
    const before = buildFingerprint(root);
    write('apps/web/public/new.svg', 'asset');
    const added = buildFingerprint(root);
    expect(added).not.toBe(before);
    renameSync(join(root, 'apps/web/public/new.svg'), join(root, 'apps/web/public/renamed.svg'));
    expect(buildFingerprint(root)).not.toBe(added);
    rmSync(join(root, 'apps/web/public/renamed.svg'));
    expect(buildFingerprint(root)).toBe(before);
  });
});
