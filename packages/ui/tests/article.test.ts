import { describe, expect, it } from 'vitest';
import { ARTICLE_MAX_APP_CARDS, headingId, renderArticle } from '../src/utils/article';

const image = { url: 'https://cdn.opennavo.example/a.png', width: 1280, height: 800 };

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
});
