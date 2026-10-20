import { describe, expect, it } from 'vitest';
import { uninstallPageOpen, uninstallPreviewLink } from '../app/utils/uninstallPage';

const decision = (robots: string, canonicalPath = '') => ({
  robots,
  canonicalPath,
  hreflang: ['en-US'],
  inSitemap: robots === 'index, follow',
  announce: false,
  reason: 'fixture'
});
const evidence = {
  sourceUrl: 'https://formulae.brew.sh/cask/example',
  checkedAt: '2026-10-01T00:00:00Z',
  validUntil: null,
  status: 'verified'
};
const pkg = (overrides: Record<string, unknown> = {}) =>
  ({
    kind: 'cask',
    facts: { uninstall: [{ kind: 'zap', phase: 'zap', value: '~/Library/Caches/example', evidence }] },
    indexing: { pages: { uninstall: decision('index, follow') } },
    ...overrides
  }) as unknown as Parameters<typeof uninstallPageOpen>[0];

describe('Uninstall intent page (X2)', () => {
  it('opens only where the decision indexes it with its own canonical and there is something to uninstall', () => {
    expect(uninstallPageOpen(pkg())).toBe(true);
    expect(uninstallPageOpen(pkg({ indexing: { pages: { uninstall: decision('noindex, follow') } } }))).toBe(false);
    expect(
      uninstallPageOpen(pkg({ indexing: { pages: { uninstall: decision('index, follow', '/apps/example') } } }))
    ).toBe(false);
    expect(uninstallPageOpen(pkg({ indexing: { pages: {} } }))).toBe(false);
    expect(uninstallPageOpen(pkg({ facts: { uninstall: [] } }))).toBe(false);
    // Link apps have no Homebrew uninstall steps to show.
    expect(uninstallPageOpen(pkg({ kind: 'link' }))).toBe(false);
  });

  it('links the client preview only for tokens the client accepts', () => {
    expect(uninstallPreviewLink('visual-studio-code')).toBe('opennavo://uninstall/visual-studio-code?preview=1');
    expect(uninstallPreviewLink('../etc')).toBeNull();
  });
});
