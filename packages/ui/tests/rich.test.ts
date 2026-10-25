import { config as testConfig } from '@vue/test-utils';
import { ref as localeRef } from 'vue';
import { ON_UI_LOCALE as fixtureLocaleKey } from '../src/composables/locale';
import { mount } from '@vue/test-utils';
import { afterEach, describe, expect, it } from 'vitest';
import { nextTick } from 'vue';
import OnFeatureHero from '../src/components/OnFeatureHero.vue';
import OnMarkdown from '../src/components/OnMarkdown.vue';
import OnMarkdownView from '../src/components/OnMarkdownView.vue';
import OnScreenshotGallery from '../src/components/OnScreenshotGallery.vue';
import OnTopNav from '../src/components/OnTopNav.vue';
import { heroGlowGradient } from '../src/utils/glow';
import { renderMarkdown, sanitizeReleaseHtml } from '../src/utils/markdown';
import { RouterLinkStub, withLocale } from './helpers';

/** Parse HTML into DOM so assertions do not depend on attribute order. */
function parse(html: string): HTMLElement {
  const root = document.createElement('div');
  root.innerHTML = html;
  return root;
}

describe('sanitizeReleaseHtml', () => {
  // A typical Sparkle appcast description: a full document with styles, a script, a tracking pixel and its own h1.
  const appcast = `<html><head><style>body{color:red}</style><script>alert(1)</script></head><body>
    <h1>Version 2.4</h1><h2>Fixes</h2><ul><li onclick="x()">Faster <b>sync</b></li></ul>
    <img src="https://vendor.example/shot.png" alt="Screenshot"><img src="http://tracker.example/p.gif">
    <a href="javascript:alert(1)">bad</a> <a href="https://vendor.example/notes">notes</a></body></html>`;

  it('Keeps text and lists but drops scripts, styles, handlers and unsafe links', () => {
    const root = parse(sanitizeReleaseHtml(appcast));
    expect(root.querySelector('script, style, [onclick]')).toBeNull();
    expect(root.querySelector('li b')?.textContent).toBe('sync');
    const links = Array.from(root.querySelectorAll('a'));
    expect(links.find(link => link.textContent === 'bad')?.hasAttribute('href')).toBe(false);
    const notes = links.find(link => link.textContent === 'notes');
    expect(notes?.getAttribute('target')).toBe('_blank');
    expect(notes?.getAttribute('rel')).toBe('noopener noreferrer');
  });

  it('Never loads images: https ones become links named by their alt text, others disappear', () => {
    const root = parse(sanitizeReleaseHtml(appcast));
    expect(root.querySelector('img')).toBeNull();
    const shot = Array.from(root.querySelectorAll('a')).find(link => link.textContent === 'Screenshot');
    expect(shot?.getAttribute('href')).toBe('https://vendor.example/shot.png');
    expect(root.innerHTML).not.toContain('tracker.example');
  });

  it('Starts headings below the page: the first level found becomes h4 by default', () => {
    const root = parse(sanitizeReleaseHtml(appcast));
    expect(Array.from(root.querySelectorAll('h4, h5'), heading => heading.tagName)).toEqual(['H4', 'H5']);
    expect(root.querySelector('h1, h2')).toBeNull();
  });
});

describe('renderMarkdown', () => {
  it('Closes bold next to CJK punctuation in release notes too', () => {
    expect(renderMarkdown('修复：**同步失败。**Intel 机型')).toContain('修复：<strong>同步失败。</strong>Intel');
  });

  it('Displays raw HTML as text', () => {
    const html = renderMarkdown('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)> **粗体**');
    const root = parse(html);
    expect(root.querySelector('script, img')).toBeNull();
    expect(root.textContent).toContain('<script>alert(1)</script>');
    expect(root.querySelector('strong')?.textContent).toBe('粗体');
  });

  it('Maps encountered heading levels in order starting at h3 and ending at h6', () => {
    const root = parse(renderMarkdown('# 一\n\n## 二\n\n### 三\n\n#### 四\n\n##### 五'));
    expect(Array.from(root.children, element => element.tagName)).toEqual(['H3', 'H4', 'H5', 'H6', 'H6']);
  });

  it('Compresses skipped source heading levels and assigns visual classes in order', () => {
    const root = parse(renderMarkdown('## 一\n\n#### 二\n\n###### 三\n\n## 四'));
    expect(Array.from(root.children, element => element.tagName)).toEqual(['H3', 'H4', 'H5', 'H3']);
    expect(Array.from(root.children, element => element.className)).toEqual([
      'on-md-heading-1',
      'on-md-heading-2',
      'on-md-heading-3',
      'on-md-heading-1'
    ]);
  });

  it('Uses headingLevel for the highest heading and clamps it to levels 2 through 6', () => {
    const tags = (source: string, headingLevel: number) =>
      Array.from(parse(renderMarkdown(source, { headingLevel })).children, element => element.tagName);
    expect(tags('## 一\n\n### 二', 2)).toEqual(['H2', 'H3']);
    expect(tags('# 一\n\n## 二', 4)).toEqual(['H4', 'H5']);
    expect(tags('# 一', 1)).toEqual(['H2']);
  });

  it('Opens links in new windows without opener access and automatically recognizes URLs', () => {
    const root = parse(renderMarkdown('[发布说明](https://example.com/notes) 另见 https://ghostty.org/docs'));
    const links = Array.from(root.querySelectorAll('a'));
    expect(links.map(link => link.getAttribute('href'))).toEqual([
      'https://example.com/notes',
      'https://ghostty.org/docs'
    ]);
    for (const link of links) {
      expect(link.getAttribute('target')).toBe('_blank');
      expect(link.getAttribute('rel')).toBe('noopener noreferrer');
    }
  });

  it('Does not produce links or event attributes from dangerous protocols or attribute injection', () => {
    const root = parse(renderMarkdown('[x](javascript:alert(1)) [y](https://a.com "t\\" onmouseover=\\"alert(1)")'));
    // javascript: links are unrecognized and displayed as literal text.
    expect(root.textContent).toContain('[x](javascript:alert(1))');
    expect(root.querySelector('[onmouseover]')).toBeNull();
    expect(root.querySelectorAll('a')).toHaveLength(1);
    expect(root.querySelector('a')?.getAttribute('href')).toBe('https://a.com');
  });

  it('Renders images as links to the originals without img elements', () => {
    const root = parse(
      renderMarkdown('![新的设置页](https://cdn.example.com/a.png) ![](https://cdn.example.com/b.png)')
    );
    expect(root.querySelector('img')).toBeNull();
    const links = Array.from(root.querySelectorAll('a'));
    expect(links.map(link => [link.textContent, link.getAttribute('href')])).toEqual([
      ['新的设置页', 'https://cdn.example.com/a.png'],
      ['https://cdn.example.com/b.png', 'https://cdn.example.com/b.png']
    ]);
  });

  it('Uses classes for table alignment and preserves code-block language classes', () => {
    const html = renderMarkdown('| 名称 | 大小 |\n|:-:|--:|\n| a | 2 |\n\n```js\nconst a = 1;\n```');
    const root = parse(html);
    expect(html).not.toContain('style=');
    expect(root.querySelector('th')?.className).toBe('on-md-center');
    expect(root.querySelectorAll('td')[1]?.className).toBe('on-md-right');
    expect(root.querySelector('pre > code')?.className).toBe('language-js');
    expect(root.querySelector('pre > code')?.textContent).toBe('const a = 1;\n');
  });
});

describe('heroGlowGradient', () => {
  it('Derives color stops from the default glow proportions and returns undefined for invalid colors', () => {
    expect(heroGlowGradient('#5B5BD6')).toBe(
      'radial-gradient(circle, rgba(206, 206, 243, 0.95) 0%, rgba(135, 135, 225, 0.78) 14%, rgba(91, 91, 214, 0.5) 32%, rgba(68, 68, 161, 0.2) 52%, transparent 70%)'
    );
    expect(heroGlowGradient('red')).toBeUndefined();
    expect(heroGlowGradient(null)).toBeUndefined();
    expect(heroGlowGradient(undefined)).toBeUndefined();
  });
});

describe('OnMarkdown', () => {
  it('Renders sanitized HTML with a maximum width', () => {
    const wrapper = mount(OnMarkdown, { props: { source: '## 新功能\n\n- 支持 `brew bundle`\n- <b>不是标签</b>' } });
    expect(wrapper.classes()).toContain('max-w-[72ch]');
    expect(wrapper.get('h3').text()).toBe('新功能');
    expect(wrapper.get('code').text()).toBe('brew bundle');
    expect(wrapper.findAll('code .on-code-word').map(word => word.text())).toEqual(['brew', 'bundle']);
    expect(wrapper.find('b').exists()).toBe(false);
    expect(wrapper.text()).toContain('<b>不是标签</b>');
  });

  it('OnMarkdownView displays only supplied sanitized HTML and changes font size with size', () => {
    const wrapper = mount(OnMarkdownView, {
      props: { html: '<h2 class="on-md-heading-1">简介</h2><p>正文</p>', size: 'md' }
    });
    expect(wrapper.classes()).toEqual(expect.arrayContaining(['on-markdown', 'text-14px']));
    expect(wrapper.get('h2').text()).toBe('简介');
    expect(wrapper.get('p').text()).toBe('正文');
  });

  it('OnMarkdownView reads article bodies across their column', () => {
    const wrapper = mount(OnMarkdownView, { props: { html: '<h2 id="pick">怎么选</h2>', size: 'article' } });
    expect(wrapper.classes()).toEqual(expect.arrayContaining(['on-markdown', 'on-markdown-article']));
    expect(wrapper.classes()).not.toContain('max-w-[72ch]');
  });

  it('Passes headingLevel to rendering so content directly below the page h1 starts at h2', () => {
    const wrapper = mount(OnMarkdown, { props: { source: '## 简介\n\n### 特性', headingLevel: 2 } });
    expect(wrapper.get('h2').text()).toBe('简介');
    expect(wrapper.get('h3').text()).toBe('特性');
  });
});

const heroIcons = [
  { kind: 'cask', token: 'ghostty', name: 'Ghostty' },
  { kind: 'cask', token: 'iterm2', name: 'iTerm2' },
  { kind: 'cask', token: 'zed', name: 'Zed' },
  { kind: 'cask', token: 'warp', name: 'Warp' }
] as const;

describe('OnFeatureHero', () => {
  it('Renders badges, titles, secondary lines, highlighted text, and at most three icons', () => {
    const wrapper = mount(OnFeatureHero, {
      props: {
        badge: '本周精选',
        title: 'Ghostty 1.3',
        subtitle: '终端，本该这么快。',
        description: '原生 macOS 界面。**近 30 天安装 14,326 次。**',
        icons: [...heroIcons]
      }
    });
    expect(wrapper.text()).toContain('本周精选');
    const heading = wrapper.get('h2');
    expect(heading.text()).toContain('Ghostty 1.3');
    expect(heading.get('span').text()).toBe('终端，本该这么快。');
    expect(wrapper.get('p span.font-500').text()).toBe('近 30 天安装 14,326 次。');
    const floats = wrapper.findAll('.on-hero-float');
    expect(floats).toHaveLength(3);
    expect(floats.map(float => float.get('span').attributes('style'))).toEqual([
      expect.stringContaining('width: 118px'),
      expect.stringContaining('width: 54px'),
      expect.stringContaining('width: 46px')
    ]);
    expect(floats[0]?.attributes('style')).toContain('rotate(-8deg)');
    // Hide the entire decorative layer from screen readers.
    expect(wrapper.get('.on-hero-stage').attributes('aria-hidden')).toBe('true');
  });

  it('Overrides the glow with glowColor and otherwise uses tokens', () => {
    const tinted = mount(OnFeatureHero, { props: { title: 'Zed', glowColor: '#3C96F5' } });
    expect(tinted.get('.on-hero-glow').attributes('style')).toContain('radial-gradient');
    const plain = mount(OnFeatureHero, { props: { title: 'Zed', glowColor: 'not-a-color' } });
    expect(plain.get('.on-hero-glow').attributes('style')).toBeUndefined();
  });

  it('Emits action from the primary button and renders detail links with linkAs', async () => {
    const wrapper = mount(OnFeatureHero, {
      props: { title: 'Ghostty', detailHref: '/apps/ghostty', linkAs: RouterLinkStub, responsive: true }
    });
    expect(wrapper.classes()).toContain('lg:h-360px');
    await wrapper.get('button').trigger('click');
    expect(wrapper.emitted('action')).toHaveLength(1);
    const detail = wrapper.get('a');
    expect(detail.attributes('href')).toBe('#/apps/ghostty');
    expect(detail.text()).toBe('查看详情');

    const bare = mount(OnFeatureHero, { props: { title: 'Ghostty', headingLevel: 3 }, global: withLocale('en-US') });
    expect(bare.find('a').exists()).toBe(false);
    expect(bare.get('h3').text()).toBe('Ghostty');
    expect(bare.get('button').text()).toBe('Get');
  });
});

describe('OnScreenshotGallery', () => {
  const shots = [
    { src: '/shots/1.png', caption: '主窗口' },
    { src: '/shots/2.png' },
    { src: '/shots/3.png', caption: '设置' }
  ];

  afterEach(() => {
    document.body.innerHTML = '';
  });

  const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"]');

  it('Renders nothing when there are no screenshots', () => {
    const wrapper = mount(OnScreenshotGallery, { props: { items: [] } });
    expect(wrapper.find('section').exists()).toBe(false);
  });

  it('Uses captions as alternative text and otherwise names screenshots by index', () => {
    const wrapper = mount(OnScreenshotGallery, { props: { items: shots }, global: withLocale('en-US') });
    expect(wrapper.get('section').attributes('aria-label')).toBe('Screenshots');
    expect(wrapper.findAll('img').map(image => image.attributes('alt'))).toEqual(['主窗口', 'Screenshot 2', '设置']);
    // Visible captions repeat alt text and are hidden from screen readers.
    expect(wrapper.get('button span').attributes('aria-hidden')).toBe('true');
  });

  it('Names uncaptioned screenshots after the app when the host passes its name', async () => {
    const wrapper = mount(OnScreenshotGallery, {
      props: { items: shots, name: 'Inkscape' },
      global: withLocale('en-US'),
      attachTo: document.body
    });
    expect(wrapper.findAll('img').map(image => image.attributes('alt'))).toEqual([
      '主窗口',
      'Inkscape · Screenshot 2',
      '设置'
    ]);
    // The lightbox image carries the same text.
    await wrapper.findAll('button')[1]?.trigger('click');
    await nextTick();
    expect(dialog()?.querySelector('figure img')?.getAttribute('alt')).toBe('Inkscape · Screenshot 2');
    wrapper.unmount();
  });

  it('Renders caption chips and the lightbox in the dark theme, as their backgrounds stay dark in both themes', async () => {
    const wrapper = mount(OnScreenshotGallery, { props: { items: shots }, attachTo: document.body });
    expect(wrapper.get('button span').attributes('data-theme')).toBe('dark');
    await wrapper.get('button').trigger('click');
    await nextTick();
    expect(dialog()?.dataset.theme).toBe('dark');
    wrapper.unmount();
  });

  it('Moves focus into the lightbox, cycles with arrow keys, and closes with Escape while restoring focus', async () => {
    const wrapper = mount(OnScreenshotGallery, { props: { items: shots }, attachTo: document.body });
    const thumbs = wrapper.findAll('button');
    await thumbs[1]?.trigger('click');
    await nextTick();

    const box = dialog();
    expect(box?.getAttribute('aria-modal')).toBe('true');
    expect(box?.querySelector('figure img')?.getAttribute('src')).toBe('/shots/2.png');
    expect(box?.textContent).toContain('2 / 3');
    expect(document.activeElement?.getAttribute('aria-label')).toBe('关闭');
    expect(document.documentElement.style.overflow).toBe('hidden');

    const press = async (key: string) => {
      box?.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true }));
      await nextTick();
    };
    await press('ArrowRight');
    expect(dialog()?.querySelector('figure img')?.getAttribute('src')).toBe('/shots/3.png');
    expect(dialog()?.textContent).toContain('设置');
    await press('ArrowRight');
    expect(dialog()?.querySelector('figure img')?.getAttribute('src')).toBe('/shots/1.png');
    await press('ArrowLeft');
    expect(dialog()?.querySelector('figure img')?.getAttribute('src')).toBe('/shots/3.png');

    await press('Escape');
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(thumbs[1]?.element);
    expect(document.documentElement.style.overflow).toBe('');
    wrapper.unmount();
  });

  it('Cycles Tab among lightbox buttons, closes on backdrop clicks, and omits navigation for a single image', async () => {
    const wrapper = mount(OnScreenshotGallery, { props: { items: shots }, attachTo: document.body });
    await wrapper.get('button').trigger('click');
    await nextTick();
    const buttons = Array.from(dialog()?.querySelectorAll('button') ?? []);
    expect(buttons.map(button => button.getAttribute('aria-label'))).toEqual(['关闭', '上一张', '下一张']);
    buttons[2]?.focus();
    buttons[2]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    expect(document.activeElement).toBe(buttons[0]);
    buttons[0]?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }));
    expect(document.activeElement).toBe(buttons[2]);

    dialog()?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await nextTick();
    expect(dialog()).toBeNull();
    wrapper.unmount();

    const single = mount(OnScreenshotGallery, {
      props: { items: [shots[0] as (typeof shots)[0]] },
      attachTo: document.body
    });
    await single.get('button').trigger('click');
    await nextTick();
    expect(dialog()?.querySelectorAll('button')).toHaveLength(1);
    single.unmount();
    // Release scroll lock on unmount.
    expect(document.documentElement.style.overflow).toBe('');
  });
});

describe('OnTopNav', () => {
  const items = [
    { label: '发现', href: '/', active: true },
    { label: '分类', href: '/categories' },
    { label: '排行榜', href: '/rankings' }
  ];

  afterEach(() => {
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true });
    document.body.innerHTML = '';
  });

  it('Renders links with linkAs and marks the current page with aria-current', () => {
    const wrapper = mount(OnTopNav, { props: { items, downloadHref: '/download', linkAs: RouterLinkStub } });
    const nav = wrapper.get('nav');
    const links = nav.findAll('a');
    expect(links.map(link => link.attributes('href'))).toEqual(['#/', '#/categories', '#/rankings']);
    expect(links[0]?.attributes('aria-current')).toBe('page');
    expect(links[1]?.attributes('aria-current')).toBeUndefined();
    expect(wrapper.get('a[aria-label="OpenNavo 首页"]').attributes('href')).toBe('#/');
    expect(
      wrapper.findAll('a').some(link => link.text() === '下载客户端' && link.attributes('href') === '#/download')
    ).toBe(true);
  });

  it('Emits search on search-box clicks and displays the shortcut', async () => {
    const wrapper = mount(OnTopNav, {
      props: { items, downloadHref: '/download', searchShortcut: '⌘K' },
      global: withLocale('en-US')
    });
    const trigger = wrapper.get('button[aria-haspopup="dialog"]');
    expect(trigger.text()).toContain('Search apps');
    expect(trigger.get('kbd').text()).toBe('⌘K');
    await trigger.trigger('click');
    await wrapper.get('button[aria-label="Search apps"]').trigger('click');
    expect(wrapper.emitted('search')).toHaveLength(2);
  });

  it('Toggles the hamburger menu and closes on link clicks or Escape', async () => {
    const wrapper = mount(OnTopNav, { props: { items, downloadHref: '/download' }, attachTo: document.body });
    const toggle = wrapper.get('button[aria-controls]');
    const panel = wrapper.get(`#${toggle.attributes('aria-controls')}`);
    expect(toggle.attributes('aria-expanded')).toBe('false');
    expect(panel.isVisible()).toBe(false);

    await toggle.trigger('click');
    expect(toggle.attributes('aria-expanded')).toBe('true');
    expect(toggle.attributes('aria-label')).toBe('关闭菜单');
    expect(panel.isVisible()).toBe(true);
    await panel.get('a').trigger('click');
    expect(panel.isVisible()).toBe(false);

    await toggle.trigger('click');
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await nextTick();
    expect(panel.isVisible()).toBe(false);
    expect(document.activeElement).toBe(toggle.element);
    wrapper.unmount();
  });

  it('Switches to a translucent blurred background after scrolling', async () => {
    const wrapper = mount(OnTopNav, { props: { items, downloadHref: '/download' } });
    expect(wrapper.classes()).toContain('bg-surface-page');
    Object.defineProperty(window, 'scrollY', { value: 120, configurable: true });
    window.dispatchEvent(new Event('scroll'));
    await nextTick();
    expect(wrapper.classes()).toContain('on-top-nav-scrolled');
    wrapper.unmount();
  });
});

// These interaction fixtures explicitly exercise the Chinese UI.
testConfig.global.provide = { ...testConfig.global.provide, [fixtureLocaleKey as symbol]: localeRef('zh-CN') };
