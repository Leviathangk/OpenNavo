import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  currentReleaseDate,
  DESCRIPTION_WIDTH,
  fitSummary,
  overviewDescription,
  overviewTitle,
  brandlessTitle,
  pagedDescription,
  pagedTitle,
  reliableVersion,
  textWidth,
  titleName,
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
      { ...app, disabled: true },
      { ...app, kind: 'link' },
      { ...app, kind: 'link', summary: null }
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
    // Link apps promise the developer's own download, not an install method (store/security plan §4).
    expect(overviewDescription({ ...app, kind: 'link' }, t)).toBe(
      'Open-source code editor. Find the official download for Visual Studio Code and its release history.'
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

describe('App names that already name the platform', () => {
  // [WeChat for Mac, 微信 Mac 版 (the Chinese catalog name), WeChat for Mac when Homebrew disabled it]
  const expected: Record<(typeof LOCALES)[number], [string, string, string]> = {
    'zh-CN': ['WeChat Mac 版下载安装 · OpenNavo', '微信 Mac 版下载安装 · OpenNavo', 'WeChat Mac 版 · OpenNavo'],
    'en-US': [
      'WeChat for Mac – Download & Install · OpenNavo',
      '微信 for Mac – Download & Install · OpenNavo',
      'WeChat for Mac · OpenNavo'
    ],
    'ja-JP': [
      'WeChat for Mac ダウンロード・インストール · OpenNavo',
      '微信 for Mac ダウンロード・インストール · OpenNavo',
      'WeChatの Mac 版 · OpenNavo'
    ],
    'es-ES': [
      'Descargar WeChat para Mac · OpenNavo',
      'Descargar 微信 para Mac · OpenNavo',
      'WeChat para Mac · OpenNavo'
    ],
    'pt-BR': ['Baixar WeChat para Mac · OpenNavo', 'Baixar 微信 para Mac · OpenNavo', 'WeChat para Mac · OpenNavo'],
    'ru-RU': [
      'WeChat для Mac — скачать и установить · OpenNavo',
      '微信 для Mac — скачать и установить · OpenNavo',
      'WeChat для компьютеров Mac · OpenNavo'
    ]
  };

  it.each(LOCALES)('%s names the platform once', code => {
    const t = translator(code);
    const [latin, chinese, unavailable] = expected[code];
    expect(overviewTitle({ ...app, displayName: 'WeChat for Mac' }, t)).toBe(latin);
    expect(overviewTitle({ ...app, displayName: '微信 Mac 版' }, t)).toBe(chinese);
    expect(overviewTitle({ ...app, displayName: 'WeChat for Mac', disabled: true }, t)).toBe(unavailable);
    for (const name of ['WeChat for macOS', 'WeChat for Mac OS X', 'WeChat para Mac', 'WeChat для Mac', 'WeChat Mac版'])
      expect(overviewTitle({ ...app, displayName: name }, t), name).toBe(latin);
    // Names that only contain or start with Mac keep every word.
    for (const name of ['Mac Mouse Fix', 'iMac版', 'CleanMyMac X', 'Helper for Mac Pro'])
      expect(overviewTitle({ ...app, displayName: name }, t), name).toContain(name);
  });

  it('Takes only a trailing platform phrase off the name', () => {
    expect(titleName('WeChat for Mac')).toBe('WeChat');
    expect(titleName('Paragon NTFS for Mac')).toBe('Paragon NTFS');
    expect(titleName('微信 Mac 版')).toBe('微信');
    expect(titleName('微信Mac版')).toBe('微信');
    expect(titleName('Foo for OS X')).toBe('Foo');
    expect(titleName('Foo для macOS')).toBe('Foo');
    expect(titleName('  WeChat for Mac  ')).toBe('WeChat');
    for (const name of ['Mac Mouse Fix', 'iMac版', 'CleanMyMac X', 'Mac 版', 'for Mac', 'Macintosh for Macs'])
      expect(titleName(name), name).toBe(name);
    // Fonts are named once as well.
    expect(overviewTitle({ ...app, displayName: 'SF Mono for Mac', isFont: true }, translator('en-US'))).toBe(
      'SF Mono Font for Mac – Download & Install · OpenNavo'
    );
  });
});

describe('Descriptions that fit search results', () => {
  // The summaries the audit found on /apps/wechat in each language (2026-10-17).
  const wechat: Record<(typeof LOCALES)[number], [string, string]> = {
    'zh-CN': [
      '微信 Mac 版',
      '腾讯的 Mac 即时通信客户端，可与 WeChat 联系人收发文字消息和文件，以及进行语音和视频通话。'
    ],
    'en-US': [
      'WeChat for Mac',
      'Tencent’s Mac messaging client for text, files, voice and video calls with your WeChat contacts.'
    ],
    'ja-JP': [
      'WeChat for Mac',
      'TencentのMac向けメッセージアプリです。WeChatの連絡先とテキストやファイルをやり取りしたり、音声・ビデオ通話をしたりできます。'
    ],
    'es-ES': [
      'WeChat for Mac',
      'Cliente de mensajería de Tencent para Mac para enviar mensajes de texto y archivos y hacer llamadas de voz y vídeo con tus contactos de WeChat.'
    ],
    'pt-BR': [
      'WeChat for Mac',
      'Cliente de mensagens da Tencent para Mac para enviar textos e arquivos e fazer chamadas de voz e vídeo com seus contatos do WeChat.'
    ],
    'ru-RU': [
      'WeChat for Mac',
      'Мессенджер Tencent для Mac для обмена текстовыми сообщениями и файлами, голосовых и видеозвонков с вашими контактами WeChat.'
    ]
  };

  it.each(LOCALES)('%s shortens the summary and keeps the closing sentence whole', code => {
    const t = translator(code);
    const [displayName, summary] = wechat[code];
    for (const pkg of [
      { ...app, displayName, summary },
      { ...app, displayName, summary, disabled: true }
    ]) {
      const description = overviewDescription(pkg, t);
      const closing = overviewDescription({ ...pkg, summary: null }, t);
      expect(textWidth(description), description).toBeLessThanOrEqual(DESCRIPTION_WIDTH);
      expect(description.endsWith(closing), description).toBe(true);
      // The summary still says what the app is.
      expect(description.length - closing.length, description).toBeGreaterThan(20);
      expect(description).not.toMatch(/…\.|\p{Co}|[{}]/u);
    }
  });

  it('Cuts where the summary reads complete, else between words with an ellipsis', () => {
    const t = translator('en-US');
    expect(overviewDescription({ ...app, displayName: 'WeChat for Mac', summary: wechat['en-US'][1] }, t)).toBe(
      'Tencent’s Mac messaging client for text, files, voice… See how to install WeChat for Mac on your Mac, along with its system requirements and release history.'
    );
    expect(
      overviewDescription({ ...app, displayName: 'WeChat for Mac', summary: wechat['ja-JP'][1] }, translator('ja-JP'))
    ).toBe(
      'TencentのMac向けメッセージアプリです。WeChat for Macを Mac にインストールする方法、システム要件、バージョン履歴を確認できます。'
    );
    // Short enough already: unchanged.
    expect(overviewDescription(app, t)).toBe(
      'Open-source code editor. See how to install Visual Studio Code on your Mac, along with its system requirements and release history.'
    );
    const english = 'Tencent’s Mac messaging client for text, files, voice and video calls with your WeChat contacts';
    expect(fitSummary(english, 200)).toBe(english);
    // Latin commas separate list items, so no clause ends at them; a dangling "with" goes too.
    expect(fitSummary(english, 76)).toBe('Tencent’s Mac messaging client for text, files, voice and video calls…');
    expect(fitSummary(english, 57)).toBe('Tencent’s Mac messaging client for text, files, voice…');
    expect(
      fitSummary('Converts documents, e.g. PDF and EPUB files, into plain text for every reader you use', 50)
    ).toBe('Converts documents, e.g. PDF and EPUB files…');
    expect(
      fitSummary('A fast, extensible code editor for every language — with plugins, themes and a terminal', 60)
    ).toBe('A fast, extensible code editor for every language');
    // A clause that fills less than half the room loses to a longer cut.
    expect(
      fitSummary('A fast editor; with plugins, themes, language servers and a terminal for every project', 60)
    ).toBe('A fast editor; with plugins, themes, language servers…');
    expect(
      fitSummary('腾讯的 Mac 即时通信客户端，可与 WeChat 联系人收发文字消息和文件，以及进行语音和视频通话', 80)
    ).toBe('腾讯的 Mac 即时通信客户端，可与 WeChat 联系人收发文字消息和文件');
    expect(fitSummary('这是一段没有任何标点符号的很长很长的中文摘要用来测试按字符截断', 40)).toBe(
      '这是一段没有任何标点符号的很长很长的中…'
    );
    expect(fitSummary('Anything longer than the little room it gets', 20)).toBe('');
    expect(textWidth('Mac 微信，アプリ')).toBe(4 + 4 + 2 + 6);
  });

  it('Joins Chinese names to the Chinese text beside them and spaces Latin ones', () => {
    const zh = translator('zh-CN');
    const chinese = { ...app, displayName: '微信 Mac 版', summary: null };
    expect(overviewDescription(chinese, zh)).toBe('查看微信 Mac 版在 Mac 上的安装方法、系统要求和版本记录。');
    expect(overviewDescription({ ...chinese, disabled: true }, zh)).toBe(
      '微信 Mac 版已被 Homebrew 停用，可以查看版本记录和同类软件。'
    );
    expect(versionsTitle({ displayName: '网易云音乐', version: '3.1.2' }, zh)).toBe(
      '网易云音乐更新日志（最新 3.1.2）· OpenNavo'
    );
    expect(versionsDescription({ displayName: '网易云音乐', version: 'latest' }, zh, iso => iso)).toBe(
      '查看网易云音乐每个版本更新了什么。'
    );
    expect(versionsTitle({ displayName: 'QQ音乐', version: '10.0' }, zh)).toBe('QQ音乐更新日志（最新 10.0）· OpenNavo');
    expect(versionsTitle({ displayName: 'Visual Studio Code', version: '1.95.3' }, zh)).toBe(
      'Visual Studio Code 更新日志（最新 1.95.3）· OpenNavo'
    );
    // Japanese templates already attach the name without a space, for either script.
    const ja = translator('ja-JP');
    expect(overviewDescription({ ...app, displayName: 'WeChat for Mac', summary: null }, ja)).toBe(
      'WeChat for Macを Mac にインストールする方法、システム要件、バージョン履歴を確認できます。'
    );
    expect(overviewDescription({ ...app, displayName: '微信', summary: null }, ja)).toBe(
      '微信を Mac にインストールする方法、システム要件、バージョン履歴を確認できます。'
    );
  });
});
