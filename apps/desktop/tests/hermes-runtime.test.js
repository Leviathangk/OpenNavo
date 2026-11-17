import { describe, expect, it } from 'vitest';
import {
  applyHermesRelease,
  assertRuntimeVersionOutput,
  normalizeHermesVersion,
  readRuntimeManifest,
  requireReleaseChecksum,
  runtimeReleaseUrl,
  runtimeSourceUrl,
  validateRuntimeManifest
} from '../scripts/hermes-runtime-lib.mjs';

describe('Hermes runtime manifest', () => {
  it('pins every versioned component and the release asset', () => {
    const manifest = readRuntimeManifest();
    expect(manifest.hermes.version).toBe('0.23.2');
    expect(manifest.hermes.commit).toBe('ae2a81ccc00e9b7c06301523e5bb09608397149c');
    expect(manifest.release.tag).toBe('hermes-runtime-v0.23.2');
    expect(manifest.release.asset).toBe('runtime-0.23.2-macos-arm64.tar.gz');
    expect(manifest.plugins).toContainEqual(
      expect.objectContaining({ name: 'github-copilot', ref: 'df8f1321941125d45dde71ab20196fbeea093191' })
    );
  });

  it('rejects a release tag that drifts from the Hermes version', () => {
    const manifest = structuredClone(readRuntimeManifest());
    manifest.release.tag = 'hermes-runtime-v0.23.0';
    expect(() => validateRuntimeManifest(manifest)).toThrow(/release\.tag/);
  });

  it('builds the pinned GitHub release URL', () => {
    expect(runtimeReleaseUrl(readRuntimeManifest())).toBe(
      'https://github.com/opennavo/OpenNavo/releases/download/hermes-runtime-v0.23.2/runtime-0.23.2-macos-arm64.tar.gz'
    );
  });

  it('downloads runtime source by commit rather than a movable tag', () => {
    expect(runtimeSourceUrl(readRuntimeManifest())).toBe(
      'https://github.com/NousResearch/hermes-agent/archive/ae2a81ccc00e9b7c06301523e5bb09608397149c.tar.gz'
    );
  });

  it('requires a packaged checksum before the app can download the runtime', () => {
    const manifest = structuredClone(readRuntimeManifest());
    manifest.release.sha256 = '';
    expect(() => requireReleaseChecksum(manifest)).toThrow(/hermes:package/);
    manifest.release.sha256 = 'a'.repeat(64);
    expect(requireReleaseChecksum(manifest)).toBe('a'.repeat(64));
  });

  it('only accepts the runtime-reported version, not a date or the host checkout', () => {
    expect(normalizeHermesVersion('Hermes Agent v0.23.1 · stable')).toBe('0.23.1');
    expect(normalizeHermesVersion('Hermes Agent vunknown (2026.9.24)')).toBeNull();
    const manifest = readRuntimeManifest();
    expect(() => assertRuntimeVersionOutput('Hermes Agent vunknown (2026.9.24)', manifest)).toThrow(
      /reports an unknown version/
    );
  });

  it('moves all release-derived fields together when bumping', () => {
    const next = applyHermesRelease(readRuntimeManifest(), {
      version: '0.24.0',
      tag: 'v0.24.0',
      commit: 'b'.repeat(40),
      commitTimestamp: 1800000000
    });
    expect(next.hermes.commit).toBe('b'.repeat(40));
    expect(next.release.tag).toBe('hermes-runtime-v0.24.0');
    expect(next.release.asset).toBe('runtime-0.24.0-macos-arm64.tar.gz');
    expect(next.release.sha256).toBe('');
  });
});
