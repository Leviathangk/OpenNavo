import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NButton,
  NCard,
  NCheckbox,
  NCheckboxGroup,
  NCode,
  NDivider,
  NDynamicInput,
  NDynamicTags,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NP,
  NRadioButton,
  NRadioGroup,
  NSelect,
  NSkeleton,
  NSwitch,
  NTag,
  NText
} from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import SeoSettings from '../src/views/system/seo/index.vue';
import {
  botProblem,
  randomIndexNowKey,
  validHttps,
  validLabel,
  validRegions
} from '../src/views/system/seo/modules/validate';

const api = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn() }));
// The data-provider card (system:seo:edit) has its own tests in seo-providers.test.ts.
vi.mock('@/hooks/business/auth', () => ({ useAuth: () => ({ hasAuth: () => false }) }));
vi.mock('@/service/api', () => ({
  fetchSeoSettings: api.load,
  updateSeoSettings: api.save,
  fetchIndexReport: vi.fn()
}));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));

function settings() {
  return {
    identity: { name: 'OpenNavo', alternateNames: [], logoUrl: '', sameAs: ['https://github.com/opennavo/OpenNavo'] },
    verification: { google: '', bing: '' },
    analytics: { ga4Enabled: false, ga4MeasurementId: '', consentRegions: ['DE', 'GB'] },
    indexNow: { enabled: false },
    cloudflare: { zoneId: '', purgeEnabled: false },
    edgeCache: { enabled: false },
    crawlers: {
      training: { mode: 'limit', delaySeconds: 10, bots: ['ClaudeBot'] },
      seoTools: { mode: 'block', delaySeconds: 10, bots: ['MJ12bot', 'SerpstatBot'] }
    },
    pages: {
      titleTemplate: 'new',
      indexPolicy: { mode: 'shadow', scope: 'new', since: '' },
      translationMode: 'shadow',
      translationLocales: [] as string[],
      versionsMinNotesChars: 300,
      openLocales: ['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'],
      intentPagesEnabled: false,
      detailsRedirect: false,
      versionsSitemap: false
    }
  };
}

function response(overrides: Record<string, unknown> = {}, value = settings()) {
  return {
    data: {
      settings: value,
      indexNowKeyConfigured: false,
      cloudflareTokenConfigured: false,
      updatedAt: '2026-10-13T08:00:00Z',
      revision: 3,
      siteUrl: 'https://opennavo.example',
      changeFeed: { lastRunAt: null, lastUrls: 0, lastIndexnowStatus: '', lastPurgeStatus: '', lastError: '' },
      ...overrides
    },
    error: undefined
  };
}

function page() {
  return mount(SeoSettings, {
    global: {
      // The impact reports have their own tests (seo-index.test.ts).
      stubs: { IndexReport: true, TranslationImpact: true },
      components: {
        NAlert,
        NButton,
        NCard,
        NCheckbox,
        NCheckboxGroup,
        NCode,
        NDivider,
        NDynamicInput,
        NDynamicTags,
        NForm,
        NFormItem,
        NInput,
        NInputNumber,
        NP,
        NRadioButton,
        NRadioGroup,
        NSelect,
        NSkeleton,
        NSwitch,
        NTag,
        NText
      }
    }
  });
}

function button(wrapper: ReturnType<typeof page>, key: string) {
  return wrapper.findAll('button').find(node => node.text() === key)!;
}

function keyInput(wrapper: ReturnType<typeof page>) {
  return wrapper.find('input[aria-label="page.system.seo.indexNowKey"]');
}

beforeEach(() => {
  api.load.mockReset().mockResolvedValue(response());
  api.save.mockReset().mockResolvedValue(response({ indexNowKeyConfigured: true, revision: 4 }));
});

describe('SEO settings page', () => {
  it('Refreshes the impact report after reload and save', async () => {
    const wrapper = page();
    await flushPromises();
    const revision = () => wrapper.find('index-report-stub').attributes('revision');
    const initial = revision();
    await button(wrapper, 'common.refresh').trigger('click');
    await flushPromises();
    expect(revision()).not.toBe(initial);
    const refreshed = revision();
    await keyInput(wrapper).setValue('fixture-indexnow-key-0001');
    await button(wrapper, 'page.system.seo.save').trigger('click');
    await flushPromises();
    expect(revision()).not.toBe(refreshed);
    wrapper.unmount();
  });

  it('Previews the robots.txt the web server will serve for the current policy', async () => {
    const wrapper = page();
    await flushPromises();
    const preview = wrapper.find('[data-testid="robots-preview"]').text();
    expect(preview).toContain('User-agent: MJ12bot\nUser-agent: SerpstatBot\nDisallow: /');
    expect(preview).toContain('User-agent: ClaudeBot\nCrawl-delay: 10');
    expect(preview).toContain('Sitemap: https://opennavo.example/sitemap_index.xml');
    expect(button(wrapper, 'page.system.seo.save').attributes('disabled')).toBeDefined();
    wrapper.unmount();
  });

  it('Sends a typed key once and never shows stored credentials again', async () => {
    const wrapper = page();
    await flushPromises();
    await keyInput(wrapper).setValue('fixture-indexnow-key-0001');
    await button(wrapper, 'page.system.seo.save').trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledTimes(1);
    const body = api.save.mock.calls[0]![0];
    expect(body).toMatchObject({ indexNowKey: 'fixture-indexnow-key-0001', clearIndexNowKey: false });
    expect(body).not.toHaveProperty('cloudflareToken');
    expect(body.settings.crawlers.training.bots).toEqual(['ClaudeBot']);
    expect((keyInput(wrapper).element as HTMLInputElement).value).toBe('');
    expect(wrapper.text()).toContain('page.system.seo.configured');
    expect(wrapper.html()).not.toContain('fixture-indexnow-key-0001');
    wrapper.unmount();
  });

  it('Blocks saving search crawlers in tunable classes and switches that lack credentials', async () => {
    const locked = settings();
    locked.crawlers.training.bots = ['ClaudeBot', 'Googlebot'];
    api.load.mockResolvedValueOnce(response({}, locked));
    const wrapper = page();
    await flushPromises();
    await keyInput(wrapper).setValue('fixture-indexnow-key-0001');
    await button(wrapper, 'page.system.seo.save').trigger('click');
    await flushPromises();
    expect(api.save).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('page.system.seo.errors.lockedBot');
    wrapper.unmount();

    const enabled = settings();
    enabled.indexNow.enabled = true;
    api.load.mockResolvedValueOnce(response({}, enabled));
    const second = page();
    await flushPromises();
    await second.find('input[aria-label="page.system.seo.siteName"]').setValue('OpenNavo App Store');
    await button(second, 'page.system.seo.save').trigger('click');
    await flushPromises();
    expect(api.save).not.toHaveBeenCalled();
    expect(second.text()).toContain('page.system.seo.errors.keyRequired');
    second.unmount();
  });

  it('Opens site languages and the details redirect, keeping the default language open', async () => {
    const wrapper = page();
    await flushPromises();
    const checkbox = (name: string) => wrapper.findAll('.n-checkbox').find(node => node.text() === name)!;
    expect(checkbox('English').classes()).toContain('n-checkbox--disabled');
    await checkbox('Deutsch').trigger('click');
    await wrapper.find('[aria-label="page.system.seo.detailsRedirect"]').trigger('click');
    await button(wrapper, 'page.system.seo.save').trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledTimes(1);
    const pages = api.save.mock.calls[0]![0].settings.pages;
    expect(pages.openLocales).toEqual(['en-US', 'zh-CN', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU', 'de-DE']);
    expect(pages).toMatchObject({ detailsRedirect: true, intentPagesEnabled: false, versionsMinNotesChars: 300 });
    wrapper.unmount();

    const closed = settings();
    closed.pages.openLocales = ['zh-CN'];
    closed.pages.versionsMinNotesChars = 0;
    api.load.mockResolvedValueOnce(response({}, closed));
    const second = page();
    await flushPromises();
    await second.find('input[aria-label="page.system.seo.siteName"]').setValue('OpenNavo App Store');
    await button(second, 'page.system.seo.save').trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledTimes(1);
    expect(second.text()).toContain('page.system.seo.errors.defaultLocaleOpen');
    expect(second.text()).toContain('page.system.seo.errors.invalidMinNotes');
    second.unmount();
  });

  it('Hides the form on load failure until refresh succeeds', async () => {
    api.load.mockResolvedValueOnce({ data: null, error: new Error('offline') });
    const wrapper = page();
    await flushPromises();
    expect(wrapper.find('form').exists()).toBe(false);
    expect(wrapper.text()).toContain('page.system.seo.loadFailed');
    await button(wrapper, 'common.refresh').trigger('click');
    await flushPromises();
    expect(wrapper.find('form').exists()).toBe(true);
    expect(wrapper.text()).not.toContain('page.system.seo.loadFailed');
    wrapper.unmount();
  });
});

describe('SEO settings validation helpers', () => {
  it('Keeps search crawlers out and rejects duplicates across classes', () => {
    expect(botProblem(['GPTBot', 'CCBot'], ['MJ12bot'])).toBeNull();
    expect(botProblem(['bingbot'], [])).toEqual({ key: 'lockedBot', bot: 'bingbot' });
    expect(botProblem(['*'], [])).toEqual({ key: 'lockedBot', bot: '*' });
    expect(botProblem(['GPTBot', 'gptbot'], [])).toEqual({ key: 'invalidBot', bot: 'gptbot' });
    expect(botProblem(['Bad Bot'], [])).toEqual({ key: 'invalidBot', bot: 'Bad Bot' });
    expect(botProblem(['CCBot'], ['ccbot'])).toEqual({ key: 'overlapBot', bot: 'CCBot' });
    expect(
      botProblem(
        Array.from({ length: 21 }, (_, index) => `Bot${index}`),
        []
      )
    ).toEqual({ key: 'tooMany' });
  });

  it('Validates links, labels, regions and generated keys', () => {
    expect(validHttps('https://opennavo.example/logo.png', 500)).toBe(true);
    expect(validHttps('http://opennavo.example/logo.png', 500)).toBe(false);
    expect(validHttps('https://user:pass@opennavo.example', 500)).toBe(false);
    expect(validHttps('not a url', 500)).toBe(false);
    expect(validLabel('OpenNavo', 60)).toBe(true);
    expect(validLabel('line\nbreak', 60)).toBe(false);
    expect(validRegions(['DE', 'GB'])).toBe(true);
    expect(validRegions(['DE', 'DE'])).toBe(false);
    expect(validRegions(['de'])).toBe(false);
    expect(randomIndexNowKey()).toMatch(/^[a-f0-9]{32}$/);
  });
});
