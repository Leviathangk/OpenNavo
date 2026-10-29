import { describe, expect, it, vi } from 'vitest';
import { ARTICLE_MAX_APP_CARDS, headingId, renderArticle } from '../src/utils/article';

const image = { url: 'https://cdn.opennavo.example/a.png', width: 1280, height: 800 };

const articleHtml = (source: string, options: Parameters<typeof renderArticle>[1] = {}) =>
  renderArticle(source, options)
    .segments.map(segment => (segment.type === 'html' ? segment.html : ''))
    .join('');

describe('renderArticle', () => {
  it('splits app cards out of the Markdown and builds the table of contents', () => {
    const rendered = renderArticle(
      [
        '# Pick a tool',
        'Intro',
        '',
        '::app[shottr]',
        '',
        '## Pick a tool',
        '### Details',
        '```',
        '::app[inside-code]',
        '```'
      ].join('\n'),
      { images: [image] }
    );
    expect(rendered.segments.map(segment => segment.type)).toEqual(['html', 'app', 'html']);
    expect(rendered.segments[1]).toEqual({ type: 'app', token: 'shottr' });
    expect(rendered.toc).toEqual([
      { id: 'pick-a-tool', text: 'Pick a tool' },
      { id: 'pick-a-tool-2', text: 'Pick a tool' }
    ]);
    const html = rendered.segments.map(segment => (segment.type === 'html' ? segment.html : '')).join('');
    expect(html).toContain('<h2 id="pick-a-tool">');
    expect(html).toContain('<h3 id="details">');
    expect(html).not.toContain('<h1');
    expect(html).toContain('::app[inside-code]');
  });

  it('renders only listed images, keeps alt and size, and escapes raw HTML', () => {
    const { segments } = renderArticle(
      [
        '![Annotated capture](https://cdn.opennavo.example/a.png)',
        '![](https://cdn.opennavo.example/a.png)',
        '![Elsewhere](https://elsewhere.example/b.png)',
        '<script>alert(1)</script><img src=x onerror=alert(1)>',
        '[site](/apps/shottr) [vendor](https://vendor.example) [bad](javascript:alert(1))',
        '| App | Price |\n|:--|--:|\n| Shottr | Free |'
      ].join('\n\n'),
      { images: [image] }
    );
    const html = segments[0]!.type === 'html' ? segments[0]!.html : '';
    expect(html).toContain(
      '<img src="https://cdn.opennavo.example/a.png" alt="Annotated capture" width="1280" height="800" loading="lazy" decoding="async">'
    );
    expect(html.match(/<img/g)).toHaveLength(1);
    expect(html).toContain('href="https://elsewhere.example/b.png"');
    expect(html).not.toContain('<script');
    expect(html).not.toMatch(/<img[^>]*onerror/);
    expect(html).toContain('&lt;script&gt;');
    expect(html).toContain('<a href="/apps/shottr">site</a>');
    expect(html).toContain('<a href="https://vendor.example" target="_blank" rel="noopener noreferrer">vendor</a>');
    expect(html).not.toContain('href="javascript:');
    expect(html).toContain('class="on-md-right"');
  });

  it('limits app cards and names headings in any script', () => {
    const many = Array.from({ length: ARTICLE_MAX_APP_CARDS + 2 }, (_, index) => `::app[app-${index}]`).join('\n');
    expect(renderArticle(many).segments.filter(segment => segment.type === 'app')).toHaveLength(ARTICLE_MAX_APP_CARDS);
    expect(headingId('怎么选 Shottr？')).toBe('怎么选-shottr');
    expect(headingId('???')).toBe('section');
    expect(renderArticle('').segments).toEqual([]);
  });

  it('leaves out an opening heading that repeats the title, here and in the table of contents', () => {
    const body = ['## SwiftVM：在 Mac 菜单栏里试机', '', 'Intro', '', '## Setup', 'Steps'].join('\n');
    const html = (rendered: ReturnType<typeof renderArticle>) =>
      rendered.segments.map(segment => (segment.type === 'html' ? segment.html : '')).join('');
    const titled = renderArticle(body, { title: 'SwiftVM:在 Mac 菜单栏里试机 ' });
    expect(html(titled)).not.toContain('菜单栏里试机');
    expect(titled.toc).toEqual([{ id: 'setup', text: 'Setup' }]);
    // Other openings, and callers that give no title, keep the heading.
    expect(renderArticle(body, { title: 'Something else' }).toc).toHaveLength(2);
    expect(renderArticle(body).toc).toHaveLength(2);
    expect(html(renderArticle(`Intro\n\n${body}`, { title: 'SwiftVM：在 Mac 菜单栏里试机' }))).toContain(
      '菜单栏里试机'
    );
  });

  it('closes bold next to CJK punctuation and keeps CommonMark rules elsewhere', () => {
    const html = (source: string) => {
      const [segment] = renderArticle(source).segments;
      return segment?.type === 'html' ? segment.html : '';
    };
    expect(html('门槛：**要求 macOS 27 或更新版本。**Intel Mac 不受支持')).toContain(
      '门槛：<strong>要求 macOS 27 或更新版本。</strong>Intel Mac'
    );
    expect(html('他说**“先备份”**再升级')).toContain('他说<strong>“先备份”</strong>再升级');
    expect(html('（**注意**）不要删除')).toContain('（<strong>注意</strong>）');
    expect(html('foo**bar**baz')).toContain('foo<strong>bar</strong>baz');
    // CommonMark: a closer after punctuation needs whitespace or punctuation after it in Latin text.
    expect(html('**foo,**bar')).toContain('**foo,**bar');
    expect(html('a ** b ** c')).toContain('a ** b ** c');
  });
});

describe('article links', () => {
  it.each([
    ['/apps/tool', '/apps/tool', '/ja/apps/tool'],
    ['https://opennavo.com/apps/tool', '/apps/tool', '/ja/apps/tool'],
    ['https://www.opennavo.com/apps/tool', '/apps/tool', '/ja/apps/tool'],
    ['https://OPENNAVO.COM/apps/tool', '/apps/tool', '/ja/apps/tool'],
    ['https://opennavo.com', '/', '/ja'],
    ['/apps/tool?sort=name&note=a%2Fb#install', '/apps/tool', '/ja/apps/tool?sort=name&note=a%2Fb#install'],
    ['https://www.opennavo.com/apps/tool?x=1#part', '/apps/tool', '/ja/apps/tool?x=1#part'],
    ['https://opennavo.com?x=1#part', '/', '/ja?x=1#part'],
    ['/apps/tool?#', '/apps/tool', '/ja/apps/tool?#']
  ])('localizes %s in the same tab without changing its query or hash', (href, path, expected) => {
    const localizePath = vi.fn((pathname: string) => (pathname === '/' ? '/ja' : `/ja${pathname}`));
    const html = articleHtml(`[tool](${href})`, { locale: 'ja', localizePath });
    const element = new DOMParser().parseFromString(html, 'text/html').querySelector('a')!;
    expect(element.getAttribute('href')).toBe(expected);
    expect(element.hasAttribute('target')).toBe(false);
    expect(localizePath).toHaveBeenCalledExactlyOnceWith(path, 'ja');
  });

  it.each([
    'https://vendor.example/apps/tool',
    '//opennavo.com/apps/tool',
    'https://opennavo.com.example/apps/tool',
    'https://opennavo.com@vendor.example/apps/tool',
    'http://opennavo.com/apps/tool',
    '#install',
    'mailto:reader@example.test'
  ])('preserves %s without calling the path resolver', href => {
    const localizePath = vi.fn();
    const html = articleHtml(`[link](${href})`, { locale: 'ja', localizePath });
    const element = new DOMParser().parseFromString(html, 'text/html').querySelector('a')!;
    expect(element.getAttribute('href')).toBe(href);
    expect(localizePath).not.toHaveBeenCalled();
    if (href.startsWith('#')) expect(element.hasAttribute('target')).toBe(false);
    else expect(element.getAttribute('target')).toBe('_blank');
  });

  it('leaves code and image URLs untouched, including images rendered as fallback links', () => {
    const localizePath = vi.fn();
    const html = articleHtml(
      [
        '```md\n[block](/apps/tool)\n```',
        '`[inline](https://opennavo.com/apps/tool)`',
        '![Listed](/images/capture.png)',
        '![Fallback](https://opennavo.com/images/missing.png)'
      ].join('\n\n'),
      { locale: 'ja', localizePath, images: [{ url: '/images/capture.png', width: 100, height: 80 }] }
    );
    const document = new DOMParser().parseFromString(html, 'text/html');
    expect(document.querySelector('pre code')?.textContent).toBe('[block](/apps/tool)\n');
    expect(document.querySelector('p code')?.textContent).toBe('[inline](https://opennavo.com/apps/tool)');
    expect(document.querySelector('img')?.getAttribute('src')).toBe('/images/capture.png');
    expect(document.querySelector('a')?.getAttribute('href')).toBe('https://opennavo.com/images/missing.png');
    expect(localizePath).not.toHaveBeenCalled();
  });

  it('does not reuse a previous render’s locale or resolver', () => {
    const source = '[tool](https://opennavo.com/apps/tool)';
    expect(articleHtml(source, { locale: 'ja', localizePath: path => `/ja${path}` })).toContain('href="/ja/apps/tool"');
    expect(articleHtml(source)).toContain(
      '<a href="https://opennavo.com/apps/tool" target="_blank" rel="noopener noreferrer">'
    );
  });

  it.each([
    { host: 'desktop', options: { images: [image], title: 'Desktop article' } },
    { host: 'admin preview', options: { images: [], title: 'Admin preview' } },
    { host: 'locale-only', options: { locale: 'ja' } }
  ])('preserves base link behavior for $host calls without localizePath', ({ options }) => {
    const externalHrefs = [
      'https://opennavo.com/apps/tool?x=1#install',
      'https://www.opennavo.com/articles/feed.xml?x=1#part',
      'https://opennavo.com/feed.xml',
      'https://opennavo.com?x=1#part',
      'https://OPENNAVO.COM:443/apps/tool',
      '//opennavo.com/apps/tool',
      'https://vendor.example/apps/tool',
      'mailto:reader@example.test'
    ];
    const hrefs = [...externalHrefs, '/apps/tool?x=1#install', '#install'];
    const html = articleHtml(hrefs.map(href => `[link](${href})`).join('\n\n'), options);
    const links = Array.from(new DOMParser().parseFromString(html, 'text/html').querySelectorAll('a'));
    expect(links.map(link => link.getAttribute('href'))).toEqual(hrefs);
    for (const link of links.slice(0, externalHrefs.length)) {
      expect(link.getAttribute('target')).toBe('_blank');
      expect(link.getAttribute('rel')).toBe('noopener noreferrer');
    }
    for (const link of links.slice(externalHrefs.length)) {
      expect(link.hasAttribute('target')).toBe(false);
      expect(link.hasAttribute('rel')).toBe(false);
    }
  });
});
