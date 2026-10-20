import { describe, expect, it } from 'vitest';
import type { PublicComponents } from '@opennavo/api';
import {
  feedTarget,
  inlineHtml,
  markdownHtml,
  newestFirst,
  releaseAnchor,
  releaseDate,
  releaseHtml,
  renderReleaseFeed
} from '../server/utils/releaseFeed';
import { siteMessage } from '../server/utils/siteCopy';

type ReleaseEntry = PublicComponents['schemas']['ReleaseEntry'];

const entry = (patch: Partial<ReleaseEntry>): ReleaseEntry => ({
  id: 7,
  version: '1.2.0',
  publishedAt: '2026-10-01T08:00:00Z',
  source: 'github_release',
  isLatest: true,
  isPrerelease: false,
  hasNotes: true,
  sections: [],
  translation: { status: 'source', locale: 'en-US' },
  ...patch
});

describe('Update feeds (F6)', () => {
  it('Reads the language and app or collection from the feed path', () => {
    expect(feedTarget('/apps/visual-studio-code/feed.xml')).toEqual({
      locale: 'en-US',
      section: 'apps',
      key: 'visual-studio-code'
    });
    expect(feedTarget('/zh-hant/apps/nextpad++/feed.xml')).toEqual({
      locale: 'zh-Hant',
      section: 'apps',
      key: 'nextpad++'
    });
    expect(feedTarget('/ja/collections/new-mac/feed.xml')).toEqual({
      locale: 'ja-JP',
      section: 'collections',
      key: 'new-mac'
    });
    expect(feedTarget('/apps/python%403.14/feed.xml')?.key).toBe('python@3.14');
    for (const path of [
      '/xx/apps/a/feed.xml',
      '/en/apps/a/feed.xml',
      '/apps/A B/feed.xml',
      '/collections/Bad_Slug/feed.xml',
      '/apps/a/rss'
    ])
      expect(feedTarget(path)).toBeUndefined();
  });

  it('Turns release notes into escaped HTML, keeping only bold, code, lists and paragraphs', () => {
    expect(inlineHtml('Bell\u0007 and\u0000 null')).toBe('Bell and null');
    expect(inlineHtml('Faster **sync** with `--flag` & <tags>')).toBe(
      'Faster <strong>sync</strong> with <code>--flag</code> &amp; &lt;tags&gt;'
    );
    expect(markdownHtml('## Editor\n\n- One\n- **Two**\n\nText <b>raw</b>\nmore')).toBe(
      '<p><strong>Editor</strong></p><ul><li>One</li><li><strong>Two</strong></li></ul><p>Text &lt;b&gt;raw&lt;/b&gt;<br>more</p>'
    );
    expect(
      releaseHtml(entry({ summary: 'Big **one**', sections: [{ area: 'Editor', items: ['Fix `x`'] }] }), 'No notes')
    ).toBe('<p>Big <strong>one</strong></p><p><strong>Editor</strong></p><ul><li>Fix <code>x</code></li></ul>');
    expect(releaseHtml(entry({ hasNotes: false }), 'No notes')).toBe('<p>No notes</p>');
    expect(releaseHtml(entry({ bodyMarkdown: 'Plain' }), 'No notes')).toBe('<p>Plain</p>');
  });

  it('Dates releases by upstream, then Homebrew, and orders them newest first', () => {
    expect(releaseDate({ publishedAt: null, brewCommittedAt: '2026-09-01T00:00:00Z' })).toBe(
      '2026-09-01T00:00:00.000Z'
    );
    expect(releaseDate({ publishedAt: '0001-01-01T00:00:00Z', brewCommittedAt: null })).toBeNull();
    expect(newestFirst([{ date: null }, { date: '2026-01-01T00:00:00Z' }, { date: '2026-02-01T00:00:00Z' }])).toEqual([
      { date: '2026-02-01T00:00:00Z' },
      { date: '2026-01-01T00:00:00Z' },
      { date: null }
    ]);
    expect(releaseAnchor({ id: 7, version: '1.2' })).toBe('r-7');
    expect(releaseAnchor({ id: null, version: '0.1,49' })).toBe('v-0.1-49');
  });

  it('Renders an RSS 2.0 channel with escaped items, a self link and stable guids', () => {
    const xml = renderReleaseFeed({
      title: siteMessage('en-US', 'feeds.appTitle', { name: 'Tom & Jerry' }),
      link: 'https://opennavo.example/apps/tj/versions',
      description: siteMessage('en-US', 'feeds.appDescription', { name: 'Tom & Jerry' }),
      locale: 'en-US',
      self: 'https://opennavo.example/apps/tj/feed.xml',
      items: [
        {
          title: '1.2.0',
          link: 'https://opennavo.example/apps/tj/versions#r-7',
          guid: 'cask/tj@1.2.0',
          date: '2026-10-01T08:00:00.000Z',
          html: '<p>Notes &amp; more</p>'
        }
      ]
    });
    expect(xml.startsWith('<?xml version="1.0" encoding="UTF-8"?>\n<rss version="2.0"')).toBe(true);
    expect(xml).toContain('<title>Tom &amp; Jerry updates · OpenNavo</title>');
    expect(xml).toContain(
      '<atom:link href="https://opennavo.example/apps/tj/feed.xml" rel="self" type="application/rss+xml"/>'
    );
    expect(xml).toContain('<guid isPermaLink="false">cask/tj@1.2.0</guid>');
    expect(xml).toContain('<pubDate>Thu, 01 Oct 2026 08:00:00 GMT</pubDate>');
    expect(xml).toContain('<lastBuildDate>Thu, 01 Oct 2026 08:00:00 GMT</lastBuildDate>');
    // The item HTML is escaped once more inside the XML element.
    expect(xml).toContain('<description>&lt;p&gt;Notes &amp;amp; more&lt;/p&gt;</description>');
    // Every element that opens also closes.
    for (const tag of ['rss', 'channel', 'item', 'title', 'link', 'guid', 'description'])
      expect(xml.split(`<${tag}`).length - 1).toBe(xml.split(`</${tag}>`).length - 1);
  });

  it('Writes channel copy in the feed language, falling back to English for missing messages', () => {
    expect(siteMessage('zh-CN', 'feeds.appTitle', { name: 'Zed' })).toBe('Zed 版本更新 · OpenNavo');
    expect(siteMessage('de-DE', 'feeds.appTitle', { name: 'Zed' })).toBe('Updates für Zed · OpenNavo');
    expect(siteMessage('en-US', 'no.such.key')).toBe('no.such.key');
    expect(siteMessage('en-US', 'browse.appCount', { count: 1 }, 1)).toBe('1 app');
    expect(siteMessage('en-US', 'browse.appCount', { count: 3 }, 3)).toBe('3 apps');
  });
});
