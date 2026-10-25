import { describe, expect, it } from 'vitest';
import { BRAND } from '@opennavo/shared';
import { applicationCategory, brandDownloadPath, packageLink, packagePath } from '../app/utils/packageView';

describe('Detail URLs', () => {
  it('Cask-only details use /apps with tab subpaths', () => {
    expect(packagePath('visual-studio-code')).toBe('/apps/visual-studio-code');
    expect(packagePath('python@3.14', 'versions')).toBe('/apps/python@3.14/versions');
  });

  it('OpenNavo’s own entry links to /download; its tabs keep their paths', () => {
    expect(packagePath(BRAND.caskToken)).toBe('/download');
    expect(packageLink('cask', BRAND.caskToken)).toBe('/download');
    expect(packagePath(BRAND.caskToken, 'versions')).toBe(`/apps/${BRAND.caskToken}/versions`);
    expect(packagePath(BRAND.caskToken, 'uninstall')).toBe(`/apps/${BRAND.caskToken}/uninstall`);
    expect(packagePath(`${BRAND.caskToken}-helper`)).toBe(`/apps/${BRAND.caskToken}-helper`);
  });

  it('OpenNavo’s overview in any language goes to that language’s /download, and no other path does', () => {
    expect(brandDownloadPath(`/apps/${BRAND.caskToken}`)).toBe('/download');
    expect(brandDownloadPath(`/zh/apps/${BRAND.caskToken}`)).toBe('/zh/download');
    expect(brandDownloadPath(`/zh-hant/apps/${BRAND.caskToken}`)).toBe('/zh-hant/download');
    for (const path of [
      `/apps/${BRAND.caskToken}/versions`,
      `/apps/${BRAND.caskToken}-helper`,
      `/en/apps/${BRAND.caskToken}`,
      `/docs/apps/${BRAND.caskToken}`,
      `/xapps/${BRAND.caskToken}`,
      '/apps/visual-studio-code',
      '/download'
    ])
      expect(brandDownloadPath(path), path).toBeUndefined();
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
