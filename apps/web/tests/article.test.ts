import type { PackageSummary } from '@opennavo/api';
import { describe, expect, it } from 'vitest';
import {
  articleDescription,
  articleIndexedLocales,
  articleRobots,
  articleTitle,
  blogPostingNode,
  isArticleType,
  roundupItems,
  showUpdated
} from '../app/utils/article';
import type { Article, ArticleCard } from '../app/utils/article';
import { hreflangsFor } from '../shared/utils/hreflang';
import { feedLocale, renderFeed } from '../server/utils/articleFeed';

const site = 'https://opennavo.example';

function pkg(token: string, displayName: string): PackageSummary {
  return { kind: 'cask', token, name: displayName, displayName, summary: null, iconUrl: null } as PackageSummary;
}

function article(overrides: Partial<Article> = {}): Article {
  return {
    slug: 'choose-a-screenshot-app',
    type: 'roundup',
    title: 'How to choose a screenshot app',
    summary: 'What to compare.',
    bodyMarkdown: '## Pick\n\n::app[shottr]',
    testNote: null,
    seoTitle: null,
    seoDescription: null,
    locale: 'en-US',
    sourceLocale: 'en-US',
    machineTranslated: false,
    availableLocales: ['en-US', 'zh-CN'],
    author: null,
    cover: null,
    sources: [],
    packages: [pkg('shottr', 'Shottr'), pkg('cleanshot', 'CleanShot X')],
    images: [],
    related: [],
    publishedAt: '2026-10-12T10:00:00Z',
    updatedAt: '2026-10-12T18:00:00Z',
    readingMinutes: 6,
    revision: 2,
    ...overrides
  };
}

const appPath = (token: string) => `/zh/apps/${token}`;

describe('Article pages', () => {
  it('index only the languages whose text is ready and list just those in hreflang', () => {
    const open = ['en-US', 'zh-CN', 'ja-JP'] as const;
    expect(articleRobots(article(), 'zh-CN', open)).toBeUndefined();
    expect(articleRobots(article(), 'ja-JP', open)).toBe('noindex, follow');
    expect(articleIndexedLocales(article(), open)).toEqual(['en-US', 'zh-CN']);
    expect(hreflangsFor(['en-US', 'zh-CN'])).toEqual(['en', 'zh-Hans', 'zh', 'x-default']);
    expect(hreflangsFor(['zh-CN'])).toEqual(['zh-Hans', 'zh']);
  });

  it('leave languages the site has closed for indexing out of the index and out of hreflang', () => {
    // zh-CN text is ready, but the site has closed the language (L1, pages.openLocales).
    expect(articleIndexedLocales(article(), ['en-US', 'de-DE'])).toEqual(['en-US']);
    expect(articleRobots(article(), 'zh-CN', ['en-US', 'de-DE'])).toBe('noindex, follow');
    expect(articleRobots(article(), 'en-US', ['en-US', 'de-DE'])).toBeUndefined();
    expect(articleIndexedLocales(article(), ['de-DE'])).toEqual([]);
  });

  it('brand titles once and prefer the search title and description editors wrote', () => {
    expect(articleTitle(article())).toBe('How to choose a screenshot app · OpenNavo');
    expect(articleTitle(article({ seoTitle: 'Screenshot apps compared · OpenNavo' }))).toBe(
      'Screenshot apps compared · OpenNavo'
    );
    expect(articleDescription(article())).toBe('What to compare.');
    expect(articleDescription(article({ seoDescription: 'Compare four tools.' }))).toBe('Compare four tools.');
    expect(articleDescription(article({ summary: null }))).toBeUndefined();
    expect(showUpdated(article())).toBe(false);
    expect(showUpdated(article({ updatedAt: '2026-10-14T08:00:00Z' }))).toBe(true);
    expect(isArticleType('roundup')).toBe(true);
    expect(isArticleType('press')).toBe(false);
  });

  it('describe the byline, dates, language and apps in BlogPosting', () => {
    const editorial = blogPostingNode(article(), { siteUrl: site, appPath });
    expect(editorial).toMatchObject({
      '@type': 'BlogPosting',
      headline: 'How to choose a screenshot app',
      datePublished: '2026-10-12T10:00:00Z',
      dateModified: '2026-10-12T18:00:00Z',
      inLanguage: 'en',
      author: { '@id': `${site}/#identity` },
      publisher: { '@id': `${site}/#identity` }
    });
    expect(editorial.about).toBeUndefined();
    expect(editorial.mentions).toEqual([
      {
        '@type': 'SoftwareApplication',
        '@id': `${site}/apps/shottr#app`,
        name: 'Shottr',
        url: `${site}/zh/apps/shottr`
      },
      {
        '@type': 'SoftwareApplication',
        '@id': `${site}/apps/cleanshot#app`,
        name: 'CleanShot X',
        url: `${site}/zh/apps/cleanshot`
      }
    ]);
    const signed = blogPostingNode(
      article({
        author: { slug: 'lin-wei', name: 'Lin Wei', role: null, bio: null, avatarUrl: null, links: [] },
        packages: [pkg('shottr', 'Shottr')]
      }),
      { siteUrl: site, appPath, authorPath: '/zh/authors/lin-wei', image: `${site}/_og/s/a.png` }
    );
    expect(signed.author).toEqual({
      '@type': 'Person',
      '@id': `${site}/authors/lin-wei#person`,
      name: 'Lin Wei',
      url: `${site}/zh/authors/lin-wei`
    });
    expect(signed.about).toMatchObject({ '@type': 'SoftwareApplication', '@id': `${site}/apps/shottr#app` });
    expect(signed.image).toBe(`${site}/_og/s/a.png`);
  });

  it('list a roundup’s apps in body order, and nothing for other types', () => {
    expect(roundupItems(article(), ['cleanshot', 'shottr', 'cleanshot', 'gone'], { siteUrl: site, appPath })).toEqual([
      { position: 1, name: 'CleanShot X', url: `${site}/zh/apps/cleanshot` },
      { position: 2, name: 'Shottr', url: `${site}/zh/apps/shottr` }
    ]);
    expect(roundupItems(article({ type: 'review' }), ['shottr', 'cleanshot'], { siteUrl: site, appPath })).toBeNull();
    expect(roundupItems(article(), ['shottr'], { siteUrl: site, appPath })).toBeNull();
  });
});

describe('Article feeds', () => {
  const card = (overrides: Partial<ArticleCard>): ArticleCard => ({
    slug: 'pick',
    type: 'tutorial',
    title: 'Pick <apps> & more',
    summary: 'Short "summary"',
    cover: null,
    author: null,
    publishedAt: '2026-10-12T10:00:00Z',
    updatedAt: '2026-10-12T10:00:00Z',
    readingMinutes: 4,
    locale: 'zh-CN',
    machineTranslated: true,
    ...overrides
  });

  it('map path prefixes to languages', () => {
    expect(feedLocale(undefined)).toBe('en-US');
    expect(feedLocale('zh')).toBe('zh-CN');
    expect(feedLocale('xx')).toBeUndefined();
  });

  it('hold the newest articles whose text is in the feed language, escaped', () => {
    const xml = renderFeed({
      locale: 'zh-CN',
      siteUrl: site,
      siteName: 'OpenNavo',
      cards: [card({}), card({ slug: 'english-only', locale: 'en-US' })]
    });
    expect(xml).toContain('<title>OpenNavo 文章</title>');
    expect(xml).toContain(`<link>${site}/zh/articles</link>`);
    expect(xml).toContain(`<atom:link href="${site}/zh/articles/feed.xml" rel="self" type="application/rss+xml"/>`);
    expect(xml).toContain('<language>zh-CN</language>');
    expect(xml).toContain('<title>Pick &lt;apps&gt; &amp; more</title>');
    expect(xml).toContain(`<guid isPermaLink="true">${site}/zh/articles/pick</guid>`);
    expect(xml).toContain('<pubDate>Mon, 12 Oct 2026 10:00:00 GMT</pubDate>');
    expect(xml).toContain('<dc:creator>OpenNavo 编辑部</dc:creator>');
    expect(xml).toContain('<category>教程</category>');
    expect(xml).toContain('<description>Short &quot;summary&quot;</description>');
    expect(xml).not.toContain('english-only');
  });
});
