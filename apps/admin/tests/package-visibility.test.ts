import { flushPromises, mount } from '@vue/test-utils';
import { NGi, NGrid, NForm, NFormItem, NInput, NSwitch, NButton } from 'naive-ui';
import { describe, expect, it, vi } from 'vitest';
import BasicTab from '../src/views/catalog/package-detail/modules/basic-tab.vue';
import type { DataOf } from '../src/typings/api/opennavo';

const update = vi.hoisted(() => vi.fn(async () => ({ error: undefined })));
vi.mock('@/service/api', () => ({ updatePackageMeta: update }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/hooks/business/auth', () => ({ useAuth: () => ({ hasAuth: () => true }) }));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => ({ locale: 'en-US' }) }));

const detail = {
  id: 42,
  names: ['Visibility fixture'],
  raw: {},
  hidden: false,
  private: false,
  visibilityReason: '',
  editorChoice: false,
  meta: { tags: [] }
} as unknown as DataOf<'getAdminPackage'>;

function mountBasicTab() {
  return mount(BasicTab, {
    props: { detail },
    global: {
      mocks: { $t: (key: string) => key },
      components: { NGi, NGrid, NForm, NFormItem, NInput, NSwitch, NButton },
      stubs: {
        PermissionGate: { props: ['code'], template: '<slot />' },
        NDescriptions: true,
        NDescriptionsItem: true,
        NCollapse: true,
        NCollapseItem: true,
        NInputNumber: true,
        NColorPicker: true,
        NDynamicTags: true,
        NText: true
      }
    }
  });
}

describe('Admin visibility form', () => {
  it.each(['hidden', 'private'] as const)('requires a reason before saving %s', async field => {
    update.mockClear();
    const wrapper = mountBasicTab();
    const switches = wrapper.findAllComponents(NSwitch);
    await switches[field === 'hidden' ? 0 : 1]!.trigger('click');
    const save = wrapper.findAllComponents(NButton).find(button => button.text().endsWith('basic.save'))!;
    expect(save.props('disabled')).toBe(true);
    expect(wrapper.text()).toContain('visibilityReasonRequired');
    expect(update).not.toHaveBeenCalled();
    await wrapper.get('textarea').setValue('  Catalog visibility review.  ');
    expect(save.props('disabled')).toBe(false);
    await save.trigger('click');
    await flushPromises();
    expect(update).toHaveBeenCalledWith(
      42,
      expect.objectContaining({
        [field]: true,
        visibilityReason: 'Catalog visibility review.'
      })
    );
    await wrapper.get('textarea').setValue('');
    expect(save.props('disabled')).toBe(true);
    await switches[field === 'hidden' ? 0 : 1]!.trigger('click');
    expect(save.props('disabled')).toBe(false);
    await save.trigger('click');
    await flushPromises();
    expect(update).toHaveBeenLastCalledWith(42, expect.objectContaining({ [field]: false }));
    expect(update).toHaveBeenLastCalledWith(42, expect.not.objectContaining({ visibilityReason: expect.anything() }));
    wrapper.unmount();
  });

  it('shows the paused CDN purge warning alongside the success message', async () => {
    const warning = 'CDN purge is pending; edge copies may persist until the purge is resumed.';
    update.mockClear();
    update.mockResolvedValueOnce({ data: { warning }, error: undefined } as never);
    const message = { success: vi.fn(), warning: vi.fn() };
    vi.stubGlobal('$message', message);
    const wrapper = mountBasicTab();
    const save = wrapper.findAllComponents(NButton).find(button => button.text().endsWith('basic.save'))!;

    await save.trigger('click');
    await flushPromises();

    expect(message.success).toHaveBeenCalledWith('page.shared.saved');
    expect(message.warning).toHaveBeenCalledWith(warning);
    expect(wrapper.emitted('saved')).toHaveLength(1);
    wrapper.unmount();
    vi.unstubAllGlobals();
  });
});
