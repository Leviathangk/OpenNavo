import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NAvatar,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NImage,
  NInput,
  NModal,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSpin,
  NSwitch,
  NTag,
  NText,
  NUpload
} from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import ArticleDetail from '../src/views/content/article-detail/[id].vue';
import {
  changedFields,
  formFromView,
  formProblems,
  packagesChanged
} from '../src/views/content/article-detail/modules/article';
import type { Article } from '../src/views/content/article-detail/modules/article';

const api = vi.hoisted(() => ({
  fetchArticle: vi.fn(),
  fetchArticleAuthors: vi.fn(),
  updateArticle: vi.fn(),
  setArticlePackages: vi.fn(),
  submitArticle: vi.fn(),
  publishArticle: vi.fn(),
  withdrawArticle: vi.fn(),
  restoreArticle: vi.fn(),
  revertArticle: vi.fn(),
  setArticleSlug: vi.fn(),
  setArticleLocale: vi.fn(),
  uploadAsset: vi.fn()
}));
const permissions = vi.hoisted(() => ({ publish: false }));
vi.mock('@/service/api', () => api);
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/hooks/business/auth', () => ({
  useAuth: () => ({ hasAuth: (code: string) => code !== 'content:article:publish' || permissions.publish })
}));
vi.mock('@/hooks/business/localized', () => ({
  useLocalizedPackage: () => ({ nameOf: (row: { name: string }) => row.name })
}));
vi.mock('vue-router', () => ({ useRoute: () => ({ params: { id: '1' } }) }));

const components = {
  NAlert,
  NAvatar,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NImage,
  NInput,
  NModal,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSpin,
  NSwitch,
  NTag,
  NText,
  NUpload
};

function article(overrides: Partial<Article> = {}, working: Partial<Article['working']> = {}): Article {
  return {
    id: 1,
    slug: 'choose-a-screenshot-app',
    status: 'draft',
    revision: 4,
    firstPublishedAt: null,
    contentUpdatedAt: null,
    archivedAt: null,
    createdAt: '2026-10-13T08:00:00Z',
    updatedAt: '2026-10-13T08:00:00Z',
    webUrl: null,
    aliases: [],
    working: {
      number: 1,
      sourceLocale: 'en-US',
      type: 'roundup',
      authorId: null,
      author: null,
      coverAssetId: null,
      coverUrl: null,
      coverAlt: null,
      sources: [{ title: 'Shottr docs', url: 'https://shottr.cc/kb' }],
      packages: [{ id: 7, token: 'shottr', name: 'Shottr', displayName: 'Shottr', iconUrl: null }],
      title: 'How to choose a screenshot app',
      summary: null,
      bodyMarkdown: '## Pick\n\n::app[shottr]',
      testNote: null,
      seoTitle: null,
      seoDescription: null,
      submittedAt: null,
      submittedByName: '',
      publishedAt: null,
      createdByName: 'Hermes',
      createdAt: '2026-10-13T08:00:00Z',
      updatedAt: '2026-10-13T08:00:00Z',
      ...working
    },
    published: null,
    hasPendingChanges: true,
    readiness: ['summary'],
    images: [],
    locales: [],
    revisions: [],
    events: [],
    ...overrides
  } as Article;
}

beforeEach(() => {
  permissions.publish = false;
  for (const fn of Object.values(api)) fn.mockReset();
  api.fetchArticle.mockResolvedValue({ data: article(), error: null });
  api.fetchArticleAuthors.mockResolvedValue({ data: [], error: null });
  api.updateArticle.mockImplementation(async (_id: number, body: Record<string, unknown>) => ({
    data: article({ revision: 5, readiness: [] }, { summary: String(body.summary ?? '') }),
    error: null
  }));
  api.submitArticle.mockResolvedValue({
    data: article(
      { status: 'review', revision: 6, readiness: [] },
      { summary: 'What to compare', submittedAt: '2026-10-13T09:00:00Z' }
    ),
    error: null
  });
});

describe('Article form helpers', () => {
  it('sends only changed fields and clears emptied ones', () => {
    const view = article({}, { summary: 'Old', testNote: 'Tested' });
    const form = formFromView(view);
    expect(changedFields(form, view)).toEqual({});
    form.summary = '  New summary ';
    form.testNote = '   ';
    form.sources.push({ title: ' Docs ', url: ' https://vendor.example ' });
    expect(changedFields(form, view)).toEqual({
      summary: 'New summary',
      testNote: null,
      sources: [
        { title: 'Shottr docs', url: 'https://shottr.cc/kb' },
        { title: 'Docs', url: 'https://vendor.example' }
      ]
    });
    form.coverAssetId = 3;
    form.coverAlt = 'Capture';
    expect(changedFields(form, view)).toMatchObject({ coverAssetId: 3, coverAlt: 'Capture' });
    expect(packagesChanged(form, view)).toBe(false);
    form.packages.push({ id: 8, token: 'cleanshot', displayName: 'CleanShot X', iconUrl: null });
    expect(packagesChanged(form, view)).toBe(true);
  });

  it('flags what the server would reject before saving', () => {
    const form = formFromView(article());
    expect(formProblems(form)).toEqual([]);
    form.title = ' ';
    form.sources.push({ title: 'x', url: 'http://insecure.example' });
    form.coverAssetId = 1;
    expect(formProblems(form)).toEqual(['title', 'sources', 'cover_alt']);
  });
});

describe('Article editor', () => {
  it('lists problems, saves with the article revision and submits', async () => {
    const wrapper = mount(ArticleDetail, {
      global: { components, stubs: { PackagePicker: true, ArticlePreview: true } }
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="article-problems"]').text()).toContain(
      'page.content.articleDetail.problems.summary'
    );
    const button = (label: string) => wrapper.findAll('button').find(node => node.text() === label);
    expect(button('page.content.articleDetail.publish')).toBeUndefined();
    await wrapper.find('textarea[aria-label="page.content.articleDetail.summary"]').setValue('What to compare');
    await button('page.content.articleDetail.submit')!.trigger('click');
    await flushPromises();
    expect(api.updateArticle).toHaveBeenCalledWith(1, { expectedRevision: 4, summary: 'What to compare' });
    expect(api.submitArticle).toHaveBeenCalledWith(1, 5);
    expect(wrapper.text()).toContain('page.content.article.statuses.review');
  });

  it('offers publishing only to publishers once the draft is submitted', async () => {
    permissions.publish = true;
    api.fetchArticle.mockResolvedValue({
      data: article({ status: 'review', readiness: [] }, { summary: 'Done', submittedAt: '2026-10-13T09:00:00Z' }),
      error: null
    });
    const wrapper = mount(ArticleDetail, {
      global: { components, stubs: { PackagePicker: true, ArticlePreview: true } }
    });
    await flushPromises();
    const labels = wrapper.findAll('button').map(node => node.text());
    expect(labels).toContain('page.content.articleDetail.publish');
    expect(labels).not.toContain('page.content.articleDetail.submit');
  });
});
