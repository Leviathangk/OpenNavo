import { flushPromises, mount } from '@vue/test-utils';
import {
  NAlert,
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NPagination,
  NRadioButton,
  NRadioGroup,
  NSkeleton,
  NSpin,
  NTable,
  NTag,
  NText
} from 'naive-ui';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import IndexTab from '../src/views/catalog/package-detail/modules/index-tab.vue';
import IndexReport from '../src/views/system/seo/modules/index-report.vue';

const api = vi.hoisted(() => ({ status: vi.fn(), save: vi.fn(), report: vi.fn() }));
vi.mock('@/service/api', () => ({
  fetchPackageIndexStatus: api.status,
  updatePackageIndexStatus: api.save,
  fetchIndexReport: api.report
}));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));

const components = {
  NAlert,
  NButton,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NPagination,
  NRadioButton,
  NRadioGroup,
  NSkeleton,
  NSpin,
  NTable,
  NTag,
  NText
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

beforeEach(() => {
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
  it('Shows the verdict and requires a reason before forcing a page in', async () => {
    const wrapper = mount(IndexTab, { props: { packageId: 7 }, global: { components } });
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
    const wrapper = mount(IndexTab, { props: { packageId: 7 }, global: { components } });
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
