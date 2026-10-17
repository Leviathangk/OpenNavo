import { describe, expect, it } from 'vitest';
import {
  githubReleaseUrl,
  httpsUrl,
  linkDownloadUrl,
  linkNotes,
  linkSource,
  officialDownloadUrl,
  sortedAssets
} from '../app/utils/linkApp';
import type { ReleaseAsset } from '../app/utils/linkApp';

const release = {
  tag: 'v2.4.1',
  version: '2.4.1',
  publishedAt: '2026-10-13T00:00:00Z',
  url: 'https://github.com/lumen-labs/lumen/releases/tag/v2.4.1',
  assets: []
};
const link = {
  homepage: 'https://lumen.app',
  downloadUrl: null,
  github: { repo: 'lumen-labs/lumen', latest: release },
  teamId: null,
  pricing: 'free' as const,
  notes: null,
  adoptedByCask: null
};

const asset = (name: string, arch: ReleaseAsset['arch'], format: ReleaseAsset['format'] = 'dmg'): ReleaseAsset => ({
  name,
  url: `https://github.com/lumen-labs/lumen/releases/download/v2.4.1/${name}`,
  size: 1,
  arch,
  format,
  sha256: null
});

describe('Link app downloads', () => {
  it('Only https addresses become links', () => {
    expect(httpsUrl('https://lumen.app')).toBe('https://lumen.app');
    expect(httpsUrl('http://lumen.app')).toBeUndefined();
    expect(httpsUrl('javascript:alert(1)')).toBeUndefined();
    expect(httpsUrl('not a url')).toBeUndefined();
    expect(httpsUrl(null)).toBeUndefined();
  });

  it('Downloads from the official page, then GitHub, then the homepage', () => {
    expect(linkDownloadUrl({ ...link, downloadUrl: 'https://lumen.app/download' })).toBe('https://lumen.app/download');
    expect(linkDownloadUrl(link)).toBe(release.url);
    expect(linkDownloadUrl({ ...link, github: { repo: 'lumen-labs/lumen', latest: null } })).toBe(
      'https://github.com/lumen-labs/lumen/releases/latest'
    );
    expect(linkDownloadUrl({ ...link, github: null })).toBe('https://lumen.app');
    expect(
      linkDownloadUrl({ ...link, downloadUrl: 'http://lumen.app/x', github: null, homepage: null })
    ).toBeUndefined();
    expect(githubReleaseUrl({ github: null })).toBeUndefined();
  });

  it('Names where the files come from', () => {
    expect(linkSource({ ...link, downloadUrl: 'https://www.lumen.app/download' })).toBe('lumen.app');
    expect(linkSource(link)).toBe('github.com/lumen-labs/lumen');
    expect(linkSource({ ...link, github: null })).toBe('lumen.app');
    expect(linkSource({ ...link, github: null, homepage: null })).toBeUndefined();
  });

  it('Treats only the developer site or the repository as the official file', () => {
    const own = { ...link, downloadUrl: 'https://cdn.lumen.app/Lumen.dmg' };
    expect(officialDownloadUrl(own)).toBe('https://cdn.lumen.app/Lumen.dmg');
    const repo = { ...link, downloadUrl: 'https://github.com/lumen-labs/lumen/releases/download/v2/L.dmg' };
    expect(officialDownloadUrl({ ...repo, homepage: 'https://github.com/lumen-labs/lumen' })).toBe(repo.downloadUrl);
    // Another repository on github.com does not count just because the homepage is on GitHub too.
    const other = {
      ...link,
      homepage: 'https://github.com/lumen-labs/lumen',
      downloadUrl: 'https://github.com/x/y/a.dmg'
    };
    expect(officialDownloadUrl(other)).toBe(release.url);
    expect(officialDownloadUrl({ ...link, downloadUrl: 'https://lumen.app.evil.example/L.dmg' })).toBe(release.url);
  });

  it('Lists installers first, universal before either chip', () => {
    const names = sortedAssets([
      asset('latest-mac.yml', null, 'other'),
      asset('Lumen-x64.dmg', 'x86_64'),
      asset('Lumen-arm64.dmg', 'arm64'),
      asset('Lumen-universal.zip', 'universal', 'zip')
    ]).map(item => item.name);
    expect(names).toEqual(['Lumen-universal.zip', 'Lumen-arm64.dmg', 'Lumen-x64.dmg', 'latest-mac.yml']);
  });

  it('Shows notes in the page language, else English, else any', () => {
    expect(linkNotes({ 'zh-CN': '中文', 'en-US': 'English' }, 'zh-CN')).toBe('中文');
    expect(linkNotes({ 'zh-CN': '', 'en-US': 'English' }, 'zh-CN')).toBe('English');
    expect(linkNotes({ 'ja-JP': '日本語' }, 'ru-RU')).toBe('日本語');
    expect(linkNotes(null, 'en-US')).toBe('');
  });
});
