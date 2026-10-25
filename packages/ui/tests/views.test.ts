import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import OnAboutSection from '../src/components/OnAboutSection.vue';
import OnAppShell from '../src/components/OnAppShell.vue';
import OnCategoriesView from '../src/components/OnCategoriesView.vue';
import OnCollectionView from '../src/components/OnCollectionView.vue';
import OnCollectionsView from '../src/components/OnCollectionsView.vue';
import OnDiscoverView from '../src/components/OnDiscoverView.vue';
import OnPackageHeader from '../src/components/OnPackageHeader.vue';
import OnPackageListView from '../src/components/OnPackageListView.vue';
import OnPackageNotice from '../src/components/OnPackageNotice.vue';
import OnPackageOverview from '../src/components/OnPackageOverview.vue';
import OnPackageStats from '../src/components/OnPackageStats.vue';
import OnRailPanel from '../src/components/OnRailPanel.vue';
import OnRankingsView from '../src/components/OnRankingsView.vue';
import OnSearchResultsView from '../src/components/OnSearchResultsView.vue';
import OnSidebar from '../src/components/OnSidebar.vue';
import OnToolbar from '../src/components/OnToolbar.vue';
import { packageSummary } from './fixtures';
import { RouterLinkStub, withLocale } from './helpers';

// Shared desktop/web shells and views (ADR-017): presentation only; host chooses link behavior through linkAs.

const labels = {
  categories: '按分类浏览',
  popularApps: '热门 App',
  popularAppsHint: '近 30 天安装量',
  viewRankings: '查看排行榜',
  recentlyUpdated: '最近更新',
  recentlyUpdatedHint: '刚发布新版本',
  collections: '合集',
  viewAll: '查看全部'
};

describe('OnAppShell', () => {
  it('In container mode, scrolls the main area independently and keeps the right panel persistent or opens it as an overlay', () => {
    const docked = mount(OnAppShell, {
      props: { hasRail: true, railDocked: true },
      slots: { default: () => h('p', '内容'), rail: () => h('aside', { class: 'rail' }, '右栏') }
    });
    expect(docked.get('main').classes()).toContain('overflow-y-auto');
    expect(docked.get('.rail').element.parentElement?.className).toContain('w-288px');

    const overlay = mount(OnAppShell, {
      props: { hasRail: true, railDocked: false, railOpen: true },
      slots: { rail: () => h('aside', { class: 'rail' }) }
    });
    expect(overlay.get('.rail').element.parentElement?.className).toContain('shadow-popover');
    const closed = mount(OnAppShell, {
      props: { hasRail: true, railDocked: false, railOpen: false },
      slots: { rail: () => h('aside', { class: 'rail' }) }
    });
    expect(closed.find('.rail').exists()).toBe(false);
  });

  it('In document mode, places the mobile menu and footer after the main area and right panel and renders the panel once', () => {
    const wrapper = mount(OnAppShell, {
      props: { scroll: 'document', hasRail: true },
      slots: {
        compactBar: () => h('nav', { class: 'compact' }),
        default: () => h('p', { class: 'page' }),
        rail: () => h('aside', { class: 'rail' }),
        footer: () => h('footer', { class: 'site-footer' })
      }
    });
    expect(wrapper.get('.compact').element.parentElement?.className).toContain('md:hidden');
    expect(wrapper.findAll('.rail')).toHaveLength(1);
    expect(wrapper.get('.rail').element.parentElement?.className).toContain('xl:sticky');
    const html = wrapper.html();
    expect(html.indexOf('page')).toBeLessThan(html.indexOf('rail'));
    expect(html.indexOf('rail')).toBeLessThan(html.indexOf('site-footer'));
  });
});

describe('OnSidebar and OnToolbar', () => {
  it('Renders grouped navigation with linkAs, aria-current, counts, and badges', () => {
    const wrapper = mount(OnSidebar, {
      props: {
        label: '主导航',
        linkAs: RouterLinkStub,
        groups: [
          {
            key: 'browse',
            label: '浏览',
            items: [
              { key: 'discover', label: '发现', href: '/discover', active: true },
              { key: 'updates', label: '更新', href: '/updates', badge: 4, badgeLabel: '4 个更新' }
            ]
          }
        ],
        footerItems: [{ key: 'settings', label: '设置', href: '/settings' }]
      },
      slots: { top: () => h('div', { class: 'top-card' }) }
    });
    const links = wrapper.findAll('a');
    expect(links.map(link => link.attributes('href'))).toEqual(['#/discover', '#/updates', '#/settings']);
    expect(links[0]?.attributes('aria-current')).toBe('page');
    expect(wrapper.text()).toContain('4');
    expect(wrapper.find('.top-card').exists()).toBe(true);
    expect(wrapper.find('[data-tauri-drag-region]').exists()).toBe(false);
  });

  it('Emits search from the toolbar search box and makes the entire desktop toolbar draggable', async () => {
    const wrapper = mount(OnToolbar, { props: { searchLabel: '搜索 App', searchShortcut: '⌘K', dragRegion: true } });
    expect(wrapper.attributes('data-tauri-drag-region')).toBe('');
    await wrapper.get('button').trigger('click');
    expect(wrapper.emitted('search')).toHaveLength(1);
    expect(wrapper.get('kbd').text()).toBe('⌘K');
  });

  it('Uses a top border on narrow web sidebars and a left border at xl and above', () => {
    const wrapper = mount(OnRailPanel, { props: { responsive: true } });
    expect(wrapper.classes()).toContain('border-t');
    expect(wrapper.classes()).toContain('xl:border-l');
    expect(mount(OnRailPanel).classes()).toContain('border-l');
  });
});

describe('OnDiscoverView', () => {
  it('Keeps both sections at three responsive rows with two empty final slots', async () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get');
    try {
      for (const [pixels, count] of [
        [1612, 19],
        [916, 10],
        [684, 7],
        [452, 5],
        [220, 3]
      ]) {
        width.mockReturnValue(pixels!);
        const apps = Array.from({ length: 30 }, (_, index) => ({ ...packageSummary(), token: `app-${index}` }));
        const wrapper = mount(OnDiscoverView, {
          props: {
            chips: [],
            popular: apps,
            recent: apps,
            popularRows: 3,
            labels,
            rankingsHref: '/rankings',
            recentHref: '/recent',
            collectionsHref: '/collections'
          },
          slots: { card: ({ section }: { section: string }) => h('div', { 'data-section': section }) },
          global: withLocale('zh-CN')
        });
        await nextTick();
        expect(wrapper.findAll('[data-section="popular"]')).toHaveLength(count!);
        expect(wrapper.findAll('[data-section="recent"]')).toHaveLength(count!);
        wrapper.unmount();
      }
    } finally {
      width.mockRestore();
    }
  });

  it('Displays a primary pick and clickable secondary tiles with app, collection, and external destinations', () => {
    const wrapper = mount(OnDiscoverView, {
      props: {
        hero: { badge: '精选', title: 'Visual Studio Code', icons: [] },
        features: [
          {
            key: 2,
            badge: '精选',
            title: 'Ghostty',
            description: '使用 **Ghostty**',
            icons: [],
            href: '/apps/ghostty',
            detailLabel: '查看详情'
          },
          { key: 3, badge: '精选', title: '新 Mac', icons: [], href: '/collections/new-mac', detailLabel: '查看合集' },
          {
            key: 4,
            badge: '精选',
            title: '官方网站',
            icons: [],
            href: 'https://example.com',
            external: true,
            detailLabel: '访问网站'
          }
        ],
        chips: [],
        popular: [],
        recent: [],
        labels,
        rankingsHref: '/rankings',
        recentHref: '/recent',
        collectionsHref: '/collections',
        linkAs: RouterLinkStub
      },
      slots: { heroActions: '<a href="/apps/visual-studio-code">查看详情</a>' },
      global: withLocale('zh-CN')
    });
    expect(wrapper.findAll('h2').map(node => node.text())).toEqual(['精选']);
    expect(wrapper.findComponent({ name: 'OnFeatureHero' }).get('h3').text()).toBe('Visual Studio Code');
    expect(wrapper.findAll('.on-discover-feature h3').map(node => node.text())).toEqual([
      'Ghostty',
      '新 Mac',
      '官方网站'
    ]);
    const links = wrapper.findAll('a.on-discover-feature');
    expect(links.map(link => link.attributes('href'))).toEqual([
      '#/apps/ghostty',
      '#/collections/new-mac',
      'https://example.com'
    ]);
    expect(links[1]?.text()).toContain('新 Mac');
    expect(wrapper.findAll('.on-discover-feature button')).toHaveLength(0);
    expect(links[0]?.attributes('target')).toBeUndefined();
    expect(links[2]?.attributes('target')).toBe('_blank');
    expect(links[2]?.attributes('rel')).toBe('noopener noreferrer');
    expect(wrapper.find('.on-discover-feature p span').text()).toBe('Ghostty');
  });

  it('Fills up to three secondary slots from collections without repeating the primary or editorial destinations', () => {
    const collection = (key: string) => ({ key, title: key, count: 2, icons: [], href: `/collections/${key}` });
    const wrapper = mount(OnDiscoverView, {
      props: {
        hero: { badge: '精选合集', title: 'Main', icons: [], href: '/collections/main' },
        features: [
          { key: 1, badge: '', title: 'Duplicate main', icons: [], href: '/collections/main', detailLabel: 'View' },
          { key: 2, badge: '', title: 'Editorial', icons: [], href: '/collections/editorial', detailLabel: 'View' },
          {
            key: 3,
            badge: '',
            title: 'Duplicate editorial',
            icons: [],
            href: '/collections/editorial',
            detailLabel: 'View'
          }
        ],
        collections: ['main', 'editorial', 'third', 'fourth', 'overflow'].map(collection),
        chips: [],
        popular: [],
        recent: [],
        labels,
        rankingsHref: '/rankings',
        recentHref: '/recent',
        collectionsHref: '/collections'
      }
    });
    expect(wrapper.findAll('a.on-discover-feature').map(node => node.attributes('href'))).toEqual([
      '/collections/editorial',
      '/collections/third',
      '/collections/fourth'
    ]);
    expect(wrapper.find('a[href="/collections"]').text()).toContain('查看全部');
  });

  it('Shows loading skeletons, renders loaded sections through host card slots, and includes recent-update statistics', () => {
    const loading = mount(OnDiscoverView, {
      props: {
        chips: [],
        popular: null,
        recent: null,
        labels,
        rankingsHref: '/rankings',
        recentHref: '/recent',
        collectionsHref: '/c'
      }
    });
    expect(loading.find('[aria-hidden="true"]').exists()).toBe(true);

    const popular = [packageSummary({ token: 'a' }), packageSummary({ token: 'b' })];
    const recent = [packageSummary({ token: 'c' })];
    const wrapper = mount(OnDiscoverView, {
      props: {
        chips: [
          { value: 'all', label: '全部', href: '/' },
          { value: 'dev', label: '开发', href: '/categories/dev' }
        ],
        popular,
        recent,
        labels,
        rankingsHref: '/rankings',
        recentHref: '/recent',
        collectionsHref: '/collections',
        collections: [{ key: 'new', title: '新 Mac 必备', count: 6, icons: [], href: '/collections/new' }],
        linkAs: RouterLinkStub
      },
      slots: {
        card: ({ pkg, section, stats }) =>
          h('div', { class: 'card', 'data-section': section, 'data-stats': stats?.join(',') ?? '' }, pkg.token)
      },
      global: withLocale('zh-CN')
    });
    const cards = wrapper.findAll('.card');
    expect(cards.map(card => card.text())).toEqual(['a', 'b', 'c']);
    expect(cards[2]?.attributes('data-stats')).toBe('version,installs30d');
    expect(cards[0]?.attributes('data-stats')).toBe('version,installs30d');
    expect(wrapper.text()).toContain('新 Mac 必备');
    expect(wrapper.find('a[href="#/rankings"]').exists()).toBe(true);
    // Recently Updated links its full list from the section header, like Popular Apps links Rankings.
    const recentHeader = wrapper.findAll('section')[1];
    expect(recentHeader?.get('h2').text()).toContain('最近更新');
    expect(recentHeader?.get('a[href="#/recent"]').text()).toContain('查看全部');
  });
});

describe('List views', () => {
  it('Rankings include period segments, one action slot per row, and rank changes', async () => {
    const wrapper = mount(OnRankingsView, {
      props: {
        title: '排行榜',
        periods: [
          { value: '30d', label: '30 天' },
          { value: '90d', label: '90 天' }
        ],
        period: '30d',
        periodLabel: '周期',
        entries: [
          { rank: 1, change: 2, pkg: packageSummary({ token: 'a' }), value: '9 万', href: '/a' },
          { rank: 2, pkg: packageSummary({ token: 'b' }), value: '8 万', href: '/b' }
        ],
        linkAs: RouterLinkStub
      },
      slots: { action: ({ entry }) => h('button', { class: 'get' }, entry.pkg.token) },
      global: withLocale('zh-CN')
    });
    expect(wrapper.findAll('li')).toHaveLength(2);
    expect(wrapper.findAll('.get').map(button => button.text())).toEqual(['a', 'b']);
    expect(wrapper.get('ol').classes()).toContain('xl:grid-cols-2');
    await wrapper.findAll('[role="radio"]')[1]?.trigger('click');
    expect(wrapper.emitted('update:period')?.[0]).toEqual(['90d']);
  });

  it('App lists include breadcrumbs, sorting, and card slots and use the empty slot without data', async () => {
    const wrapper = mount(OnPackageListView, {
      props: {
        breadcrumb: [{ label: '分类', href: '/categories' }, { label: '开发' }],
        title: '开发',
        sorts: [
          { value: 'popular', label: '热门' },
          { value: 'name', label: '名称' }
        ],
        sort: 'popular',
        sortLabel: '排序',
        items: [packageSummary()],
        linkAs: RouterLinkStub
      },
      slots: { card: ({ pkg }) => h('div', { class: 'card' }, pkg.token), filters: () => h('span', { class: 'flt' }) }
    });
    expect(wrapper.get('nav a').attributes('href')).toBe('#/categories');
    expect(wrapper.find('.flt').exists()).toBe(true);
    expect(wrapper.get('.card').text()).toBe('docker-desktop');
    await wrapper.findAll('[role="radio"]')[1]?.trigger('click');
    expect(wrapper.emitted('update:sort')?.[0]).toEqual(['name']);

    const empty = mount(OnPackageListView, {
      props: { title: '开发', items: [] },
      slots: { card: () => h('div'), empty: () => h('p', { class: 'none' }, '没有') }
    });
    expect(empty.find('.none').exists()).toBe(true);
  });

  it('Category overviews and collection lists include card links and loading skeletons', () => {
    const categories = mount(OnCategoriesView, {
      props: {
        title: '分类',
        items: [
          {
            key: 'dev',
            name: '开发',
            icon: 'lucide:code-xml',
            count: '12 个',
            description: '编辑器与工具',
            children: ['编辑器', '终端'],
            href: '/categories/dev'
          }
        ],
        linkAs: RouterLinkStub
      }
    });
    expect(categories.get('a').attributes('href')).toBe('#/categories/dev');
    expect(categories.text()).toContain('编辑器 · 终端');
    expect(categories.text()).toContain('编辑器与工具');
    expect(
      mount(OnCategoriesView, { props: { title: '分类', items: null } })
        .find('ul')
        .exists()
    ).toBe(false);

    const collections = mount(OnCollectionsView, {
      props: {
        title: '合集',
        items: [{ key: 'new', title: '新 Mac 必备', count: 6, icons: [], href: '/collections/new' }],
        linkAs: RouterLinkStub
      },
      global: withLocale('zh-CN')
    });
    expect(collections.text()).toContain('新 Mac 必备');
    expect(collections.find('a[href="#/collections/new"]').exists()).toBe(true);
  });

  it('Collection details include body slots, recommendations, and one action per item, with loading skeletons before data arrives', () => {
    const wrapper = mount(OnCollectionView, {
      props: {
        breadcrumb: [{ label: '合集', href: '/collections' }, { label: '新 Mac 必备' }],
        title: '新 Mac 必备',
        subtitle: '装好日常所需',
        meta: '6 款',
        entries: [
          { key: 'cask/a', kind: 'cask', token: 'a', name: 'A', description: '推荐语', href: '/apps/a' },
          { key: 'cask/b', kind: 'cask', token: 'b', name: 'B', href: '/apps/b' }
        ],
        linkAs: RouterLinkStub
      },
      slots: { body: () => h('p', { class: 'body' }, '正文'), action: ({ entry }) => h('button', entry.token) }
    });
    expect(wrapper.find('.body').exists()).toBe(true);
    expect(wrapper.text()).toContain('推荐语');
    expect(wrapper.text()).toContain('6 款');
    expect(wrapper.findAll('li button').map(button => button.text())).toEqual(['a', 'b']);

    const loading = mount(OnCollectionView, { props: { breadcrumb: [], title: null, entries: [], loading: true } });
    expect(loading.find('ul').exists()).toBe(false);
    expect(loading.find('[aria-hidden="true"]').exists()).toBe(true);
  });

  it('Search results label matched aliases and report result positions on click', async () => {
    const wrapper = mount(OnSearchResultsView, {
      props: {
        title: '「code」的搜索结果',
        hint: '也搜索了：visual studio code',
        results: [
          { key: 'cask/a', kind: 'cask', token: 'a', name: 'A', matched: '匹配：VS Code', href: '/apps/a' },
          { key: 'cask/b', kind: 'cask', token: 'b', name: 'B', href: '/apps/b' }
        ],
        linkAs: RouterLinkStub
      },
      slots: { search: () => h('input', { class: 'q' }) }
    });
    expect(wrapper.find('.q').exists()).toBe(true);
    expect(wrapper.text()).toContain('也搜索了：visual studio code');
    expect(wrapper.text()).toContain('匹配：VS Code');
    await wrapper.findAll('li a')[1]?.trigger('click');
    expect(wrapper.emitted('navigate')?.[0]?.[1]).toBe(1);

    const empty = mount(OnSearchResultsView, {
      props: { title: '搜索', results: null },
      slots: { empty: () => h('p', { class: 'none' }) }
    });
    expect(empty.find('.none').exists()).toBe(true);
  });
});

describe('Detail views', () => {
  it('Headers include names, labels, action slots, and source captions', () => {
    const wrapper = mount(OnPackageHeader, {
      props: {
        kind: 'cask',
        token: 'ghostty',
        name: 'Ghostty',
        subtitle: 'Ghostty · 终端模拟器',
        chips: [
          { key: 'kind', label: 'Cask' },
          { key: 'installed', label: '已安装 1.3.1', tone: 'success', dot: true }
        ],
        meta: '来自 homebrew/cask'
      },
      slots: { actions: () => h('button', { class: 'get' }, '获取') }
    });
    expect(wrapper.get('h1').text()).toBe('Ghostty');
    expect(wrapper.text()).toContain('已安装 1.3.1');
    expect(wrapper.find('.get').exists()).toBe(true);
    expect(wrapper.text()).toContain('来自 homebrew/cask');
  });

  it('Notices link replacement packages with detail pages and otherwise show names only', () => {
    const linked = mount(OnPackageNotice, {
      props: {
        tone: 'danger',
        text: '已停用',
        replacementLabel: '替代：b',
        replacementHref: '/apps/b',
        linkAs: RouterLinkStub
      }
    });
    expect(linked.get('a').attributes('href')).toBe('#/apps/b');
    const plain = mount(OnPackageNotice, {
      props: { tone: 'warning', text: '已弃用', replacementLabel: '替代：ripgrep' }
    });
    expect(plain.find('a').exists()).toBe(false);
    expect(plain.text()).toContain('替代：ripgrep');
    expect(plain.classes()).toContain('text-status-warning');
  });

  it('Renders data cells without a left border on the first cell', () => {
    const wrapper = mount(OnPackageStats, {
      props: {
        label: '数据',
        cells: [
          { key: 'd30', label: '近 30 天安装', value: '17,394' },
          { key: 'size', label: '下载大小', value: '318', unit: 'MB' }
        ]
      }
    });
    expect(wrapper.attributes('aria-label')).toBe('数据');
    expect(wrapper.text()).toContain('17,394');
    expect(wrapper.text()).toContain('MB');
  });

  it('Introductions include title and body slots, label machine translations with original links, and omit expansion below 12 lines', () => {
    const wrapper = mount(OnAboutSection, {
      props: { machineTranslated: true, originalHref: '/apps/a', linkAs: RouterLinkStub },
      slots: { default: () => h('p', '介绍正文') },
      global: withLocale('zh-CN')
    });
    expect(wrapper.get('h2').text()).toBe('介绍');
    expect(wrapper.text()).toContain('AI 翻译');
    expect(wrapper.get('a').attributes('href')).toBe('#/apps/a');
    expect(wrapper.text()).toContain('介绍正文');
    expect(wrapper.find('button').exists()).toBe(false);

    const english = mount(OnAboutSection, { slots: { default: () => h('p', 'Body') }, global: withLocale('en-US') });
    expect(english.get('h2').text()).toBe('About');
    expect(english.text()).not.toContain('AI translated');
  });

  it('Overviews include introductions, host sections, installation commands, information, and similar apps', async () => {
    const wrapper = mount(OnPackageOverview, {
      props: {
        hasAbout: true,
        caveats: '需要重启',
        caveatsTitle: '注意事项',
        command: 'brew install --cask ghostty',
        info: [{ key: 'token', label: 'Token', value: 'ghostty', mono: true }],
        related: [{ key: 'cask/a', kind: 'cask', token: 'a', name: 'A', description: '简介', href: '/apps/a' }],
        relatedTitle: '相似应用',
        linkAs: RouterLinkStub
      },
      slots: {
        about: () => h('p', '介绍正文'),
        main: () => h('div', { class: 'release' }),
        aside: () => h('div', { class: 'installs' })
      },
      global: withLocale('zh-CN')
    });
    expect(wrapper.text()).toContain('介绍正文');
    expect(wrapper.find('.release').exists()).toBe(true);
    expect(wrapper.find('.installs').exists()).toBe(true);
    expect(wrapper.text()).toContain('需要重启');
    expect(wrapper.text()).toContain('brew install --cask ghostty');
    expect(wrapper.find('a[href="#/apps/a"]').text()).toBe('A');
  });

  it('Overviews pass the app name on to screenshots without a caption', () => {
    const screenshots = [{ src: '/shots/1.png' }, { src: '/shots/2.png', caption: '设置' }];
    const alts = (props: Record<string, unknown>) =>
      mount(OnPackageOverview, { props: { info: [], screenshots, ...props }, global: withLocale('zh-CN') })
        .findAll('img')
        .map(image => image.attributes('alt'));
    expect(alts({ screenshotsName: 'Ghostty' })).toEqual(['Ghostty · 截图 1', '设置']);
    // Without a name (the desktop app's callers), screenshots keep their numbered names.
    expect(alts({})).toEqual(['截图 1', '设置']);
  });
});

describe('Responsive app-list pagination', () => {
  it('Requests complete rows for narrow and wide containers without dropping supplied items', () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get');
    try {
      for (const [pixels, size] of [
        [260, 24],
        [1076, 24],
        [1892, 28],
        [2164, 24]
      ]) {
        width.mockReturnValue(pixels!);
        const items = Array.from({ length: 24 }, (_, i) => packageSummary({ token: `app-${i}` }));
        const wrapper = mount(OnPackageListView, {
          props: { title: 'Apps', items },
          slots: { card: ({ pkg }: { pkg: { token: string } }) => h('div', { 'data-app': pkg.token }) }
        });
        expect(wrapper.emitted('pageSize')).toEqual([[size]]);
        expect(wrapper.findAll('[data-app]')).toHaveLength(24);
        wrapper.unmount();
      }
    } finally {
      width.mockRestore();
    }
  });
  it('Keeps one page size when the host turns width fitting off (web)', () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(1892);
    try {
      const items = Array.from({ length: 24 }, (_, i) => packageSummary({ token: `app-${i}` }));
      const wrapper = mount(OnPackageListView, {
        props: { title: 'Apps', items, fitToWidth: false },
        slots: { card: ({ pkg }: { pkg: { token: string } }) => h('div', { 'data-app': pkg.token }) }
      });
      expect(wrapper.emitted('pageSize')).toBeUndefined();
      expect(wrapper.findAll('[data-app]')).toHaveLength(24);
      wrapper.unmount();
    } finally {
      width.mockRestore();
    }
  });
});

describe('Headings hold only their titles (C3)', () => {
  it('Puts a section header’s hint beside its h2, not inside it', async () => {
    const { default: OnSectionHeader } = await import('../src/components/OnSectionHeader.vue');
    const wrapper = mount(OnSectionHeader, { props: { title: '热门 App', hint: '近 30 天安装量' } });
    expect(wrapper.get('h2').text()).toBe('热门 App');
    expect(wrapper.get('p').text()).toBe('近 30 天安装量');
  });

  it('Reads the stats strip as a description list', () => {
    const wrapper = mount(OnPackageStats, {
      props: { label: '数据', cells: [{ key: 'size', label: '下载大小', value: '318', unit: 'MB', hint: 'dmg' }] }
    });
    expect(wrapper.get('dt').text()).toBe('下载大小');
    expect(wrapper.findAll('dd').map(item => item.text())).toEqual(['318MB', 'dmg']);
  });

  it('Links header chips that carry an address', () => {
    const wrapper = mount(OnPackageHeader, {
      props: {
        kind: 'cask',
        token: 'iterm2',
        name: 'iTerm2',
        chips: [
          { key: 'kind', label: 'Cask' },
          { key: 'category:terminal', label: '终端', href: '/categories/terminal' }
        ],
        linkAs: RouterLinkStub
      }
    });
    expect(wrapper.findAll('a').map(link => [link.text(), link.attributes('href')])).toEqual([
      ['终端', '#/categories/terminal']
    ]);
  });
});
