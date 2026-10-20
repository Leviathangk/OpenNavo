import { describe, expect, it } from 'vitest';
import { applicationCategory, packageLink, packagePath } from '../app/utils/packageView';

describe('Detail URLs', () => {
  it('Cask-only details use /apps with tab subpaths', () => {
    expect(packagePath('visual-studio-code')).toBe('/apps/visual-studio-code');
    expect(packagePath('python@3.14', 'versions')).toBe('/apps/python@3.14/versions');
  });

  it('Mixed lists link Casks and link apps and never create /cli URLs', () => {
    expect(packageLink('cask', 'docker-desktop')).toBe('/apps/docker-desktop');
    expect(packageLink('formula', 'docker')).toBeUndefined();
    expect(packageLink('link', 'lumen-notes')).toBe('/apps/lumen-notes');
  });
});

describe('Structured data', () => {
  it('Map categories to applicationCategory, default UtilitiesApplication', () => {
    expect(applicationCategory('developer-tools')).toBe('DeveloperApplication');
    expect(applicationCategory('virtualization')).toBe('DeveloperApplication');
    expect(applicationCategory('media')).toBe('MultimediaApplication');
    expect(applicationCategory('education')).toBe('EducationalApplication');
    expect(applicationCategory('productivity')).toBe('BusinessApplication');
    expect(applicationCategory('browsers')).toBe('BrowserApplication');
    expect(applicationCategory('menu-bar')).toBe('DesktopEnhancementApplication');
    expect(applicationCategory('utilities')).toBe('UtilitiesApplication');
    // An unmapped primary falls through to the first mapped category of the app.
    expect(applicationCategory('ai', ['ai', 'developer-tools'])).toBe('DeveloperApplication');
    expect(applicationCategory(null, ['network'])).toBe('UtilitiesApplication');
    expect(applicationCategory(null)).toBe('UtilitiesApplication');
  });
});
