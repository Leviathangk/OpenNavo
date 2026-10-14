import { config as testConfig } from '@vue/test-utils';
import { ref as localeRef } from 'vue';
import { ON_UI_LOCALE as fixtureLocaleKey } from '../src/composables/locale';
import { mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import OnAppCard from '../src/components/OnAppCard.vue';
import OnAppRow from '../src/components/OnAppRow.vue';
import OnBreadcrumb from '../src/components/OnBreadcrumb.vue';
import OnCodeText from '../src/components/OnCodeText.vue';
import OnCategoryChips from '../src/components/OnCategoryChips.vue';
import OnEmpty from '../src/components/OnEmpty.vue';
import OnErrorState from '../src/components/OnErrorState.vue';
import OnHighlightText from '../src/components/OnHighlightText.vue';
import OnInstallCommand from '../src/components/OnInstallCommand.vue';
import OnKeyValueList from '../src/components/OnKeyValueList.vue';
import OnOffline from '../src/components/OnOffline.vue';
import OnPagination from '../src/components/OnPagination.vue';
import OnRankRow from '../src/components/OnRankRow.vue';
import OnSkeleton from '../src/components/OnSkeleton.vue';
import { codeParts, wordPieces } from '../src/utils/codeText';
import { commandBreak } from '../src/utils/command';
import { parseHighlight } from '../src/utils/highlight';
import { paginationRange } from '../src/utils/pagination';
import { packageSummary } from './fixtures';
import { RouterLinkStub, withLocale } from './helpers';

describe('Content helpers', () => {
  it('Highlights only double-asterisk emphasis', () => {
    expect(parseHighlight('近 30 天安装 **14,326** 次，**Cask 榜第 10**。')).toEqual([
      { text: '近 30 天安装 ', strong: false },
      { text: '14,326', strong: true },
      { text: ' 次，', strong: false },
      { text: 'Cask 榜第 10', strong: true },
      { text: '。', strong: false }
    ]);
    expect(parseHighlight('没有标记 *斜体* **未闭合')).toEqual([{ text: '没有标记 *斜体* **未闭合', strong: false }]);
    expect(parseHighlight('')).toEqual([]);
  });

  it('Shows at most seven pagination items including first, last, and current', () => {
    const view = (current: number, total: number) =>
      paginationRange(current, total).map(item => (item.type === 'page' ? item.page : '…'));
    expect(view(1, 5)).toEqual([1, 2, 3, 4, 5]);
    expect(view(1, 20)).toEqual([1, 2, 3, 4, 5, '…', 20]);
    expect(view(5, 20)).toEqual([1, '…', 4, 5, 6, '…', 20]);
    expect(view(17, 20)).toEqual([1, '…', 16, 17, 18, 19, 20]);
    expect(view(99, 20)).toEqual([1, '…', 16, 17, 18, 19, 20]);
    expect(view(0, 0)).toEqual([1]);
  });

  it('Splits long commands after --cask/--formula', () => {
    expect(commandBreak('brew install --cask uv')).toBeNull();
    expect(commandBreak('brew install --cask visual-studio-code')).toEqual({
      head: 'brew install --cask',
      tail: 'visual-studio-code'
    });
    expect(commandBreak('brew install --formula imagemagick-full')).toEqual({
      head: 'brew install --formula',
      tail: 'imagemagick-full'
    });
    expect(commandBreak('brew install --cask visual-studio-code', 80)).toBeNull();
    expect(commandBreak('brew bundle --file=~/Downloads/Brewfile --no-lock')).toBeNull();
  });

  it('Splits code text into words and long words after separators', () => {
    expect(codeParts('brew upgrade  --cask\n  code')).toEqual(['brew', ' ', 'upgrade', '  ', '--cask', '\n', '  code']);
    expect(codeParts('  "a",\n\n    "b"\n')).toEqual(['  "a",', '\n\n', '    "b"', '\n']);
    expect(wordPieces('visual-studio-code')).toEqual(['visual-studio-code']);
    expect(wordPieces('~/Library/com.apple.plist')).toEqual(['~/', 'Library/', 'com.', 'apple.', 'plist']);
    expect(wordPieces('--filter=a.b')).toEqual(['--filter=', 'a.', 'b']);
  });
});

describe('OnHighlightText', () => {
  it('Uses primary emphasis', () => {
    const wrapper = mount(OnHighlightText, { props: { text: '原生 macOS 界面，**GPU 加速渲染**。', as: 'p' } });
    expect(wrapper.element.tagName).toBe('P');
    expect(wrapper.get('.text-ink-primary').text()).toBe('GPU 加速渲染');
    expect(wrapper.text()).toBe('原生 macOS 界面，GPU 加速渲染。');
  });
});

describe('OnAppCard', () => {
  it('Renders card links, statistics, and defaults', async () => {
    const wrapper = mount(OnAppCard, { props: { pkg: packageSummary(), state: 'get', href: '/apps/docker-desktop' } });
    const link = wrapper.get('a');
    expect(link.attributes('href')).toBe('/apps/docker-desktop');
    expect(link.classes()).toContain('on-stretched');
    expect(wrapper.text()).toContain('20,043');
    expect(wrapper.text()).toContain('4.93.0');
    expect(wrapper.text()).not.toContain('240920');
    await wrapper.get('button').trigger('click');
    expect(wrapper.emitted('action')).toHaveLength(1);
  });

  it('Opens categories with English button labels', async () => {
    const wrapper = mount(OnAppCard, {
      props: { pkg: packageSummary(), state: 'update', stats: ['installs30d', 'category'], actionLabel: 'Update' },
      global: withLocale('en-US')
    });
    await wrapper.get('button.on-stretched').trigger('click');
    expect(wrapper.emitted('open')).toHaveLength(1);
    expect(wrapper.text()).toContain('30-day installs');
    expect(wrapper.text()).toContain('虚拟化与容器');
    expect(wrapper.text()).toContain('Update');
    const noCategory = mount(OnAppCard, {
      props: {
        pkg: packageSummary({ primaryCategory: null, summary: null }),
        state: 'running',
        progress: 40,
        stats: ['category']
      }
    });
    expect(noCategory.text()).toContain('—');
    expect(noCategory.find('p').exists()).toBe(false);
    await noCategory.get('[role="progressbar"]').trigger('click');
    expect(noCategory.emitted('cancel')).toHaveLength(1);
  });
});

describe('List rows', () => {
  it('Displays app row information', () => {
    const wrapper = mount(OnAppRow, {
      props: {
        kind: 'cask',
        token: 'ghostty',
        name: 'Ghostty',
        meta: '1.2.3 → 1.3.0',
        description: '终端模拟器',
        href: '/apps/ghostty'
      },
      slots: { badges: '<span class="badge">自带更新</span>', actions: '<button type="button">更新</button>' }
    });
    expect(wrapper.get('a').attributes('href')).toBe('/apps/ghostty');
    expect(wrapper.text()).toContain('1.2.3 → 1.3.0');
    expect(wrapper.find('.badge').exists()).toBe(true);
    expect(wrapper.get('button').text()).toBe('更新');
    const plain = mount(OnAppRow, { props: { kind: 'formula', token: 'uv', name: 'uv' } });
    expect(plain.find('a').exists()).toBe(false);
  });

  it('Displays ranking rows', () => {
    const wrapper = mount(OnRankRow, {
      props: { rank: 1, pkg: packageSummary(), value: '9.1万', href: '/apps/docker-desktop' }
    });
    expect(wrapper.text()).toContain('1');
    expect(wrapper.text()).toContain('9.1万');
    expect(wrapper.get('a').attributes('href')).toBe('/apps/docker-desktop');
    expect(
      mount(OnRankRow, { props: { rank: 2, pkg: packageSummary({ summary: null }), value: '2万' } })
        .find('a')
        .exists()
    ).toBe(false);
  });
});

describe('OnKeyValueList', () => {
  it('Uses monospace definition values and external links', () => {
    const wrapper = mount(OnKeyValueList, {
      props: {
        items: [
          { key: 'token', label: 'Token', value: 'visual-studio-code', mono: true },
          { key: 'homepage', label: '主页', value: 'code.visualstudio.com', href: 'https://code.visualstudio.com/' },
          { key: 'auto', label: '自动更新', value: '应用自带' }
        ]
      }
    });
    expect(wrapper.findAll('dt').map(dt => dt.text())).toEqual(['Token', '主页', '自动更新']);
    expect(wrapper.findAll('dd')[0]?.classes()).toContain('font-mono');
    const link = wrapper.get('a');
    expect(link.attributes('target')).toBe('_blank');
    expect(link.attributes('rel')).toBe('noopener noreferrer');
  });
});

describe('OnCodeText', () => {
  it('Keeps words whole and the text unchanged', () => {
    const text = 'brew upgrade --cask --greedy visual-studio-code';
    const wrapper = mount({
      components: { OnCodeText },
      template: `<code><OnCodeText :text="text" /></code>`,
      data: () => ({ text })
    });
    expect(wrapper.findAll('.on-code-word').map(word => word.text())).toEqual(text.split(' '));
    expect(wrapper.element.textContent).toBe(text);
    const path = mount({
      components: { OnCodeText },
      template: `<pre><OnCodeText text="~/Library/a b.plist\n  x" /></pre>`
    });
    expect(path.element.textContent).toBe('~/Library/a b.plist\n  x');
    expect(path.findAll('wbr')).toHaveLength(3);
  });
});

describe('OnInstallCommand', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('Copies the original wrapped command and restores the label after 1.5 seconds', async () => {
    const copy = vi.fn(async (_text: string) => {});
    const wrapper = mount(OnInstallCommand, { props: { command: 'brew install --cask visual-studio-code', copy } });
    // Both parts are always rendered; the row width decides in CSS whether the tail wraps.
    expect(wrapper.get('.on-command-head').text()).toBe('$ brew install --cask \\');
    expect(wrapper.get('.on-command-tail').text()).toBe('visual-studio-code');
    expect(wrapper.get('pre code').attributes('style')).toBe('--on-command-head: 21ch; --on-command-line: 40ch;');
    await wrapper.get('button').trigger('click');
    await Promise.resolve();
    expect(copy).toHaveBeenCalledWith('brew install --cask visual-studio-code');
    expect(wrapper.emitted('copied')?.[0]).toEqual(['brew install --cask visual-studio-code']);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').text()).toBe('已复制');
    vi.advanceTimersByTime(1500);
    await wrapper.vm.$nextTick();
    expect(wrapper.get('button').text()).toBe('复制');
  });

  it('Uses the default clipboard and emits failures', async () => {
    const writeText = vi.fn(async () => {
      throw new Error('denied');
    });
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    const wrapper = mount(OnInstallCommand, {
      props: { command: 'brew install --formula wget', title: '安装命令 · OpenNavo 实际执行的命令' }
    });
    expect(wrapper.text()).toContain('安装命令 · OpenNavo 实际执行的命令');
    await wrapper.get('button').trigger('click');
    await Promise.resolve();
    await Promise.resolve();
    expect(writeText).toHaveBeenCalledWith('brew install --formula wget');
    expect(wrapper.emitted('copyFailed')).toHaveLength(1);
    expect(wrapper.get('button').text()).toBe('复制');
  });
});

const chips = [
  { value: 'all', label: '全部' },
  { value: 'ai', label: 'AI 工具' },
  { value: 'dev', label: '开发' }
] as const;

describe('OnCategoryChips', () => {
  it('Supports keyboard navigation in radio groups', async () => {
    const wrapper = mount(OnCategoryChips, { props: { modelValue: 'ai', items: [...chips] }, attachTo: document.body });
    const radios = wrapper.findAll('[role="radio"]');
    expect(wrapper.get('[role="radiogroup"]').attributes('aria-label')).toBe('分类筛选');
    expect(radios[1]?.attributes('aria-checked')).toBe('true');
    expect(radios[1]?.classes()).toContain('bg-button-primary-bg');
    await wrapper.get('[role="radiogroup"]').trigger('keydown', { key: 'ArrowRight' });
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['dev']);
    await radios[0]?.trigger('click');
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual(['all']);
    await wrapper.get('[role="radiogroup"]').trigger('keydown', { key: 'a' });
    expect(wrapper.emitted('update:modelValue')).toHaveLength(2);
    wrapper.unmount();
  });

  it('Sets aria-current on router links', () => {
    const items = chips.map(item => ({ ...item, href: `/categories/${item.value}` }));
    const wrapper = mount(OnCategoryChips, { props: { modelValue: 'dev', items, ariaLabel: '分类' } });
    expect(wrapper.get('nav').attributes('aria-label')).toBe('分类');
    expect(wrapper.findAll('a')[2]?.attributes('aria-current')).toBe('page');
    const routed = mount(OnCategoryChips, { props: { modelValue: 'all', items, linkAs: RouterLinkStub } });
    expect(routed.get('a').attributes('href')).toBe('#/categories/all');
  });
});

describe('OnBreadcrumb', () => {
  it('Marks the last breadcrumb as current', () => {
    const wrapper = mount(OnBreadcrumb, {
      props: { items: [{ label: '发现', href: '/' }, { label: '开发' }, { label: 'Visual Studio Code' }] }
    });
    expect(wrapper.get('nav').attributes('aria-label')).toBe('面包屑');
    expect(wrapper.get('a').attributes('href')).toBe('/');
    expect(wrapper.get('[aria-current="page"]').text()).toBe('Visual Studio Code');
    expect(wrapper.findAll('svg')).toHaveLength(2);
    const routed = mount(OnBreadcrumb, {
      props: { items: [{ label: '发现', href: '/discover' }, { label: '详情' }], linkAs: RouterLinkStub }
    });
    expect(routed.get('a').attributes('href')).toBe('#/discover');
  });
});

describe('OnPagination', () => {
  it('Hides pagination for a single page', () => {
    expect(
      mount(OnPagination, { props: { page: 1, pageCount: 1 } })
        .find('nav')
        .exists()
    ).toBe(false);
  });

  it('Disables page buttons at boundaries', async () => {
    const wrapper = mount(OnPagination, { props: { page: 1, pageCount: 20 } });
    expect(wrapper.text()).toContain('…');
    expect(wrapper.get('[aria-current="page"]').text()).toBe('1');
    expect(wrapper.get('[aria-label="第 5 页"]').text()).toBe('5');
    await wrapper.get('[aria-current="page"]').trigger('click');
    await wrapper.get('[aria-disabled="true"]').trigger('click');
    expect(wrapper.emitted('update:page')).toBeUndefined();
    await wrapper.get('[aria-label="第 3 页"]').trigger('click');
    expect(wrapper.emitted('update:page')?.[0]).toEqual([3]);
    const next = wrapper.findAll('button').at(-1);
    await next?.trigger('click');
    expect(wrapper.emitted('update:page')?.[1]).toEqual([2]);
  });

  it('Builds page query links', async () => {
    const wrapper = mount(OnPagination, {
      props: { page: 20, pageCount: 20, hrefFor: (page: number) => `/apps?page=${page}` }
    });
    expect(wrapper.get('[aria-label="第 19 页"]').attributes('href')).toBe('/apps?page=19');
    expect(wrapper.findAll('[aria-disabled="true"]')).toHaveLength(1);
    const routed = mount(OnPagination, {
      props: { page: 2, pageCount: 3, hrefFor: (page: number) => `/apps?page=${page}`, linkAs: RouterLinkStub },
      global: withLocale('en-US')
    });
    expect(routed.get('[aria-label="Page 3"]').attributes('href')).toBe('#/apps?page=3');
    await routed.get('[aria-label="Page 1"]').trigger('click');
    expect(routed.emitted('update:page')?.[0]).toEqual([1]);
  });
});

describe('Empty states', () => {
  it('Renders OnEmpty parts', () => {
    const wrapper = mount(OnEmpty, {
      props: { title: '没有找到「vscodee」', description: '试试英文名或拼音。' },
      slots: { actions: '<button type="button">反馈</button>' }
    });
    expect(wrapper.get('h3').text()).toBe('没有找到「vscodee」');
    expect(wrapper.find('svg').exists()).toBe(true);
    expect(wrapper.get('button').text()).toBe('反馈');
    expect(
      mount(OnEmpty, { props: { title: 'x' } })
        .find('p')
        .exists()
    ).toBe(false);
  });

  it('Provides default retry behavior and request IDs in OnError', async () => {
    const wrapper = mount(OnErrorState, { props: { requestId: 'req-42' } });
    expect(wrapper.attributes('role')).toBe('alert');
    expect(wrapper.text()).toContain('加载失败');
    expect(wrapper.text()).toContain('req-42');
    await wrapper.get('button').trigger('click');
    expect(wrapper.emitted('retry')).toHaveLength(1);
    const plain = mount(OnErrorState, { props: { retryable: false, title: '无法连接', description: '稍后重试' } });
    expect(plain.find('button').exists()).toBe(false);
    expect(plain.text()).toContain('无法连接');
  });

  it('Provides OnOffline defaults', () => {
    const wrapper = mount(OnOffline, {
      global: withLocale('en-US'),
      slots: { actions: '<button type="button">Retry</button>' }
    });
    expect(wrapper.attributes('role')).toBe('status');
    expect(wrapper.text()).toContain('Offline');
    expect(wrapper.find('button').exists()).toBe(true);
  });
});

describe('OnSkeleton', () => {
  it('Renders multiline skeletons with a 60-percent final line and radius', () => {
    const wrapper = mount(OnSkeleton, { props: { lines: 3, radius: 'tiny', height: '12px' } });
    const rows = wrapper.findAll('.on-skeleton');
    expect(rows).toHaveLength(3);
    expect(rows[2]?.attributes('style')).toContain('width: 60%');
    expect(rows[0]?.classes()).toContain('rounded-tiny');
    expect(wrapper.attributes('aria-hidden')).toBe('true');
    expect(
      mount(OnSkeleton, { props: { radius: 'app-icon', width: '44px', height: '44px' } })
        .find('.rounded-app-icon')
        .exists()
    ).toBe(true);
  });
});

describe('Supports router slots', () => {
  it('Supports card links and action slots', () => {
    const wrapper = mount(OnAppCard, {
      props: { pkg: packageSummary(), state: 'get', href: '/apps/visual-studio-code', linkAs: RouterLinkStub },
      slots: { action: '<button type="button" class="custom-get">获取 ▾</button>' }
    });
    expect(wrapper.get('a').attributes('href')).toBe('#/apps/visual-studio-code');
    expect(wrapper.find('.custom-get').exists()).toBe(true);
    expect(wrapper.findAll('button').map(button => button.text())).toEqual(['获取 ▾']);
  });

  it('Supports ranking links', () => {
    const wrapper = mount(OnRankRow, {
      props: {
        rank: 1,
        pkg: packageSummary(),
        value: '1.7万',
        href: '/apps/visual-studio-code',
        linkAs: RouterLinkStub
      }
    });
    expect(wrapper.get('a').attributes('href')).toBe('#/apps/visual-studio-code');
  });
});

describe('OnCollectionCard', () => {
  it('Renders collection icon fallbacks, titles, and buttons', async () => {
    const { default: OnCollectionCard } = await import('../src/components/OnCollectionCard.vue');
    const wrapper = mount(OnCollectionCard, {
      props: {
        title: '新 Mac 必装',
        subtitle: '开发、沟通与日常效率工具',
        count: 15,
        href: '/collections/new-mac',
        linkAs: RouterLinkStub,
        icons: [
          { kind: 'cask', token: 'ghostty', name: 'Ghostty' },
          { src: 'https://cdn.example.com/icons/raycast.png' },
          {},
          { kind: 'formula', token: 'uv', name: 'uv' },
          { kind: 'cask', token: 'iina', name: 'IINA' },
          { kind: 'cask', token: 'extra', name: 'Extra' }
        ]
      }
    });
    expect(wrapper.findAll('.on-collection-icon')).toHaveLength(5);
    expect(wrapper.find('img[src="https://cdn.example.com/icons/raycast.png"]').exists()).toBe(true);
    expect(wrapper.get('h3').text()).toBe('新 Mac 必装');
    const link = wrapper.get('a');
    expect(link.attributes('href')).toBe('#/collections/new-mac');
    expect(link.text()).toBe('查看全部 15 款');
  });
});

describe('Renders ranking changes and app links', () => {
  it('Uses green for rises, red for falls, and an accessible dash for no change', () => {
    const view = (change: number | null) => {
      const wrapper = mount(OnRankRow, { props: { rank: 3, pkg: packageSummary(), value: '17,394', change } });
      const cell = wrapper.findAll('span').find(span => span.classes().includes('w-36px'));
      return {
        tone: cell?.classes().find(name => name.startsWith('text-status') || name === 'text-ink-tertiary'),
        text: cell?.text()
      };
    };
    expect(view(3)).toEqual({ tone: 'text-status-success', text: '3上升 3 位' });
    expect(view(-2)).toEqual({ tone: 'text-status-danger', text: '2下降 2 位' });
    expect(view(0)).toEqual({ tone: 'text-ink-tertiary', text: '—排名不变' });
    expect(view(null)).toEqual({ tone: 'text-ink-tertiary', text: '—排名不变' });
    // Omitting change hides the change column, as in homepage weekly rankings.
    const plain = mount(OnRankRow, { props: { rank: 1, pkg: packageSummary(), value: '1.7万' } });
    expect(plain.findAll('span').some(span => span.classes().includes('w-36px'))).toBe(false);
  });

  it('Supports slots and link navigation', async () => {
    const rank = mount(OnRankRow, {
      props: { rank: 1, pkg: packageSummary(), value: '1', change: 1 },
      slots: { action: '<button type="button">获取</button>' }
    });
    expect(rank.get('button').text()).toBe('获取');

    const row = mount(OnAppRow, {
      props: {
        kind: 'cask',
        token: 'visual-studio-code',
        name: 'Visual Studio Code',
        href: '/apps/visual-studio-code',
        linkAs: RouterLinkStub
      }
    });
    const link = row.get('a');
    expect(link.attributes('href')).toBe('#/apps/visual-studio-code');
    await link.trigger('click');
    expect(row.emitted('navigate')).toHaveLength(1);
  });
});

// These interaction fixtures explicitly exercise the Chinese UI.
testConfig.global.provide = { ...testConfig.global.provide, [fixtureLocaleKey as symbol]: localeRef('zh-CN') };
