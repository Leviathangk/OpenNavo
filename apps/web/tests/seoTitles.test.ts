import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { SITE_LOCALES, formatList, selectPlural } from '@opennavo/shared';
import {
  categoryDescription,
  currentReleaseDate,
  dependenciesDescription,
  detailsDescription,
  DESCRIPTION_WIDTH,
  fitSummary,
  overrideDescription,
  overrideTitle,
  overviewDescription as describeOverview,
  type OverviewFacts,
  overviewTitle,
  brandlessTitle,
  pagedDescription,
  pagedTitle,
  reliableVersion,
  textWidth,
  titleName,
  usableSummary,
  variantLabel,
  versionsDescription,
  versionsTitle,
  withVariantName,
  withVariantNames,
  type Translate
} from '../app/utils/seoTitles';
import { evidence } from './packageFixtures';
import type { PriceFact } from '../app/utils/packageFacts';

const LOCALES = SITE_LOCALES;

// Real messages with vue-i18n style {name} interpolation and plural forms, so every template is exercised
// in every site language.
function translator(code: (typeof LOCALES)[number]) {
  const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
  const fallback = JSON.parse(readFileSync(new URL('../i18n/locales/en-US.json', import.meta.url), 'utf8'));
  const t: Translate = (key, named = {}, plural) => {
    const found =
      key.split('.').reduce((node, part) => node?.[part], messages) ??
      key.split('.').reduce((node, part) => node?.[part], fallback);
    if (typeof found !== 'string') throw new Error(`missing ${code} ${key}`);
    const template = plural === undefined ? found : selectPlural(found, plural, code);
    return template.replace(/\{(\w+)\}/g, (_, name: string) => {
      if (!(name in named)) throw new Error(`unfilled {${name}} in ${code} ${key}`);
      return String(named[name]);
    });
  };
  return Object.assign(t, { locale: code });
}

function overviewDescription(pkg: OverviewFacts, t: ReturnType<typeof translator>) {
  return describeOverview(pkg, t, values => formatList(values, { locale: t.locale }));
}

const app: OverviewFacts = {
  kind: 'cask',
  displayName: 'Visual Studio Code',
  summary: 'Open-source code editor.',
  isFont: false,
  disabled: false,
  version: '1.95.3',
  facts: {
    requirements: [],
    uninstall: [],
    alternatives: [],
    prices: [],
    overview: {
      version: '1.95.3',
      downloadUrl: null,
      sha256: null,
      caveats: null,
      artifacts: { apps: [], pkgs: [], binaries: [], uninstallNotes: [] },
      aliases: [],
      dependencyCount: 0,
      conflictCount: 0
    }
  }
};
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
      if (!pkg.summary || pkg.disabled) expect(description).toContain(pkg.displayName);
      // The whole summary leads; the section sentence follows only when it fits after it.
      else expect(description.startsWith(usableSummary(pkg.summary, pkg.displayName)), description).toBe(true);
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
    // Paused only by the 90-day rule (or deprecated): Homebrew still installs it, so the page says so.
    const disabledHere = { ...app, disabled: true, homebrewStatus: { state: 'disabled' } };
    expect(overviewTitle(disabledHere, t)).toBe('Visual Studio Code for Mac · OpenNavo');
    for (const state of ['active', 'deprecated']) {
      const paused = { ...app, disabled: true, homebrewStatus: { state } };
      expect(overviewTitle(paused, t), state).toBe(overviewTitle(app, t));
      expect(overviewDescription(paused, t), state).toBe(overviewDescription(app, t));
    }
    for (const text of [overviewTitle(app, t), t('categories.seo.itemTitle', { name: 'Browsers' })])
      expect(text).not.toMatch(/\b(best|free|20\d\d)\b/i);
  });

  it('Drops summaries that only repeat the name and trailing punctuation', () => {
    expect(usableSummary('Visual Studio Code', 'visual studio code')).toBe('');
    expect(usableSummary('代码编辑器。', 'VS Code')).toBe('代码编辑器');
    expect(usableSummary(null, 'VS Code')).toBe('');
    expect(overviewDescription({ ...app, summary: 'Visual Studio Code' }, translator('zh-CN'))).toBe(
      '查看 Visual Studio Code 在 Mac 上的安装方法和卸载方法。'
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
    ],
    'de-DE': [
      'WeChat für Mac – Download & Installation · OpenNavo',
      '微信 für Mac – Download & Installation · OpenNavo',
      'WeChat für Mac · OpenNavo'
    ],
    'fr-FR': [
      'WeChat pour Mac – Télécharger et installer · OpenNavo',
      '微信 pour Mac – Télécharger et installer · OpenNavo',
      'WeChat pour Mac · OpenNavo'
    ],
    'it-IT': [
      'WeChat per Mac – Scarica e installa · OpenNavo',
      '微信 per Mac – Scarica e installa · OpenNavo',
      'WeChat per Mac · OpenNavo'
    ],
    'zh-Hant': ['WeChat Mac 版下載安裝 · OpenNavo', '微信 Mac 版下載安裝 · OpenNavo', 'WeChat Mac 版 · OpenNavo']
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
    expect(titleName('Paragon NTFS für Mac')).toBe('Paragon NTFS');
    expect(titleName('Foo pour Mac')).toBe('Foo');
    expect(titleName('Foo per macOS')).toBe('Foo');
    expect(titleName('  WeChat for Mac  ')).toBe('WeChat');
    for (const name of ['Mac Mouse Fix', 'iMac版', 'CleanMyMac X', 'Mac 版', 'for Mac', 'Macintosh for Macs'])
      expect(titleName(name), name).toBe(name);
    // Fonts are named once as well.
    expect(overviewTitle({ ...app, displayName: 'SF Mono for Mac', isFont: true }, translator('en-US'))).toBe(
      'SF Mono Font for Mac – Download & Install · OpenNavo'
    );
  });
});

describe('Variant casks', () => {
  const named = (token: string, displayName: string) => withVariantName({ token, displayName }).displayName;

  it('names the variant a plain name leaves out', () => {
    expect(named('aerial@beta', 'Aerial')).toBe('Aerial Beta');
    expect(named('parallels@19', 'Parallels Desktop')).toBe('Parallels Desktop 19');
    expect(named('cloudflare-warp@beta', 'Cloudflare WARP')).toBe('Cloudflare WARP Beta');
    for (const [suffix, label] of [
      ['nightly', 'Nightly'],
      ['preview', 'Preview'],
      ['dev', 'Dev'],
      ['canary', 'Canary']
    ])
      expect(named(`hyper@${suffix}`, 'Hyper')).toBe(`Hyper ${label}`);
    // Other suffixes stay as the token spells them.
    expect(named('vscodium@insiders', 'VSCodium')).toBe('VSCodium insiders');
    expect(named('burp-suite@early-adopter', 'Burp Suite')).toBe('Burp Suite early-adopter');
    expect(variantLabel('aerial@beta')).toBe('Beta');
    expect(variantLabel('parallels@19')).toBe('19');
    expect(variantLabel('aerial')).toBe('');
  });

  it('keeps names that already say the variant, and every other token', () => {
    for (const [token, name] of [
      ['versioned-app@11', 'Example App @11'],
      ['firefox@nightly', 'Firefox Nightly'],
      ['kaleidoscope@2', 'Kaleidoscope v2'],
      ['firefox@developer-edition', 'Mozilla Firefox Developer Edition'],
      ['android-studio-preview@canary', 'Android Studio Preview (Canary)'],
      ['kitty@nightly', 'kitty-nightly'],
      ['picoscope@beta', 'PicoScope beta'],
      // Chinese and Japanese write Latin words without a space.
      ['wechat@beta', '微信Beta版'],
      ['aerial', 'Aerial'],
      ['visual-studio-code', 'Visual Studio Code']
    ]) {
      const detail = { token, displayName: name, version: '1.0' };
      expect(withVariantName(detail), `${token} ${name}`).toBe(detail);
    }
  });

  it('matches whole words only and goes before a platform phrase', () => {
    // The 1 of 1Password and the Beta of Betamax are not the variant.
    expect(named('1password-cli@1', '1Password CLI')).toBe('1Password CLI 1');
    expect(named('betamax@beta', 'Betamax')).toBe('Betamax Beta');
    expect(named('telegram@beta', 'Telegram for macOS')).toBe('Telegram Beta for macOS');
    expect(named('wechat@beta', '微信 Mac 版')).toBe('微信 Beta Mac 版');
    expect(named('wechat@beta', '微信Mac版')).toBe('微信 Beta Mac版');
    expect(titleName(named('telegram@beta', 'Telegram for macOS'))).toBe('Telegram Beta');
  });

  it('changes nothing when applied twice and keeps the rest of the detail', () => {
    const detail = { token: 'aerial@beta', displayName: 'Aerial', names: ['Aerial'], version: '3.6' };
    const once = withVariantName(detail);
    expect(once).toEqual({ ...detail, displayName: 'Aerial Beta' });
    expect(withVariantName(once)).toBe(once);
    expect(detail.displayName).toBe('Aerial');
  });

  it('names variants anywhere in an API response, copying only what changes', () => {
    const main = { kind: 'cask', token: 'aerial', displayName: 'Aerial' };
    const response = {
      total: 2,
      records: [
        { rank: 1, package: { kind: 'cask', token: 'aerial@beta', displayName: 'Aerial' } },
        { rank: 2, package: main }
      ],
      facts: { alternatives: [{ package: { kind: 'cask', token: 'parallels@19', displayName: 'Parallels Desktop' } }] }
    };
    const result = withVariantNames(response);
    expect(result.records[0]!.package.displayName).toBe('Aerial Beta');
    expect(result.facts.alternatives[0]!.package.displayName).toBe('Parallels Desktop 19');
    // Unchanged parts keep their identity, and the response itself is left as it is.
    expect(result.records[1]).toBe(response.records[1]);
    expect(response.records[0]!.package.displayName).toBe('Aerial');
    const plain = { total: 1, records: [{ package: main }] };
    expect(withVariantNames(plain)).toBe(plain);
    for (const value of [null, undefined, 'aerial@beta', 3]) expect(withVariantNames(value)).toBe(value);
  });

  it('keeps the name in a description whose summary the main app shares', () => {
    const t = translator('en-US');
    // Too long for the closing sentence to follow it whole, so the main app's description is the summary alone.
    const summary =
      'Screensaver that plays the aerial videos Apple filmed for Apple TV, with a choice of places, times of day and weather.';
    const main = { ...app, token: 'aerial', displayName: 'Aerial', summary };
    const beta = { ...main, token: 'aerial@beta', displayName: named('aerial@beta', 'Aerial') };
    expect(overviewDescription(main, t)).toBe(summary);
    const description = overviewDescription(beta, t);
    expect(description).toContain('Aerial Beta');
    expect(description.startsWith('Screensaver that plays')).toBe(true);
    expect(textWidth(description)).toBeLessThanOrEqual(DESCRIPTION_WIDTH);
  });

  it.each(LOCALES)('%s titles and descriptions no longer repeat the main app', code => {
    const t = translator(code);
    const main = { ...app, displayName: 'Aerial' };
    const beta = { ...main, displayName: named('aerial@beta', 'Aerial') };
    expect(overviewTitle(beta, t)).toContain('Aerial Beta');
    expect(overviewTitle(beta, t)).not.toBe(overviewTitle(main, t));
    expect(overviewDescription(beta, t)).not.toBe(overviewDescription(main, t));
    expect(versionsTitle(beta, t)).toContain('Aerial Beta');
    expect(detailsDescription(beta.displayName, t)).toContain('Aerial Beta');
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
    ],
    // Written for this test in the style of the catalog translations (no audit data for these languages yet).
    'de-DE': [
      'WeChat for Mac',
      'Messenger von Tencent für den Mac zum Austausch von Textnachrichten und Dateien sowie für Sprach- und Videoanrufe mit Ihren WeChat-Kontakten.'
    ],
    'fr-FR': [
      'WeChat for Mac',
      'Messagerie de Tencent pour Mac permettant d’échanger des messages texte et des fichiers et de passer des appels vocaux et vidéo avec vos contacts WeChat.'
    ],
    'it-IT': [
      'WeChat for Mac',
      'Client di messaggistica di Tencent per Mac per scambiare messaggi di testo e file e fare chiamate vocali e video con i tuoi contatti WeChat.'
    ],
    'zh-Hant': [
      '微信 Mac 版',
      '騰訊的 Mac 即時通訊用戶端，可與 WeChat 聯絡人收發文字訊息和檔案，以及進行語音和視訊通話。'
    ]
  };

  it.each(LOCALES)('%s preserves the app summary and still states unavailability', code => {
    const t = translator(code);
    const [displayName, summary] = wechat[code];
    for (const pkg of [
      { ...app, displayName, summary },
      { ...app, displayName, summary, disabled: true }
    ]) {
      const description = overviewDescription(pkg, t);
      const closing = overviewDescription({ ...pkg, summary: null }, t);
      expect(textWidth(description), description).toBeLessThanOrEqual(DESCRIPTION_WIDTH);
      if (pkg.disabled) {
        expect(description.endsWith(closing), description).toBe(true);
      } else {
        // Never shortened for the section sentence: either the summary alone, or the
        // whole summary followed by a complete sentence that names the app.
        const alone = fitSummary(summary, DESCRIPTION_WIDTH);
        if (description !== alone) {
          expect(description.startsWith(usableSummary(summary, displayName)), description).toBe(true);
          expect(description).toContain(displayName);
        }
      }
      expect(description).not.toMatch(/…\.|\p{Co}|[{}]/u);
    }
  });

  it('Cuts where the summary reads complete, else between words with an ellipsis', () => {
    const t = translator('en-US');
    expect(overviewDescription({ ...app, displayName: 'WeChat for Mac', summary: wechat['en-US'][1] }, t)).toBe(
      'Tencent’s Mac messaging client for text, files, voice and video calls with your WeChat contacts.'
    );
    expect(
      overviewDescription({ ...app, displayName: 'WeChat for Mac', summary: wechat['ja-JP'][1] }, translator('ja-JP'))
    ).toBe(fitSummary(wechat['ja-JP'][1], DESCRIPTION_WIDTH));
    // A short summary keeps the section sentence when both fit completely.
    expect(overviewDescription(app, t)).toBe(
      'Open-source code editor. Explore installation and uninstallation for Visual Studio Code on Mac.'
    );
    const english = 'Tencent’s Mac messaging client for text, files, voice and video calls with your WeChat contacts';
    expect(fitSummary(english, 200)).toBe(english);
    // Latin commas separate list items, so no clause ends at them; a dangling "with" goes too.
    expect(fitSummary(english, 76)).toBe('Tencent’s Mac messaging client for text, files, voice and video calls…');
    expect(fitSummary(english, 57)).toBe('Tencent’s Mac messaging client for text, files, voice…');
    // German, French and Italian cuts never end on an article or preposition either.
    expect(fitSummary('Ein schneller Editor für Notizen mit Markdown und Plugins', 34)).toBe(
      'Ein schneller Editor für Notizen…'
    );
    expect(fitSummary('Un éditeur rapide pour les notes avec Markdown et extensions', 38)).toBe(
      'Un éditeur rapide pour les notes…'
    );
    expect(fitSummary('Un editor veloce per le note con Markdown e plugin', 30)).toBe('Un editor veloce per le note…');
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
    expect(overviewDescription(chinese, zh)).toBe('查看微信 Mac 版在 Mac 上的安装方法和卸载方法。');
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
    // Japanese joins Chinese names too, and keeps the space before a Latin one.
    const ja = translator('ja-JP');
    expect(overviewDescription({ ...app, displayName: 'WeChat for Mac', summary: null }, ja)).toBe(
      'Mac 版 WeChat for Mac のインストール方法、アンインストール方法をご確認ください。'
    );
    expect(overviewDescription({ ...app, displayName: '微信', summary: null }, ja)).toBe(
      'Mac 版微信のインストール方法、アンインストール方法をご確認ください。'
    );
  });
});

describe('C2 page descriptions', () => {
  const list = (code: string) => (values: string[]) => formatList(values, { locale: code });
  const browsers = (overrides: Record<string, unknown> = {}) => ({
    name: 'Browsers',
    description: 'Web browsers',
    total: 42,
    totalLabel: '42',
    apps: ['Google Chrome', 'Firefox', 'Arc', 'Brave Browser'],
    ...overrides
  });

  it('Describes a category by its size and first apps, so pages of one category differ', () => {
    const t = translator('en-US');
    expect(categoryDescription(browsers(), t, list('en-US'))).toBe(
      'Web browsers. 42 Mac apps in Browsers, including Google Chrome, Firefox, and Arc.'
    );
    expect(categoryDescription(browsers({ apps: ['Vivaldi', 'Opera'] }), t, list('en-US'))).toBe(
      'Web browsers. 42 Mac apps in Browsers, including Vivaldi and Opera.'
    );
    expect(
      categoryDescription(browsers({ total: 1, totalLabel: '1', apps: ['Arc'], description: null }), t, list('en-US'))
    ).toBe('1 Mac app in Browsers: Arc.');
    // An empty category keeps its own description; without one the page falls back to the generic copy.
    expect(categoryDescription(browsers({ total: 0, apps: [] }), t, list('en-US'))).toBe('Web browsers');
    expect(categoryDescription(browsers({ total: 0, apps: [], description: '' }), t, list('en-US'))).toBeUndefined();
    const zh = translator('zh-CN');
    expect(categoryDescription(browsers({ name: '浏览器', description: '网页浏览器' }), zh, list('zh-CN'))).toBe(
      '网页浏览器。「浏览器」分类共有 42 款 Mac App，包括 Google Chrome、Firefox和Arc。'
    );
  });

  it.each(LOCALES)('%s fits category descriptions into search-result width', code => {
    const t = translator(code);
    const long = browsers({
      description: 'Browsers for the web, from the big three to privacy-first and keyboard-driven ones. '.repeat(4),
      apps: ['Microsoft Edge Dev Channel Preview', 'Google Chrome Canary for Developers', 'Firefox Developer Edition']
    });
    const text = categoryDescription(long, t, list(code))!;
    expect(textWidth(text)).toBeLessThanOrEqual(DESCRIPTION_WIDTH);
    expect(text).not.toMatch(/[{}|]|undefined/);
    expect(text).toContain('42');
  });

  it.each(LOCALES)('%s counts what the dependencies page lists and names the details page content', code => {
    const t = translator(code);
    const facts = { displayName: 'Example App', runtime: 3, build: 1, other: 0, conflicts: 2, dependents: 12 };
    const text = dependenciesDescription(facts, t, list(code), String);
    expect(text).toContain('Example App');
    expect(text).toContain('12');
    expect(text).not.toMatch(/[{}|]|undefined/);
    const none = dependenciesDescription(
      { ...facts, runtime: 0, build: 0, conflicts: 0, dependents: 0 },
      t,
      list(code),
      String
    );
    expect(none).not.toBe(text);
    expect(none).not.toMatch(/[{}|]/);
    const details = detailsDescription('Example App', t);
    expect(details).toContain('Example App');
    expect(details).not.toBe(text);
  });

  it('Reads naturally in English and Russian plural forms', () => {
    const facts = { displayName: 'Example App', runtime: 1, build: 2, other: 0, conflicts: 0, dependents: 5 };
    expect(dependenciesDescription(facts, translator('en-US'), list('en-US'), String)).toBe(
      'Dependencies of Example App on Mac: 1 runtime dependency, 2 build dependencies, and 5 packages that depend on it. See what Homebrew installs along with it.'
    );
    expect(dependenciesDescription(facts, translator('ru-RU'), list('ru-RU'), String)).toContain(
      '1 зависимость для работы, 2 зависимости для сборки и 5 зависящих от него пакетов'
    );
  });

  it.each(LOCALES)('%s names the brand once in the download title and keeps the article list title short', code => {
    const messages = JSON.parse(readFileSync(new URL(`../i18n/locales/${code}.json`, import.meta.url), 'utf8'));
    expect(messages.download.metaTitle.match(/OpenNavo/g)).toHaveLength(1);
    expect(textWidth(messages.articles.metaTitle)).toBeLessThanOrEqual(60);
  });
});

describe('C2 factual overview descriptions', () => {
  const free: PriceFact = {
    region: 'US',
    edition: 'Personal',
    usage: 'free',
    period: 'free',
    downloadFree: true,
    amount: '0',
    currency: 'USD',
    channel: 'vendor',
    purchaseUrl: 'https://vendor.example/pricing',
    evidence: evidence('https://vendor.example/pricing')
  };
  const complete: OverviewFacts = {
    ...app,
    displayName: 'Arc for Mac',
    summary: null,
    latestRelease: {
      version: '2.4.1',
      publishedAt: null,
      brewCommittedAt: null,
      hasNotes: true,
      summary: null,
      bodyMarkdown: null,
      sections: [],
      source: 'github_release',
      sourceUrl: 'https://vendor.example/releases'
    },
    facts: {
      ...app.facts,
      overview: { ...app.facts.overview, version: '2.4.1,900' },
      prices: [free],
      requirements: [
        {
          versions: ['2.4.1,900'],
          architectures: ['arm64'],
          platformMacOS: [],
          minimumMacOS: '13',
          maximumMacOS: null,
          appleSilicon: 'yes',
          downloadUrl: null,
          sha256: null,
          evidence: evidence('https://vendor.example/requirements')
        }
      ],
      uninstall: [
        {
          phase: 'uninstall',
          kind: 'launchctl',
          value: 'com.example.arc.helper',
          deletesUserData: false,
          evidence: evidence('https://vendor.example/uninstall')
        }
      ],
      alternatives: [
        {
          package: {
            kind: 'cask',
            token: 'alternative',
            name: 'Alternative',
            displayName: 'Alternative',
            version: '1.0',
            isFont: false,
            disabled: false,
            installs: { d30: 1, d90: 1, d365: 1 }
          },
          reasons: [],
          limits: [],
          bestFor: null
        }
      ]
    }
  };
  const changeFacts = (facts: Partial<OverviewFacts['facts']>): OverviewFacts => ({
    ...complete,
    facts: { ...complete.facts, ...facts }
  });
  const partText = (t: ReturnType<typeof translator>, part: string) => t(`package.seo.overviewParts.${part}`);
  /**
   * The description names the first sections of `order`, at least `keep` of them, and leaves out the
   * next one only because naming it would push the closing sentence past the search-result width.
   */
  const expectListed = (text: string, t: ReturnType<typeof translator>, order: string[], keep: number) => {
    const shown = order.filter(part => text.includes(partText(t, part)));
    expect(shown, text).toEqual(order.slice(0, Math.max(shown.length, keep)));
    expect(text).toContain(
      formatList(
        shown.map(part => partText(t, part)),
        { locale: t.locale }
      )
    );
    if (shown.length === order.length) return;
    const parts = formatList(
      order.slice(0, shown.length + 1).map(part => partText(t, part)),
      { locale: t.locale }
    );
    const closing = t('package.seo.descriptionFactsNoSummary', { name: complete.displayName, parts });
    expect(textWidth(closing), closing).toBeGreaterThan(DESCRIPTION_WIDTH);
  };

  it.each(LOCALES)('%s lists only visible sections, retaining dated and pending requirements', code => {
    const t = translator(code);
    const basic = overviewDescription({ ...app, summary: null }, t);
    for (const part of ['price', 'requirements', 'alternatives', 'versions'])
      expect(basic).not.toContain(t(`package.seo.overviewParts.${part}`));
    expect(basic).toContain(t('package.seo.overviewParts.installation'));
    // Every cask renders an uninstall command, even without extra removal declarations.
    expect(basic).toContain(t('package.seo.overviewParts.uninstall'));
    // Longer languages have no room for all six sections, so the last ones go first.
    const all = overviewDescription(complete, t);
    expectListed(all, t, ['installation', 'price', 'requirements', 'uninstall', 'alternatives', 'versions'], 4);
    expect(textWidth(all)).toBeLessThanOrEqual(DESCRIPTION_WIDTH);
    const fewer = overviewDescription(changeFacts({ prices: [], requirements: [] }), t);
    expectListed(fewer, t, ['installation', 'uninstall', 'alternatives', 'versions'], 4);
    expect(all).not.toMatch(/[{}]|undefined|package\.seo/);
    for (const status of ['stale', 'needs_review', 'unknown'] as const) {
      const prices = [{ ...free, evidence: { ...free.evidence, status } }];
      expect(overviewDescription(changeFacts({ prices }), t)).not.toContain(t('package.seo.overviewParts.price'));
      const requirements = complete.facts.requirements.map(row => ({ ...row, evidence: { ...row.evidence, status } }));
      const description = overviewDescription(changeFacts({ requirements }), t);
      expect(description.includes(t('package.seo.overviewParts.requirements'))).toBe(status !== 'unknown');
    }
    expect(overviewDescription({ ...complete, latestRelease: null }, t)).not.toContain(
      t('package.seo.overviewParts.versions')
    );
  });

  it.each(LOCALES)('%s keeps the summary ahead of template lists and long product names', code => {
    const t = translator(code);
    const summary = 'A code editor with extensions, themes and useful tools for every project. '.repeat(5);
    const description = overviewDescription({ ...complete, summary }, t);
    expect(description).toContain(summary.slice(0, 50));
    expect(description).not.toContain(overviewDescription(complete, t));
    for (const displayName of [longName, '很长的应用名称'.repeat(30)]) {
      const long = overviewDescription({ ...complete, displayName, summary }, t);
      expect(textWidth(long), long).toBeLessThanOrEqual(DESCRIPTION_WIDTH);
      expect(long).toBe(description);
      expect(long).not.toMatch(/…\.|[{}]|\p{Co}/u);
      expect(long).toContain(summary.slice(0, 30));
    }
  });
});

describe('Overview search overrides', () => {
  it('use each field that is set, trimmed, and brand the title once', () => {
    const both = {
      seoOverride: { title: '  Arc for Mac, picked by editors ', description: ' Fast, private browsing. ' }
    };
    expect(overrideTitle(both)).toBe('Arc for Mac, picked by editors · OpenNavo');
    expect(overrideDescription(both)).toBe('Fast, private browsing.');
    expect(overrideTitle({ seoOverride: { title: 'Arc · OpenNavo' } })).toBe('Arc · OpenNavo');
    // One field alone: the other stays with the template.
    expect(overrideTitle({ seoOverride: { description: 'Only a description' } })).toBeUndefined();
    expect(overrideDescription({ seoOverride: { title: 'Only a title' } })).toBeUndefined();
  });

  it('leave the templates in charge without an override', () => {
    for (const seoOverride of [
      undefined,
      null,
      {},
      { title: null, description: null },
      { title: '  ', description: '' }
    ]) {
      expect(overrideTitle({ seoOverride })).toBeUndefined();
      expect(overrideDescription({ seoOverride })).toBeUndefined();
    }
    expect(overrideTitle(app)).toBeUndefined();
  });

  it.each(LOCALES)('%s never let an override promise a download Homebrew has disabled', code => {
    const t = translator(code);
    const seoOverride = { title: 'Arc for Mac – Free Download', description: 'Download Arc for free on your Mac.' };
    for (const pkg of [
      { ...app, disabled: true, seoOverride },
      { ...app, disabled: true, homebrewStatus: { state: 'disabled' }, seoOverride }
    ]) {
      expect(overrideTitle(pkg)).toBeUndefined();
      expect(overrideDescription(pkg)).toBeUndefined();
      // What the overview shows instead: the unavailable templates.
      expect(overviewTitle(pkg, t)).toBe(overviewTitle({ ...app, disabled: true }, t));
      expect(overviewDescription(pkg, t)).not.toContain('Download Arc');
    }
    // Paused only by the catalog's 90-day rule, Homebrew still installs it, so the override stays.
    const paused = { ...app, disabled: true, homebrewStatus: { state: 'active' }, seoOverride };
    expect(overrideTitle(paused)).toBe('Arc for Mac – Free Download · OpenNavo');
    expect(overrideDescription(paused)).toBe('Download Arc for free on your Mac.');
  });
});
