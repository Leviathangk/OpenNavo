import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NDivider,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NPagination,
  NRadioButton,
  NRadioGroup,
  NSkeleton,
  NSpin,
  NSwitch,
  NTable,
  NTag,
  NText
} from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import IndexTab from '../src/views/catalog/package-detail/modules/index-tab.vue';
import IntentPages from '../src/views/catalog/package-detail/modules/intent-pages.vue';
import SeoOverride from '../src/views/catalog/package-detail/modules/seo-override.vue';
import IndexReport from '../src/views/system/seo/modules/index-report.vue';
import TranslationImpact from '../src/views/system/seo/modules/translation-impact.vue';
import { isRevisionConflict, writeOwnerLabel } from '../src/utils/opennavo';

const api = vi.hoisted(() => ({
  status: vi.fn(),
  save: vi.fn(),
  report: vi.fn(),
  intent: vi.fn(),
  saveIntent: vi.fn(),
  seo: vi.fn(),
  saveSeo: vi.fn()
}));
const auth = vi.hoisted(() => ({ codes: new Set<string>() }));
vi.mock('@/service/api', () => ({
  fetchPackageIndexStatus: api.status,
  updatePackageIndexStatus: api.save,
  fetchIndexReport: api.report,
  fetchPackageIntentPages: api.intent,
  updatePackageIntentPages: api.saveIntent,
  fetchPackageSeo: api.seo,
  updatePackageSeo: api.saveSeo
}));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => ({ locale: 'en-US' }) }));
vi.mock('@/hooks/business/auth', () => ({
  useAuth: () => ({ hasAuth: (codes: string | string[]) => [codes].flat().some(code => auth.codes.has(code)) })
}));
// Named values are echoed after the key, so labels built from them can be checked.
vi.mock('@/locales', () => ({
  $t: (key: string, values?: Record<string, unknown>) => (values ? `${key}:${Object.values(values).join(',')}` : key)
}));

const components = {
  NAlert,
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NDivider,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NPagination,
  NRadioButton,
  NRadioGroup,
  NSkeleton,
  NSpin,
  NSwitch,
  NTable,
  NTag,
  NText
};
// The blocks below the index status have their own tests further down.
const blocks = { IntentPages: true, SeoOverride: true };
// The admin API answers HTTP 200 with a localized message; only the code identifies the conflict.
const conflict = {
  data: null,
  error: new Error('Conflict'),
  response: { status: 200, data: { code: '1003', msg: 'Conflict (revision_conflict:9)', data: null } }
};

function status(overrides: Record<string, unknown> = {}) {
  return {
    data: {
      eligible: false,
      reason: 'missing_summary',
      indexable: false,
      enforced: true,
      locales: [],
      override: 'auto',
      overrideReason: null,
      overrideAt: null,
      ...overrides
    },
    error: undefined
  };
}

function intentItem(pageKind: string, locale: string, enabled: boolean, contentEligible: boolean, reason?: string) {
  const indexable = enabled && contentEligible && !reason;
  return {
    pageKind,
    locale,
    enabled,
    derived: locale === 'zh-Hant',
    contentEligible,
    indexable,
    reason: indexable ? null : (reason ?? 'intent_not_eligible'),
    url: `https://example.test/${locale}/apps/example/${pageKind}`,
    enabledAt: enabled ? '2026-10-26T08:00:00Z' : null,
    enabledBy: enabled ? 'agent:3:7' : null
  };
}

function intentView(overrides: Record<string, unknown> = {}) {
  return {
    packageId: 7,
    token: 'example',
    revision: 3,
    globalEnabled: true,
    items: [
      intentItem('uninstall', 'en-US', false, false),
      intentItem('alternatives', 'zh-Hant', false, true, 'locale_closed'),
      intentItem('alternatives', 'zh-CN', false, true),
      intentItem('alternatives', 'en-US', true, true)
    ],
    ...overrides
  };
}

function seoItem(locale: string, title: string | null, description: string | null, updatedBy = 'admin:1') {
  return { locale, title, description, derived: locale === 'zh-Hant', updatedAt: '2026-10-20T08:00:00Z', updatedBy };
}

function seoView(overrides: Record<string, unknown> = {}) {
  return {
    packageId: 7,
    token: 'example',
    revision: 5,
    items: [seoItem('en-US', 'Saved title', 'Saved description', 'agent:3:7')],
    ...overrides
  };
}

beforeEach(() => {
  auth.codes = new Set(['system:seo:edit', 'catalog:package:view', 'catalog:package:edit']);
  api.intent.mockReset().mockResolvedValue({ data: intentView(), error: null });
  api.saveIntent.mockReset();
  api.seo.mockReset().mockResolvedValue({ data: seoView(), error: null });
  api.saveSeo.mockReset();
  api.status.mockReset().mockResolvedValue(status());
  api.save.mockReset().mockResolvedValue(
    status({
      eligible: true,
      reason: null,
      indexable: true,
      locales: ['en-US'],
      override: 'index',
      overrideReason: 'Being rewritten'
    })
  );
  api.report.mockReset().mockResolvedValue({
    data: {
      records: [
        {
          id: 7,
          token: 'thin-app',
          displayName: 'Thin App',
          firstSeenAt: '2026-10-01T08:00:00Z',
          eligible: false,
          reason: 'missing_summary',
          enforced: true,
          override: 'auto',
          overrideReason: null
        }
      ],
      current: 1,
      size: 20,
      total: 1,
      counts: { hidden: 0, forcedNoindex: 0, unavailable: 2, missingSummary: 1, thin: 0, forcedIndex: 0 }
    },
    error: undefined
  });
});

describe('Index tab', () => {
  it('Shows each block only to the permission that owns it', async () => {
    auth.codes = new Set(['catalog:package:view']);
    const viewer = mount(IndexTab, { props: { packageId: 7 }, global: { components, stubs: blocks } });
    await flushPromises();
    expect(api.status).not.toHaveBeenCalled();
    expect(viewer.find('[data-testid="index-effect"]').exists()).toBe(false);
    expect(viewer.get('intent-pages-stub').attributes('editable')).toBe('false');
    expect(viewer.get('seo-override-stub').attributes('editable')).toBe('false');
    viewer.unmount();

    auth.codes = new Set(['system:seo:edit']);
    const settings = mount(IndexTab, { props: { packageId: 7 }, global: { components, stubs: blocks } });
    await flushPromises();
    expect(api.status).toHaveBeenCalledWith(7);
    expect(settings.find('[data-testid="index-effect"]').exists()).toBe(true);
    expect(settings.find('intent-pages-stub').exists()).toBe(false);
    expect(settings.find('seo-override-stub').exists()).toBe(false);
    settings.unmount();
  });

  it('Shows the verdict and requires a reason before forcing a page in', async () => {
    const wrapper = mount(IndexTab, { props: { packageId: 7 }, global: { components, stubs: blocks } });
    await flushPromises();
    expect(wrapper.text()).toContain('page.catalog.packageDetail.index.ineligible');
    expect(wrapper.find('[data-testid="index-effect"]').text()).toBe('page.catalog.packageDetail.index.noindexed');
    await wrapper.findAll('input[type="radio"]')[1]!.setValue(true);
    const save = () => wrapper.findAll('button').find(node => node.text() === 'page.catalog.packageDetail.index.save')!;
    await save().trigger('click');
    await flushPromises();
    expect(api.save).not.toHaveBeenCalled();
    expect(wrapper.text()).toContain('page.catalog.packageDetail.index.reasonRequired');
    await wrapper.find('textarea').setValue('  Being rewritten  ');
    await save().trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledWith(7, { override: 'index', reason: 'Being rewritten' });
    expect(wrapper.find('[data-testid="index-effect"]').text()).toBe('page.catalog.packageDetail.index.indexed');
    wrapper.unmount();
  });

  it('Sends auto without a reason', async () => {
    api.status.mockResolvedValueOnce(status({ override: 'noindex', overrideReason: 'Duplicate' }));
    const wrapper = mount(IndexTab, { props: { packageId: 7 }, global: { components, stubs: blocks } });
    await flushPromises();
    await wrapper.findAll('input[type="radio"]')[0]!.setValue(true);
    await wrapper
      .findAll('button')
      .find(node => node.text() === 'page.catalog.packageDetail.index.save')!
      .trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledWith(7, { override: 'auto' });
    wrapper.unmount();
  });
});

describe('Intent pages', () => {
  const intent = (editable = true) => mount(IntentPages, { props: { packageId: 7, editable }, global: { components } });
  const saveButton = (wrapper: ReturnType<typeof intent>) =>
    wrapper.findAll('button').find(node => node.text() === 'page.catalog.packageDetail.index.intent.save');

  it('Lists the switches by page and language, and saves only the changed ones with the read revision', async () => {
    const wrapper = intent();
    await flushPromises();
    expect(wrapper.findAll('tbody tr').map(row => row.attributes('data-testid'))).toEqual([
      'intent-alternatives:en-US',
      'intent-alternatives:zh-CN',
      'intent-alternatives:zh-Hant',
      'intent-uninstall:en-US'
    ]);
    expect(wrapper.find('[data-testid="intent-global-off"]').exists()).toBe(false);
    const uninstall = wrapper.get('[data-testid="intent-uninstall:en-US"]');
    expect(uninstall.text()).toContain('page.catalog.packageDetail.index.intent.ineligible');
    // Why a page stays out of the index, and who opened the ones that are on.
    expect(uninstall.text()).toContain('intent_not_eligible');
    expect(wrapper.get('[data-testid="intent-alternatives:en-US"]').text()).toContain('page.shared.owner.agent:3');
    expect(saveButton(wrapper)!.attributes('disabled')).toBeDefined();
    api.saveIntent.mockResolvedValueOnce({ data: intentView({ revision: 4 }), error: null });
    await wrapper.get('[data-testid="intent-alternatives:zh-CN"] .n-switch').trigger('click');
    // Traditional Chinese follows the Simplified Chinese switch, unsaved change included, and is never sent.
    const traditional = wrapper.get('[data-testid="intent-alternatives:zh-Hant"] .n-switch');
    expect(traditional.classes()).toContain('n-switch--disabled');
    expect(traditional.classes()).toContain('n-switch--active');
    await saveButton(wrapper)!.trigger('click');
    await flushPromises();
    expect(api.saveIntent).toHaveBeenCalledWith(
      7,
      { items: [{ pageKind: 'alternatives', locale: 'zh-CN', enabled: true }] },
      3
    );
    wrapper.unmount();
  });

  it('Warns while the site-wide switch is off', async () => {
    api.intent.mockResolvedValueOnce({ data: intentView({ globalEnabled: false }), error: null });
    const wrapper = intent();
    await flushPromises();
    expect(wrapper.find('[data-testid="intent-global-off"]').exists()).toBe(true);
    wrapper.unmount();
  });

  it('After a revision conflict, takes what someone else changed and keeps only the other unsaved switches', async () => {
    const wrapper = intent();
    await flushPromises();
    const row = (key: string) => wrapper.get(`[data-testid="intent-${key}"]`);
    // Open the uninstall page and the Simplified Chinese alternatives page.
    await row('uninstall:en-US').get('.n-switch').trigger('click');
    await row('alternatives:zh-CN').get('.n-switch').trigger('click');
    // Meanwhile an agent closed the English alternatives page and opened the Chinese one itself.
    api.intent.mockResolvedValueOnce({
      data: intentView({
        revision: 4,
        items: [
          intentItem('uninstall', 'en-US', false, false),
          intentItem('alternatives', 'zh-Hant', true, true, 'locale_closed'),
          intentItem('alternatives', 'zh-CN', true, true),
          intentItem('alternatives', 'en-US', false, true)
        ]
      }),
      error: null
    });
    api.saveIntent.mockResolvedValueOnce(conflict);
    await saveButton(wrapper)!.trigger('click');
    await flushPromises();
    expect(api.intent).toHaveBeenCalledTimes(2);
    expect(wrapper.get('[data-testid="intent-save-error"]').text()).toBe(
      'page.catalog.packageDetail.index.intent.reloaded'
    );
    const changedRow = 'page.catalog.packageDetail.index.intent.changedRow';
    // Untouched here but changed elsewhere: the new value, flagged, never sent back.
    expect(row('alternatives:en-US').get('.n-switch').classes()).not.toContain('n-switch--active');
    expect(row('alternatives:en-US').text()).toContain(changedRow);
    expect(row('alternatives:zh-CN').text()).toContain(changedRow);
    // Unchanged on the server: the unsaved switch survives and is the only one saved, against the new revision.
    expect(row('uninstall:en-US').get('.n-switch').classes()).toContain('n-switch--active');
    expect(row('uninstall:en-US').text()).not.toContain(changedRow);
    api.saveIntent.mockResolvedValueOnce({ data: intentView({ revision: 5 }), error: null });
    await saveButton(wrapper)!.trigger('click');
    await flushPromises();
    expect(api.saveIntent).toHaveBeenLastCalledWith(
      7,
      { items: [{ pageKind: 'uninstall', locale: 'en-US', enabled: true }] },
      4
    );
    expect(wrapper.text()).not.toContain(changedRow);
    wrapper.unmount();
  });

  it('Keeps a link app’s uninstall pages switched off', async () => {
    api.intent.mockResolvedValueOnce({
      data: intentView({ items: [intentItem('uninstall', 'en-US', false, false, 'unsupported_page')] }),
      error: null
    });
    const wrapper = intent();
    await flushPromises();
    const row = wrapper.get('[data-testid="intent-uninstall:en-US"]');
    expect(row.get('.n-switch').classes()).toContain('n-switch--disabled');
    expect(row.text()).toContain('unsupported_page');
    wrapper.unmount();
  });

  it('Stays read-only without catalog:package:edit', async () => {
    const wrapper = intent(false);
    await flushPromises();
    expect(saveButton(wrapper)).toBeUndefined();
    expect(wrapper.get('[data-testid="intent-alternatives:zh-CN"] .n-switch').classes()).toContain(
      'n-switch--disabled'
    );
    wrapper.unmount();
  });
});

describe('Search snippet overrides', () => {
  const container = defineComponent({
    setup:
      (_, { slots }) =>
      () =>
        h('div', slots.default?.())
  });
  // Every language's pane renders at once, labelled with its tab text.
  const pane = defineComponent({
    props: ['name', 'tab'],
    setup:
      (props, { slots }) =>
      () =>
        h('section', { 'data-tab': props.tab }, slots.default?.())
  });
  const confirmation = defineComponent({
    emits: ['positive-click'],
    setup:
      (_, { slots, emit }) =>
      () =>
        h('div', [slots.trigger?.(), h('button', { onClick: () => emit('positive-click') }, 'confirm clear')])
  });
  const overrides = (editable = true) =>
    mount(SeoOverride, {
      props: { packageId: 7, editable },
      global: { components, stubs: { NTabs: container, NTabPane: pane, NPopconfirm: confirmation } }
    });
  const panel = (wrapper: ReturnType<typeof overrides>, locale: string) => wrapper.get(`[data-locale="${locale}"]`);
  const save = (wrapper: ReturnType<typeof overrides>, locale: string) =>
    panel(wrapper, locale)
      .findAll('button')
      .find(node => node.text() === 'page.catalog.packageDetail.index.snippet.save')!;

  it('Saves one language with trimmed text, the read revision and null for a cleared field', async () => {
    const wrapper = overrides();
    await flushPromises();
    expect(wrapper.findAll('section').map(node => node.attributes('data-tab'))[0]).toContain(
      'page.catalog.packageDetail.index.snippet.overridden'
    );
    const english = panel(wrapper, 'en-US');
    expect((english.get('input').element as HTMLInputElement).value).toBe('Saved title');
    expect(english.text()).toContain('page.shared.updatedBy:page.shared.owner.agent:3');
    expect(save(wrapper, 'en-US').attributes('disabled')).toBeDefined();
    api.saveSeo.mockResolvedValueOnce({ data: seoView({ revision: 6 }), error: null });
    await english.get('input').setValue('  New title  ');
    await english.get('textarea').setValue('');
    await save(wrapper, 'en-US').trigger('click');
    await flushPromises();
    expect(api.saveSeo).toHaveBeenCalledWith(7, { locale: 'en-US', title: 'New title', description: null }, 5);
    wrapper.unmount();
  });

  it('Keeps both fields on one line and refuses markup', async () => {
    const wrapper = overrides();
    await flushPromises();
    const japanese = panel(wrapper, 'ja-JP');
    await japanese.get('textarea').setValue('First line\nSecond line');
    expect((japanese.get('textarea').element as HTMLTextAreaElement).value).toBe('First line Second line');
    // The server refuses every control character, so tabs and Windows line breaks become one space too.
    await japanese.get('textarea').setValue('Tab\there\r\nnext');
    expect((japanese.get('textarea').element as HTMLTextAreaElement).value).toBe('Tab here next');
    expect(save(wrapper, 'ja-JP').attributes('disabled')).toBeUndefined();
    await japanese.get('input').setValue('<b>Bold</b> title');
    expect(japanese.text()).toContain('page.catalog.packageDetail.index.snippet.markup');
    expect(save(wrapper, 'ja-JP').attributes('disabled')).toBeDefined();
    wrapper.unmount();
  });

  it('Clears an override after confirmation and leaves Traditional Chinese read-only', async () => {
    const wrapper = overrides();
    await flushPromises();
    const traditional = panel(wrapper, 'zh-Hant');
    expect(traditional.text()).toContain('page.shared.i18n.derivedHint');
    expect(traditional.get('input').attributes('disabled')).toBeDefined();
    expect(traditional.findAll('button')).toHaveLength(0);
    api.saveSeo.mockResolvedValueOnce({ data: seoView({ revision: 6, items: [] }), error: null });
    await panel(wrapper, 'en-US')
      .findAll('button')
      .find(node => node.text() === 'confirm clear')!
      .trigger('click');
    await flushPromises();
    expect(api.saveSeo).toHaveBeenCalledWith(7, { locale: 'en-US', title: null, description: null }, 5);
    expect((panel(wrapper, 'en-US').get('input').element as HTMLInputElement).value).toBe('');
    wrapper.unmount();
  });

  it('Shows the Traditional Chinese derived from Simplified Chinese and refreshes it after a save', async () => {
    api.seo.mockResolvedValueOnce({
      data: seoView({ items: [seoItem('zh-CN', '简体标题', null), seoItem('zh-Hant', '簡體標題', null)] }),
      error: null
    });
    const wrapper = overrides();
    await flushPromises();
    const traditional = () => panel(wrapper, 'zh-Hant').get('input').element as HTMLInputElement;
    expect(traditional().value).toBe('簡體標題');
    expect(panel(wrapper, 'zh-Hant').get('input').attributes('disabled')).toBeDefined();
    await panel(wrapper, 'ja-JP').get('input').setValue('Draft in Japanese');
    await panel(wrapper, 'zh-CN').get('input').setValue('新的标题');
    api.saveSeo.mockResolvedValueOnce({
      data: seoView({ revision: 6, items: [seoItem('zh-CN', '新的标题', null), seoItem('zh-Hant', '新的標題', null)] }),
      error: null
    });
    await save(wrapper, 'zh-CN').trigger('click');
    await flushPromises();
    expect(api.saveSeo).toHaveBeenCalledWith(7, { locale: 'zh-CN', title: '新的标题', description: null }, 5);
    expect(traditional().value).toBe('新的標題');
    expect((panel(wrapper, 'ja-JP').get('input').element as HTMLInputElement).value).toBe('Draft in Japanese');
    wrapper.unmount();
  });

  it('After a revision conflict, drops drafts of languages someone else changed and keeps the rest', async () => {
    api.seo.mockResolvedValueOnce({
      data: seoView({
        items: [seoItem('en-US', 'Saved title', 'Saved description'), seoItem('ja-JP', '保存済み', null)]
      }),
      error: null
    });
    const wrapper = overrides();
    await flushPromises();
    const value = (locale: string) => (panel(wrapper, locale).get('input').element as HTMLInputElement).value;
    await panel(wrapper, 'en-US').get('input').setValue('Changed title');
    await panel(wrapper, 'ja-JP').get('input').setValue('Draft in Japanese');
    await panel(wrapper, 'es-ES').get('input').setValue('Draft in Spanish');
    // Meanwhile an agent rewrote the Japanese title; the shared revision moved on.
    api.seo.mockResolvedValueOnce({
      data: seoView({
        revision: 6,
        items: [
          seoItem('en-US', 'Saved title', 'Saved description'),
          seoItem('ja-JP', 'Agent の題名', null, 'agent:3:7')
        ]
      }),
      error: null
    });
    api.saveSeo.mockResolvedValueOnce(conflict);
    await save(wrapper, 'en-US').trigger('click');
    await flushPromises();
    expect(api.seo).toHaveBeenCalledTimes(2);
    // Changed elsewhere: the draft is gone, the new text shows, and the editor is told, also above the tabs.
    expect(value('ja-JP')).toBe('Agent の題名');
    expect(panel(wrapper, 'ja-JP').text()).toContain('page.catalog.packageDetail.index.snippet.changed');
    expect(wrapper.get('[data-testid="seo-override-changed"]').text()).toBe(
      'page.catalog.packageDetail.index.snippet.changedList:日本語'
    );
    // Unchanged on the server: the drafts survive, the one being saved included, which asks to be saved again.
    expect(value('en-US')).toBe('Changed title');
    expect(value('es-ES')).toBe('Draft in Spanish');
    expect(panel(wrapper, 'en-US').text()).toContain('page.catalog.packageDetail.index.snippet.reloaded');
    expect(panel(wrapper, 'en-US').text()).not.toContain('page.catalog.packageDetail.index.snippet.changed');
    api.saveSeo.mockResolvedValueOnce({ data: seoView({ revision: 7 }), error: null });
    await save(wrapper, 'en-US').trigger('click');
    await flushPromises();
    expect(api.saveSeo).toHaveBeenLastCalledWith(
      7,
      { locale: 'en-US', title: 'Changed title', description: 'Saved description' },
      6
    );
    wrapper.unmount();
  });

  it('Drops the draft being saved when someone else changed that same language', async () => {
    const wrapper = overrides();
    await flushPromises();
    await panel(wrapper, 'en-US').get('input').setValue('Changed title');
    api.seo.mockResolvedValueOnce({
      data: seoView({ revision: 6, items: [seoItem('en-US', 'Agent title', 'Saved description', 'agent:3:7')] }),
      error: null
    });
    api.saveSeo.mockResolvedValueOnce(conflict);
    await save(wrapper, 'en-US').trigger('click');
    await flushPromises();
    const english = panel(wrapper, 'en-US');
    expect((english.get('input').element as HTMLInputElement).value).toBe('Agent title');
    expect(english.text()).toContain('page.catalog.packageDetail.index.snippet.changed');
    expect(english.text()).not.toContain('page.catalog.packageDetail.index.snippet.reloaded');
    expect(save(wrapper, 'en-US').attributes('disabled')).toBeDefined();
    wrapper.unmount();
  });

  it('Shows no save or clear buttons without catalog:package:edit', async () => {
    const wrapper = overrides(false);
    await flushPromises();
    expect(panel(wrapper, 'en-US').findAll('button')).toHaveLength(0);
    expect(panel(wrapper, 'en-US').get('input').attributes('disabled')).toBeDefined();
    wrapper.unmount();
  });
});

describe('Write owners and conflicts', () => {
  it('Names who wrote a change and tells a revision conflict by its code', () => {
    expect(writeOwnerLabel('admin:12')).toBe('page.shared.owner.admin:12');
    expect(writeOwnerLabel('agent:3:7')).toBe('page.shared.owner.agent:3');
    expect(writeOwnerLabel('system')).toBe('page.shared.owner.system');
    expect(writeOwnerLabel('cron')).toBe('cron');
    expect(isRevisionConflict(conflict.response)).toBe(true);
    expect(isRevisionConflict({ data: { code: '1001', msg: 'Invalid (seo_text_invalid:title)' } })).toBe(false);
    expect(isRevisionConflict(undefined)).toBe(false);
  });
});

describe('Impact report', () => {
  it('Retries a failed initial request and refreshes when settings change', async () => {
    api.report.mockResolvedValueOnce({ data: undefined, error: new Error('Unavailable') });
    const wrapper = mount(IndexReport, {
      global: { components, stubs: { RouterLink: { template: '<a><slot /></a>' } } }
    });
    await flushPromises();
    expect(wrapper.text()).toContain('page.system.seo.report.loadFailed');
    await wrapper.find('button').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('Thin App');
    expect(wrapper.text()).not.toContain('page.system.seo.report.loadFailed');
    await wrapper.setProps({ revision: 1 });
    await flushPromises();
    expect(api.report).toHaveBeenCalledTimes(3);
    wrapper.unmount();
  });

  it('Ignores a slow response from before a settings refresh', async () => {
    let resolveOld!: (value: unknown) => void;
    api.report.mockImplementationOnce(
      () =>
        new Promise(resolve => {
          resolveOld = resolve;
        })
    );
    const wrapper = mount(IndexReport, {
      global: { components, stubs: { RouterLink: { template: '<a><slot /></a>' } } }
    });
    await wrapper.setProps({ revision: 1 });
    await flushPromises();
    expect(wrapper.text()).toContain('Thin App');
    resolveOld({ error: new Error('Old failure') });
    await flushPromises();
    expect(wrapper.text()).not.toContain('page.system.seo.report.loadFailed');
    expect(wrapper.text()).toContain('Thin App');
    wrapper.unmount();
  });

  it('Lists affected apps with counts and filters by reason', async () => {
    const wrapper = mount(IndexReport, {
      global: { components, stubs: { RouterLink: { template: '<a><slot /></a>' } } }
    });
    await flushPromises();
    expect(wrapper.text()).toContain('Thin App');
    expect(wrapper.find('[data-testid="index-report-counts"]').text()).toContain(
      'page.system.seo.reasons.unavailable · 2'
    );
    await wrapper.findAll('[data-testid="index-report-counts"] .n-tag')[0]!.trigger('click');
    await flushPromises();
    expect(api.report).toHaveBeenLastCalledWith({ current: 1, size: 20, reason: 'missing_summary' });
    wrapper.unmount();
  });
});

describe('Translation gate impact', () => {
  const decision = (robots: string, reason: string) => ({
    robots,
    canonicalPath: '',
    hreflang: [],
    inSitemap: robots === 'index, follow',
    announce: false,
    reason
  });

  it('Shows indexable counts per language and pages through the apps that would leave the index', async () => {
    api.report.mockResolvedValue({
      data: {
        records: [],
        current: 1,
        size: 20,
        total: 0,
        counts: { hidden: 0, forcedNoindex: 0, unavailable: 0, missingSummary: 0, thin: 0, forcedIndex: 0 },
        translationImpact: {
          mode: 'shadow',
          counts: [
            { locale: 'en-US', indexableNow: 120, indexableEnforced: 120, losing: 0 },
            { locale: 'ja-JP', indexableNow: 90, indexableEnforced: 71, losing: 19 }
          ],
          records: [
            {
              id: 7,
              token: 'stale-translation',
              locale: 'ja-JP',
              current: decision('index, follow', 'indexable'),
              enforced: decision('noindex, follow', 'translation_stale')
            }
          ],
          current: 1,
          size: 20,
          total: 41
        }
      },
      error: undefined
    });
    const wrapper = mount(TranslationImpact, {
      global: { components, stubs: { RouterLink: { template: '<a><slot /></a>' } } }
    });
    await flushPromises();
    const counts = wrapper.find('[data-testid="translation-impact-counts"]').text();
    expect(counts).toContain('English');
    expect(counts).toContain('日本語');
    expect(counts).toContain('19');
    expect(wrapper.text()).toContain('stale-translation');
    expect(wrapper.text()).toContain('translation_stale');
    await wrapper.findComponent(NPagination).vm.$emit('update:page', 2);
    await flushPromises();
    expect(api.report).toHaveBeenLastCalledWith({ current: 2, size: 20 });
    wrapper.unmount();
  });
});
