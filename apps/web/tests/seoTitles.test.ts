import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  currentReleaseDate,
  overviewDescription,
  overviewTitle,
  brandlessTitle,
  pagedDescription,
  pagedTitle,
  reliableVersion,
  usableSummary,
  versionsDescription,
  versionsTitle,
  type Translate
} from '../app/utils/seoTitles';

const LOCALES = ['zh-CN', 'en-US', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] as const;

// Real messages with vue-i18n style {name} interpolation, so every template is exercised in all six languages.
function translator(code: (typeof LOCALES)[number]): Translate {
  const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
  return (key, named = {}) => {
    const template = key.split('.').reduce((node, part) => node?.[part], messages);
    if (typeof template !== 'string') throw new Error(`missing ${code} ${key}`);
    return template.replace(/\{(\w+)\}/g, (_, name: string) => {
      if (!(name in named)) throw new Error(`unfilled {${name}} in ${code} ${key}`);
      return String(named[name]);
    });
  };
}

const app = { displayName: 'Visual Studio Code', summary: 'Open-source code editor.', isFont: false, disabled: false };
const longName = 'Microsoft Remote Desktop Beta for Enterprise Administrators (Legacy Edition)';

describe('WP1 titles and descriptions', () => {
  it.each(LOCALES)('%s keeps the full name, the brand and no unfilled variables', code => {
    const t = translator(code);
    for (const pkg of [
      app,
      { ...app, displayName: longName, summary: null },
      { ...app, displayName: 'Fira Code', isFont: true },
      { ...app, disabled: true }
    ]) {
      const title = overviewTitle(pkg, t);
      const description = overviewDescription(pkg, t);
      expect(title).toContain(pkg.displayName);
      expect(title.endsWith(' · OpenNavo')).toBe(true);
      expect(`${title} ${description}`).not.toMatch(/[{}]|brew install|undefined|null/);
      expect(description).toContain(pkg.displayName);
    }
    expect(versionsTitle({ displayName: 'Visual Studio Code', version: '1.95.3,abc' }, t)).toContain('1.95.3');
    expect(versionsTitle({ displayName: 'Visual Studio Code', version: 'latest' }, t)).not.toContain('latest');
    expect(t('categories.seo.itemTitle', { name: 'Browsers' })).toContain('Browsers');
  });

  it('Claims a download only while Homebrew can install the package', () => {
    const t = translator('en-US');
    expect(overviewTitle(app, t)).toBe('Visual Studio Code for Mac – Download & Install · OpenNavo');
    expect(overviewTitle({ ...app, disabled: true }, t)).toBe('Visual Studio Code for Mac · OpenNavo');
    expect(overviewTitle({ ...app, displayName: 'Fira Code', isFont: true }, t)).toBe(
      'Fira Code Font for Mac – Download & Install · OpenNavo'
    );
    // A name that already says "Font" does not get a second one.
    expect(overviewTitle({ ...app, displayName: 'FiraCode Nerd Font', isFont: true }, t)).toBe(
      'FiraCode Nerd Font for Mac – Download & Install · OpenNavo'
    );
    expect(overviewDescription({ ...app, disabled: true }, t)).toBe(
      'Open-source code editor. Homebrew has disabled Visual Studio Code; see its release history and similar apps.'
    );
    for (const text of [overviewTitle(app, t), t('categories.seo.itemTitle', { name: 'Browsers' })])
      expect(text).not.toMatch(/\b(best|free|20\d\d)\b/i);
  });

  it('Drops summaries that only repeat the name and trailing punctuation', () => {
    expect(usableSummary('Visual Studio Code', 'visual studio code')).toBe('');
    expect(usableSummary('代码编辑器。', 'VS Code')).toBe('代码编辑器');
    expect(usableSummary(null, 'VS Code')).toBe('');
    expect(overviewDescription({ ...app, summary: 'Visual Studio Code' }, translator('zh-CN'))).toBe(
      '查看 Visual Studio Code 在 Mac 上的安装方法、系统要求和版本记录。'
    );
  });

  it('Dates versions only from release data for the current version', () => {
    const t = translator('en-US');
    const format = (iso: string) => iso.slice(0, 10);
    const pkg = {
      displayName: 'The Unarchiver',
      version: '4.3.9',
      latestRelease: { version: '4.3.9', publishedAt: null, brewCommittedAt: '2025-03-18T10:00:00Z' }
    };
    expect(currentReleaseDate(pkg)).toBe('2025-03-18T10:00:00Z');
    expect(versionsDescription(pkg, t, format)).toBe(
      'The Unarchiver latest version 4.3.9 (2025-03-18). See what changed in every release.'
    );
    const stale = { ...pkg, latestRelease: { ...pkg.latestRelease, version: '4.3.8' } };
    expect(currentReleaseDate(stale)).toBeUndefined();
    expect(versionsDescription(stale, t, format)).toBe(
      'The Unarchiver latest version 4.3.9. See what changed in every release.'
    );
    expect(versionsDescription({ displayName: 'Nightly', version: 'latest' }, t, format)).toBe(
      'See what changed in every Nightly release.'
    );
    expect(reliableVersion('latest')).toBe('');
    expect(reliableVersion('2.0,1234')).toBe('2.0');
  });

  it('Numbers paginated titles before the brand', () => {
    expect(pagedTitle('Mac Apps · OpenNavo', 1, 'Page 1')).toBe('Mac Apps · OpenNavo');
    expect(pagedTitle('Mac Apps · OpenNavo', undefined, 'Page 1')).toBe('Mac Apps · OpenNavo');
    expect(pagedTitle('Mac Apps · OpenNavo', 3, 'Page 3')).toBe('Mac Apps · Page 3 · OpenNavo');
    expect(pagedTitle('Untitled', 2, 'Page 2')).toBe('Untitled · Page 2');
  });

  it('Leads later pages’ descriptions with the page number', () => {
    const description = 'macOS apps in Homebrew.';
    expect(pagedDescription(description, undefined, 'Page 1')).toBe(description);
    expect(pagedDescription(description, 1, 'Page 1')).toBe(description);
    expect(pagedDescription(description, 2, 'Page 2')).toBe('Page 2 · macOS apps in Homebrew.');
    expect(pagedDescription(undefined, 2, 'Page 2')).toBeUndefined();
    expect(pagedDescription(description, 4, translator('zh-CN')('catalog.pageTitle', { page: 4 }))).toBe(
      '第 4 页 · macOS apps in Homebrew.'
    );
  });

  it('Share images drop the brand suffix the card already shows', () => {
    expect(brandlessTitle('Mac App Collections · OpenNavo')).toBe('Mac App Collections');
    expect(brandlessTitle('Untitled')).toBe('Untitled');
    for (const code of LOCALES)
      for (const key of [
        'catalog.appsMetaTitle',
        'rankings.metaTitle',
        'recent.metaTitle',
        'categories.metaTitle',
        'collections.metaTitle'
      ]) {
        const title = translator(code)(key);
        expect(brandlessTitle(title), `${code} ${key}`).not.toContain('OpenNavo');
      }
  });
});
