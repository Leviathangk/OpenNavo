import { describe, expect, it } from 'vitest';
import { InvalidTokenError, brewArgs, formatBrewCommand, installCommand, isValidToken } from '../src/commands';

describe('Accepts only allowlisted tokens', () => {
  it.each(['visual-studio-code', 'python@3.12', 'gtk+3', 'node', 'font-jetbrains-mono', 'a', '0ad'])(
    '%s 合法',
    token => {
      expect(isValidToken(token)).toBe(true);
    }
  );

  it.each(['', '-rf', 'Foo', 'a b', 'a;rm', 'a/b', '$(x)', '"x"', '.hidden', 'x'.repeat(129)])(
    'Rejects invalid %s',
    token => {
      expect(isValidToken(token)).toBe(false);
    }
  );
});

describe('Builds brew argument arrays', () => {
  it('Explicitly distinguishes casks and formulae', () => {
    expect(brewArgs({ op: 'install', kind: 'cask', token: 'ghostty' })).toEqual(['install', '--cask', 'ghostty']);
    expect(brewArgs({ op: 'install', kind: 'formula', token: 'uv' })).toEqual(['install', '--formula', 'uv']);
    expect(brewArgs({ op: 'reinstall', kind: 'formula', token: 'uv' })).toEqual(['reinstall', '--formula', 'uv']);
    expect(brewArgs({ op: 'pin', kind: 'cask', token: 'ghostty' })).toEqual(['pin', '--cask', 'ghostty']);
    expect(brewArgs({ op: 'unpin', kind: 'formula', token: 'uv' })).toEqual(['unpin', '--formula', 'uv']);
  });

  it('Uses greedy only for explicit cask upgrades', () => {
    expect(brewArgs({ op: 'upgrade', kind: 'cask', token: 'ghostty' })).toEqual([
      'upgrade',
      '--cask',
      '--greedy',
      'ghostty'
    ]);
    expect(brewArgs({ op: 'upgrade', kind: 'formula', token: 'uv' })).toEqual(['upgrade', '--formula', 'uv']);
  });

  it('Uses adopt and zap only for casks', () => {
    expect(brewArgs({ op: 'install', kind: 'cask', token: 'ghostty', adopt: true })).toEqual([
      'install',
      '--cask',
      '--adopt',
      'ghostty'
    ]);
    expect(brewArgs({ op: 'install', kind: 'formula', token: 'uv', adopt: true })).toEqual([
      'install',
      '--formula',
      'uv'
    ]);
    expect(brewArgs({ op: 'uninstall', kind: 'cask', token: 'ghostty', zap: true })).toEqual([
      'uninstall',
      '--cask',
      '--zap',
      'ghostty'
    ]);
    expect(brewArgs({ op: 'uninstall', kind: 'cask', token: 'ghostty' })).toEqual(['uninstall', '--cask', 'ghostty']);
    expect(brewArgs({ op: 'uninstall', kind: 'formula', token: 'uv', zap: true })).toEqual([
      'uninstall',
      '--formula',
      'uv'
    ]);
    expect(brewArgs({ op: 'uninstall', kind: 'cask', token: 'notion', zap: true, force: true })).toEqual([
      'uninstall',
      '--cask',
      '--zap',
      '--force',
      'notion'
    ]);
    expect(brewArgs({ op: 'uninstall', kind: 'formula', token: 'uv', force: true })).toEqual([
      'uninstall',
      '--formula',
      'uv'
    ]);
  });

  it('Rejects invalid tokens', () => {
    expect(() => brewArgs({ op: 'install', kind: 'cask', token: 'x; rm -rf ~' })).toThrow(InvalidTokenError);
  });
});

describe('Formats commands for display', () => {
  it('Follows server installation rules', () => {
    expect(installCommand('cask', 'visual-studio-code')).toBe('brew install --cask visual-studio-code');
    expect(installCommand('formula', 'wget')).toBe('brew install --formula wget');
    // Casks from a third-party tap install by their qualified name; official taps never qualify one.
    expect(installCommand('cask', 'swiftvm', 'swiftvm-app/tap')).toBe('brew install --cask swiftvm-app/tap/swiftvm');
    expect(installCommand('cask', 'firefox', 'homebrew/cask')).toBe('brew install --cask firefox');
    expect(installCommand('cask', 'firefox', null)).toBe('brew install --cask firefox');
    expect(installCommand('formula', 'wget', 'someone/tap')).toBe('brew install --formula wget');
    for (const tap of ['x', 'a/b/c', 'A/b', 'a/b;id', 'a/ b'])
      expect(() => installCommand('cask', 'swiftvm', tap)).toThrow(InvalidTokenError);
    expect(formatBrewCommand(['outdated', '--json=v2'])).toBe('brew outdated --json=v2');
  });
});
