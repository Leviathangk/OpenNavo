import { describe, expect, it } from 'vitest';
import { renderMarkdown, sanitizeReleaseHtml } from '../src/utils/markdown';

/** Parse HTML into DOM so assertions do not depend on attribute order. */
function parse(html: string): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = html;
  return root;
}

const hrefs = (root: HTMLElement) => Array.from(root.querySelectorAll('a'), link => link.getAttribute('href'));

describe('Links to GitHub bot accounts', () => {
  // GitHub's generated release notes credit bots like people; their account pages answer 404.
  const notes = [
    '* Bump actions/checkout by [@dependabot[bot]](https://github.com/dependabot[bot]) in https://github.com/o/r/pull/1',
    '* Release by [@github-actions[bot]](https://github.com/github-actions%5Bbot%5D/) with **notes**',
    '* Mirror https://www.github.com/renovate%5bbot%5d linked as text',
    '* Thanks to [@octocat](https://github.com/octocat), see [the app](https://github.com/apps/dependabot)'
  ].join('\n');

  it('Render as plain text in Markdown, raw or percent-encoded, while other links stay', () => {
    const root = parse(renderMarkdown(notes));
    expect(hrefs(root)).toEqual([
      'https://github.com/o/r/pull/1',
      'https://github.com/octocat',
      'https://github.com/apps/dependabot'
    ]);
    const items = Array.from(root.querySelectorAll('li'), item => item.textContent);
    expect(items[0]).toBe('Bump actions/checkout by @dependabot[bot] in https://github.com/o/r/pull/1');
    expect(items[1]).toBe('Release by @github-actions[bot] with notes');
    // markdown-it shows a linkified address decoded.
    expect(items[2]).toBe('Mirror https://www.github.com/renovate[bot] linked as text');
    expect(root.querySelector('li:nth-child(2) strong')?.textContent).toBe('notes');
  });

  it('Render as plain text in vendor release HTML', () => {
    const root = parse(
      sanitizeReleaseHtml(
        '<p>By <a href="https://github.com/github-actions%5Bbot%5D"><b>github-actions[bot]</b></a> and ' +
          '<a href="https://github.com/octocat">octocat</a></p>'
      )
    );
    expect(hrefs(root)).toEqual(['https://github.com/octocat']);
    expect(root.querySelector('b')?.textContent).toBe('github-actions[bot]');
    expect(root.textContent).toBe('By github-actions[bot] and octocat');
  });
});

describe('third-party release note links', () => {
  it('are marked nofollow ugc in Markdown and vendor HTML', () => {
    for (const html of [
      renderMarkdown('[notes](https://example.com/notes)'),
      sanitizeReleaseHtml('<a href="https://example.com/notes">notes</a>')
    ]) {
      expect(parse(html).querySelector('a')?.getAttribute('rel')).toBe('noopener noreferrer nofollow ugc');
    }
  });
});

describe('own site links', () => {
  it('keep noopener noreferrer without nofollow ugc', () => {
    for (const href of ['/apps/maccy', '#notes', 'https://opennavo.com/apps', 'https://api.opennavo.com/v1']) {
      expect(
        parse(renderMarkdown(`[x](${href})`))
          .querySelector('a')
          ?.getAttribute('rel')
      ).toBe('noopener noreferrer');
    }
    expect(parse(renderMarkdown('[x](https://evilopennavo.com/)')).querySelector('a')?.getAttribute('rel')).toBe(
      'noopener noreferrer nofollow ugc'
    );
  });
});

describe('vendor release HTML links', () => {
  it('treats our own pages as internal, including protocol-relative links', () => {
    const root = parse(
      sanitizeReleaseHtml(
        '<a href="https://opennavo.com/apps">one</a> <a href="//opennavo.com/apps">two</a>' +
          ' <a href="//evil.example/x">three</a>'
      )
    );
    const [first, second, third] = Array.from(root.querySelectorAll('a'));
    expect(second?.getAttribute('href')).toBe('https://opennavo.com/apps');
    expect(first?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(second?.getAttribute('rel')).toBe('noopener noreferrer');
    expect(third?.getAttribute('href')).toBe('https://evil.example/x');
    expect(third?.getAttribute('rel')).toBe('noopener noreferrer nofollow ugc');
  });

  it('keeps the bot rule for protocol-relative links too', () => {
    const root = parse(sanitizeReleaseHtml('<a href="//github.com/github-actions%5Bbot%5D">bot</a>'));
    expect(root.querySelector('a')).toBeNull();
    expect(root.textContent).toBe('bot');
  });
});
