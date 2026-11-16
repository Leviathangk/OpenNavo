import { describe, expect, it } from 'vitest';
import { buildDeepLink, parseDeepLink } from '../src/deeplink';

describe('buildDeepLink', () => {
  it('Follows the deep-link specification', () => {
    expect(buildDeepLink({ type: 'package', kind: 'cask', token: 'visual-studio-code' })).toBe(
      'opennavo://package/cask/visual-studio-code'
    );
    expect(buildDeepLink({ type: 'package', kind: 'formula', token: 'python@3.12', action: 'install' })).toBe(
      'opennavo://package/formula/python%403.12?action=install'
    );
    expect(buildDeepLink({ type: 'collection', slug: 'new-mac' })).toBe('opennavo://collection/new-mac');
    expect(buildDeepLink({ type: 'collection', slug: 'new-mac', action: 'install' })).toBe(
      'opennavo://collection/new-mac?action=install'
    );
    expect(buildDeepLink({ type: 'search', q: '微信 & vscode' })).toBe(
      'opennavo://search?q=%E5%BE%AE%E4%BF%A1%20%26%20vscode'
    );
    expect(buildDeepLink({ type: 'updates' })).toBe('opennavo://updates');
    expect(buildDeepLink({ type: 'route', route: 'installed' })).toBe('opennavo://installed');
    expect(buildDeepLink({ type: 'route', route: 'monitor' })).toBe('opennavo://monitor');
  });

  it('Limits query values to 64 characters', () => {
    const url = buildDeepLink({ type: 'search', q: '字'.repeat(80) });
    expect(parseDeepLink(url)).toEqual({ type: 'search', q: '字'.repeat(64) });
  });

  it('Rejects invalid arguments', () => {
    expect(() => buildDeepLink({ type: 'package', kind: 'cask', token: '../etc' })).toThrow();
    expect(() => buildDeepLink({ type: 'package', kind: 'tap' as 'cask', token: 'x' })).toThrow();
    expect(() => buildDeepLink({ type: 'collection', slug: 'Bad Slug' })).toThrow();
    expect(() => buildDeepLink({ type: 'route', route: 'settings' as never })).toThrow();
  });
});

describe('parseDeepLink', () => {
  it('Accepts valid installation links only', () => {
    expect(parseDeepLink('opennavo://package/cask/visual-studio-code')).toEqual({
      type: 'package',
      kind: 'cask',
      token: 'visual-studio-code'
    });
    expect(parseDeepLink(' OPENNAVO://Package/formula/python%403.12?action=install ')).toEqual({
      type: 'package',
      kind: 'formula',
      token: 'python@3.12',
      action: 'install'
    });
    expect(parseDeepLink('opennavo://package/cask/ghostty?action=uninstall')).toEqual({
      type: 'package',
      kind: 'cask',
      token: 'ghostty'
    });
    expect(parseDeepLink('opennavo://collection/new-mac?action=install')).toEqual({
      type: 'collection',
      slug: 'new-mac',
      action: 'install'
    });
    expect(parseDeepLink('opennavo://search?q=%20rg%20')).toEqual({ type: 'search', q: 'rg' });
    expect(parseDeepLink('opennavo://updates')).toEqual({ type: 'updates' });
    expect(parseDeepLink('opennavo://installed')).toEqual({ type: 'route', route: 'installed' });
    expect(parseDeepLink('opennavo://cleanup')).toEqual({ type: 'route', route: 'cleanup' });
    expect(parseDeepLink('opennavo://monitor')).toEqual({ type: 'route', route: 'monitor' });
    expect(parseDeepLink('opennavo://security')).toEqual({ type: 'route', route: 'security' });
    expect(parseDeepLink('opennavo://firewall')).toEqual({ type: 'route', route: 'firewall' });
  });

  it.each([
    'not a url',
    'https://package/cask/x',
    'opennavo:package/cask/x',
    'opennavo://package/tap/x',
    'opennavo://package/cask',
    'opennavo://package/cask/x/y',
    'opennavo://package/cask/Bad%20Token',
    'opennavo://package/cask/%E0%A4%A',
    'opennavo://collection/Bad_Slug',
    'opennavo://collection',
    'opennavo://search',
    'opennavo://search/extra?q=x',
    'opennavo://updates/now',
    'opennavo://installed/now',
    'opennavo://monitor?tab=cpu',
    'opennavo://cleanup?action=install',
    'opennavo://settings'
  ])('%s → null', url => {
    expect(parseDeepLink(url)).toBeNull();
  });
});
