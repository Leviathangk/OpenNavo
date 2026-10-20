import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NAvatar,
  NButton,
  NCard,
  NDivider,
  NInputNumber,
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
import ClaimEvidence from '../src/views/content/article-detail/modules/claim-evidence.vue';
import ArticleDetail from '../src/views/content/article-detail/[id].vue';
import {
  changedFields,
  formFromView,
  formProblems,
  packagesChanged,
  publicationProblems
} from '../src/views/content/article-detail/modules/article';
import type { Article } from '../src/views/content/article-detail/modules/article';

const api = vi.hoisted(() => ({
  captureArticleEvidence: vi.fn(),
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
  NDivider,
  NInputNumber,
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
      claims: [
        {
          text: 'Shottr supports macOS.',
          kind: 'factual',
          evidenceId: 1,
          excerpt: 'Shottr supports macOS.',
          appVersion: '',
          region: 'ZZ'
        }
      ],
      target: { entityKey: 'topic:mac-screenshots', intent: 'topic' },
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

describe('Article publication fields', () => {
  it('round-trips claims and target without mutating the loaded revision', () => {
    const view = article();
    const form = formFromView(view);
    form.claims[0]!.evidenceId = 42;
    form.target!.intent = 'compare';
    expect(view.working.claims![0]!.evidenceId).toBe(1);
    expect(changedFields(form, view)).toMatchObject({ claims: [{ evidenceId: 42 }], target: { intent: 'compare' } });
    expect(publicationProblems(form)).toEqual([]);
    form.claims[0]!.kind = 'test';
    expect(publicationProblems(form)).toContain('claims');
    form.target!.entityKey = 'pkg:999';
    expect(publicationProblems(form)).toContain('target');
  });
  it('blocks publishing when evidence or target is missing', async () => {
    permissions.publish = true;
    api.fetchArticle.mockResolvedValue({
      data: article(
        { status: 'review', readiness: [] },
        { claims: [], target: null, submittedAt: '2026-10-13T09:00:00Z' }
      ),
      error: null
    });
    const wrapper = mount(ArticleDetail, {
      global: { components, stubs: { PackagePicker: true, ArticlePreview: true } }
    });
    await flushPromises();
    const publish = wrapper.findAll('button').find(node => node.text() === 'page.content.articleDetail.publish')!;
    expect(publish.attributes('disabled')).toBeDefined();
    expect(wrapper.text()).toContain('page.content.articleDetail.problems.claims');
  });
});

describe('Article editor', () => {
  it('preserves unsaved claim and source inputs while staging newly linked apps', async () => {
    const wrapper = mount(ArticleDetail, {
      global: { components, stubs: { PackagePicker: true, ArticlePreview: true } }
    });
    await flushPromises();
    wrapper.findComponent({ name: 'PackagePicker' }).vm.$emit('pick', {
      id: 8,
      token: 'new-app',
      name: 'New app',
      displayName: 'New app',
      iconUrl: null
    });
    const label = (key: string) => `[aria-label="page.content.articleDetail.${key}"]`;
    await wrapper.find(label('claimText')).setValue('Unsaved factual statement');
    await wrapper.find(label('sourceUrl')).setValue('https://vendor.example/guide');
    const evidence = wrapper.findComponent(ClaimEvidence);
    api.updateArticle.mockResolvedValue({ data: article({ revision: 5 }), error: null });
    api.captureArticleEvidence.mockResolvedValue({
      data: { id: 42, excerpt: 'Shottr supports macOS.', finalUrl: 'https://vendor.example/guide' },
      error: null
    });
    await evidence
      .findAll('button')
      .find(node => node.text() === 'page.content.articleDetail.captureSource')!
      .trigger('click');
    await flushPromises();
    expect(api.updateArticle).toHaveBeenCalledWith(1, { expectedRevision: 4, packageIds: [7, 8] });
    expect(wrapper.findComponent(ClaimEvidence).element).toBe(evidence.element);
    expect((wrapper.find(label('claimText')).element as HTMLTextAreaElement).value).toBe('Unsaved factual statement');
    expect((wrapper.find(label('sourceUrl')).element as HTMLInputElement).value).toBe('https://vendor.example/guide');
    expect(evidence.props('claim').evidenceId).toBe(42);
  });

  it.each(['save', 'capture', 'failed capture preparation'])(
    'replaces one of 50 apps without an oversized intermediate request: %s',
    async mode => {
      const packages = Array.from({ length: 50 }, (_, i) => ({
        id: i + 1,
        token: `app-${i + 1}`,
        name: `App ${i + 1}`,
        displayName: `App ${i + 1}`,
        iconUrl: null
      }));
      api.fetchArticle.mockResolvedValue({
        data: article({}, { packages, target: { entityKey: 'pkg:1', intent: 'download' } }),
        error: null
      });
      api.updateArticle.mockImplementation(async (_id, body) => {
        expect(body.packageIds).toHaveLength(50);
        expect(body.packageIds).not.toContain(1);
        expect(body.packageIds).toContain(51);
        return mode === 'failed capture preparation'
          ? { data: null, error: new Error('conflict') }
          : {
              data: article(
                { revision: 5 },
                { packages: [...packages.slice(1), { ...packages[0]!, id: 51 }], target: null }
              ),
              error: null
            };
      });
      api.captureArticleEvidence.mockResolvedValue({
        data: { id: 42, excerpt: 'Shottr supports macOS.' },
        error: null
      });
      const wrapper = mount(ArticleDetail, {
        global: { components, stubs: { PackagePicker: true, ArticlePreview: true } }
      });
      await flushPromises();
      const appCard = wrapper
        .findAllComponents(NCard)
        .find(card => card.findComponent({ name: 'PackagePicker' }).exists())!;
      const deleteApp = appCard.findAll('button').find(node => node.text() === 'common.delete')!;
      await deleteApp.trigger('click');
      wrapper
        .findComponent({ name: 'PackagePicker' })
        .vm.$emit('pick', { id: 51, token: 'new-app', name: 'New app', iconUrl: null });
      await flushPromises();
      const label = (key: string) => `[aria-label="page.content.articleDetail.${key}"]`;
      await wrapper.find(label('claimText')).setValue('Unsaved claim');
      if (mode === 'save') {
        await wrapper.find(label('targetEntity')).setValue('pkg:51');
        await wrapper
          .findAll('button')
          .find(node => node.text() === 'page.content.articleDetail.save')!
          .trigger('click');
      } else {
        await wrapper.find(label('sourceUrl')).setValue('https://vendor.example/guide');
        await wrapper
          .findAll('button')
          .find(node => node.text() === 'page.content.articleDetail.captureSource')!
          .trigger('click');
      }
      await flushPromises();
      expect(api.setArticlePackages).not.toHaveBeenCalled();
      expect(api.updateArticle).toHaveBeenCalledOnce();
      const body = api.updateArticle.mock.calls[0]![1];
      expect(body.expectedRevision).toBe(4);
      if (mode === 'save') {
        expect(body.claims[0].text).toBe('Unsaved claim');
      } else {
        expect(body.target).toBeNull();
        expect(body.claims).toBeUndefined();
        expect((wrapper.find(label('claimText')).element as HTMLTextAreaElement).value).toBe('Unsaved claim');
        expect((wrapper.find(label('sourceUrl')).element as HTMLInputElement).value).toBe(
          'https://vendor.example/guide'
        );
        expect(api.captureArticleEvidence).toHaveBeenCalledTimes(mode === 'capture' ? 1 : 0);
      }
    }
  );

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
    window.$dialog = {
      warning: ({ onPositiveClick }: { onPositiveClick: () => Promise<void> }) => onPositiveClick()
    } as unknown as Window['$dialog'];
    api.publishArticle.mockResolvedValue({
      data: article({ status: 'published', revision: 5, readiness: [] }),
      error: null
    });
    const publish = wrapper.findAll('button').find(node => node.text() === 'page.content.articleDetail.publish')!;
    expect(publish.attributes('disabled')).toBeUndefined();
    await publish.trigger('click');
    await flushPromises();
    expect(api.publishArticle).toHaveBeenCalledWith(1, 4);
    delete window.$dialog;
  });
});
