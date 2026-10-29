import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { RouterView } from 'vue-router';
import { createOnUi, OnPagination } from '@opennavo/ui';
import { i18n } from '@/i18n';
import { refreshPage } from '@/composables/usePageRefresh';
import ArticlePage from '@/pages/ArticlePage.vue';
import ArticlesPage from '@/pages/ArticlesPage.vue';
import type { Article, ArticleCard } from '@/utils/article';
import { articlePage, articleType } from '@/utils/article';
import { setup } from './helpers';

const { openExternal } = vi.hoisted(() => ({ openExternal: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@/utils/external', () => ({ openExternal }));
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  openExternal.mockClear();
});
const article: Article = {
  slug: 'screenshot-guide',
  type: 'tutorial',
  title: 'Mac screenshot guide',
  summary: 'Choose a screenshot workflow.',
  bodyMarkdown:
    '## Capture\n\nUse the keyboard.\n\n[Sources](#sources)\n\n[Documentation](https://example.com/docs)\n\n[Another guide](/articles/another-guide)\n\n<script>alert(1)</script>\n\n## Sources\n\nCompare your options.',
  testNote: null,
  seoTitle: null,
  seoDescription: null,
  locale: 'zh-CN',
  sourceLocale: 'en-US',
  machineTranslated: false,
  availableLocales: ['zh-CN', 'en-US'],
  author: {
    name: 'Example Editor',
    slug: 'example-editor',
    role: 'Editor',
    bio: 'A test author.',
    avatarUrl: null,
    links: []
  },
  cover: null,
  sources: [{ title: 'Documentation', url: 'https://example.com/docs' }],
  packages: [],
  images: [],
  related: [],
  publishedAt: '2026-10-12T10:00:00Z',
  updatedAt: '2026-10-14T10:00:00Z',
  readingMinutes: 4,
  revision: 2
};
const card: ArticleCard = { ...article };
function ok(data: unknown) {
  return new Response(JSON.stringify({ code: '0000', msg: 'ok', data }), {
    headers: { 'Content-Type': 'application/json' }
  });
}
function stubFetch(handler: (url: URL) => Response | Promise<Response>) {
  return vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL) => handler(new URL(input instanceof Request ? input.url : String(input))))
  );
}
async function mounted(path = '/articles') {
  const { pinia, router } = await setup('/discover');
  router.addRoute({ path: '/articles', name: 'articles', component: ArticlesPage, meta: { nav: 'articles' } });
  router.addRoute({
    path: '/articles/:slug',
    name: 'article',
    component: ArticlePage,
    props: true,
    meta: { nav: 'articles' }
  });
  await router.push(path);
  const wrapper = mount(RouterView, {
    attachTo: document.body,
    global: { plugins: [pinia, router, i18n, createOnUi({ locale: i18n.global.locale })] }
  });
  wrappers.push(wrapper);
  await flushPromises();
  return { wrapper, router };
}

describe('desktop article browsing', () => {
  it('normalizes invalid query values', () => {
    expect(articleType('tutorial')).toBe('tutorial');
    expect(articleType(['news'])).toBeUndefined();
    for (const value of ['-1', 'NaN', '1.5', '9007199254740993', ['2']]) expect(articlePage(value)).toBe(1);
  });

  it('filters and pages via route state, preserving the list when returning from a detail', async () => {
    const requests: URL[] = [];
    stubFetch(url => {
      requests.push(url);
      if (url.pathname.endsWith('/articles/screenshot-guide'))
        return ok({ state: 'published', slug: article.slug, article });
      return ok({ current: Number(url.searchParams.get('current')), size: 12, total: 25, records: [card] });
    });
    const { wrapper, router } = await mounted();
    expect(wrapper.text()).toContain(article.title);
    await wrapper.get('a[href="/articles?type=tutorial"]').trigger('click');
    await flushPromises();
    expect(requests.at(-1)?.searchParams.get('type')).toBe('tutorial');
    wrapper.getComponent(OnPagination).vm.$emit('update:page', 2);
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual({ type: 'tutorial', page: '2' });
    expect(requests.at(-1)?.searchParams.get('current')).toBe('2');
    await wrapper.get('[data-testid="article-card"] a').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="article-body"]').exists()).toBe(true);
    router.back();
    await flushPromises();
    expect(router.currentRoute.value.query).toEqual({ type: 'tutorial', page: '2' });
  });

  it('recovers from an offline error and shows a distinct empty state', async () => {
    let online = false;
    stubFetch(() => {
      if (!online) throw new TypeError('offline');
      return ok({ records: [], total: 0, current: 1, size: 12 });
    });
    const { wrapper } = await mounted();
    expect(wrapper.findComponent({ name: 'OnErrorState' }).exists()).toBe(true);
    expect(wrapper.text()).not.toContain('还没有文章');
    online = true;
    await refreshPage();
    await flushPromises();
    expect(wrapper.text()).toContain('还没有文章');
    expect(wrapper.findComponent({ name: 'OnErrorState' }).exists()).toBe(false);
  });

  it('renders safe Markdown, preserves hash routing for contents, and routes links correctly', async () => {
    stubFetch(() => ok({ state: 'published', slug: article.slug, article }));
    const { wrapper, router } = await mounted(`/articles/${article.slug}`);
    expect(wrapper.find('[data-testid="article-body"] script').exists()).toBe(false);
    expect(wrapper.text()).toContain('Example Editor');
    const heading = wrapper.get('[data-testid="article-body"] h2').element as HTMLElement;
    const scroll = vi.fn();
    heading.scrollIntoView = scroll;
    // The browser has CSS.escape; jsdom may not.
    vi.stubGlobal('CSS', { escape: (value: string) => value });
    const toc = wrapper.get('nav[aria-label="目录"]');
    await toc.findAll('button')[0]!.trigger('click');
    expect(scroll).toHaveBeenCalled();
    expect(document.activeElement).toBe(heading);
    expect(router.currentRoute.value.path).toBe(`/articles/${article.slug}`);
    await wrapper.get('[data-testid="article-body"] a[href="https://example.com/docs"]').trigger('click');
    expect(openExternal).toHaveBeenCalledWith('https://example.com/docs');
    await wrapper.get('[data-testid="article-body"] a[href="/articles/another-guide"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe(`/articles/${article.slug}`); // The API resolves the alias back.
  });

  it('replaces old addresses, and removes the body when a refresh reports withdrawal', async () => {
    let withdrawn = false;
    stubFetch(url =>
      url.pathname.endsWith('/old-guide')
        ? ok({ state: 'moved', slug: article.slug })
        : ok(
            withdrawn ? { state: 'withdrawn', slug: article.slug } : { state: 'published', slug: article.slug, article }
          )
    );
    const { wrapper, router } = await mounted('/articles/old-guide');
    expect(router.currentRoute.value.path).toBe(`/articles/${article.slug}`);
    expect(wrapper.find('[data-testid="article-body"]').exists()).toBe(true);
    withdrawn = true;
    await refreshPage();
    await flushPromises();
    expect(wrapper.text()).toContain('这篇文章已撤下');
    expect(wrapper.find('[data-testid="article-body"]').exists()).toBe(false);
    expect(wrapper.get('a[href="/articles"]').text()).toBe('文章');
    expect(wrapper.text()).toContain('浏览全部文章');
  });

  it('bounds redirect loops and reports not found without an endless spinner', async () => {
    stubFetch(() => ok({ state: 'moved', slug: 'loop' }));
    const { wrapper } = await mounted('/articles/loop');
    expect(wrapper.findComponent({ name: 'OnErrorState' }).props('retryable')).toBe(false);
    expect(wrapper.find('[data-testid="article-body"]').exists()).toBe(false);
  });

  it('renders translated articles without provenance controls, and refetches on language changes', async () => {
    const requests: URL[] = [];
    stubFetch(url => {
      requests.push(url);
      return ok({
        state: 'published',
        slug: article.slug,
        article: {
          ...article,
          title: 'Translated title',
          machineTranslated: true
        }
      });
    });
    const { wrapper } = await mounted(`/articles/${article.slug}`);
    expect(wrapper.get('h1').text()).toBe('Translated title');
    expect(wrapper.get('article').attributes('lang')).toBe('zh-CN');
    expect(wrapper.get('header').text()).not.toMatch(/AI 翻译|查看原文|查看译文/);
    expect(wrapper.get('header').findAll('button')).toHaveLength(0);
    expect(requests).toHaveLength(1);
    expect(i18n.global.locale.value).toBe('zh-CN');
    const count = requests.length;
    i18n.global.locale.value = 'ja-JP';
    await flushPromises();
    expect(requests.length).toBeGreaterThan(count);
    expect(wrapper.find('[data-testid="article-source-text"]').exists()).toBe(true);
  });
});
