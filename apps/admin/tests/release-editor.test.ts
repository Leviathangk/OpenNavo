import { flushPromises, mount } from '@vue/test-utils';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import ReleaseEditor from '../src/components/opennavo/release-editor.vue';

const api = vi.hoisted(() => ({ load: vi.fn(), save: vi.fn() }));
vi.mock('@/service/api', () => ({ fetchReleaseDetail: api.load, updateReleaseI18n: api.save }));
vi.mock('@/hooks/business/auth', () => ({ useAuth: () => ({ hasAuth: () => true }) }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('../src/components/opennavo/markdown-editor.vue', () => ({ default: { template: '<div />' } }));
vi.mock('../src/components/opennavo/ui-preview.vue', () => ({ default: { template: '<div><slot /></div>' } }));

const container = defineComponent({
  setup:
    (_, { slots }) =>
    () =>
      h('div', [slots.default?.(), slots.footer?.()])
});
const button = defineComponent({
  props: { disabled: Boolean },
  setup:
    (props, { slots }) =>
    () =>
      h('button', { disabled: props.disabled }, slots.default?.())
});
const input = defineComponent({ props: { value: String }, setup: props => () => h('input', { value: props.value }) });
function editor(locale?: 'ja-JP' | 'zh-CN') {
  return mount(ReleaseEditor, {
    props: { releaseId: 1, show: true, ...(locale ? { locale } : {}) },
    global: {
      stubs: {
        NDrawer: container,
        NDrawerContent: container,
        NSpin: container,
        NForm: container,
        NGrid: container,
        NGi: container,
        NFormItem: container,
        NButton: button,
        NInput: input,
        NAlert: container,
        PermissionGate: container,
        OnVersionEntry: true,
        OnMarkdown: true
      }
    }
  });
}
function release(id: number) {
  return {
    data: {
      id,
      version: `${id}.0`,
      packageName: `Package ${id}`,
      source: 'manual',
      isPrerelease: false,
      i18n: [{ locale: 'zh-CN', summary: `Translation ${id}`, sections: [], bodyMarkdown: null }]
    },
    error: undefined
  };
}
const saveButton = (wrapper: ReturnType<typeof editor>) =>
  wrapper.findAll('button').find(node => node.text() === 'page.changelog.editor.save')!;

beforeEach(() => {
  api.load.mockReset();
  api.save.mockReset();
  api.save.mockResolvedValue({ error: undefined });
});

describe('Translations belong to the current version', () => {
  it('defaults to English while retaining explicit Chinese translations', async () => {
    const response = release(1);
    response.data.i18n.push({ locale: 'en-US', summary: 'English summary', sections: [], bodyMarkdown: null });
    api.load.mockResolvedValue(response);
    const wrapper = editor();
    await flushPromises();
    expect(wrapper.find('input').element.value).toBe('English summary');
    await saveButton(wrapper).trigger('click');
    expect(api.save).toHaveBeenCalledWith(1, 'en-US', { summary: 'English summary', sections: [], bodyMarkdown: null });
    expect(response.data.i18n[0].locale).toBe('zh-CN');
    wrapper.unmount();
  });

  it('Opening Japanese entries reads/saves Japanese without submitting Chinese', async () => {
    const response = release(1);
    response.data.i18n.push({ locale: 'ja-JP', summary: '日本語の説明', sections: [], bodyMarkdown: null });
    api.load.mockResolvedValue(response);
    const wrapper = editor('ja-JP');
    await flushPromises();
    expect(wrapper.find('input').element.value).toBe('日本語の説明');
    expect(wrapper.findComponent({ name: 'OnVersionEntry' }).props('entry').translation.locale).toBe('ja-JP');
    await saveButton(wrapper).trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledWith(1, 'ja-JP', { summary: '日本語の説明', sections: [], bodyMarkdown: null });
    wrapper.unmount();
  });

  it('Switching locale within a version rejects late Chinese responses', async () => {
    let finish!: (value: ReturnType<typeof release>) => void;
    const response = release(1);
    response.data.i18n.push({ locale: 'ja-JP', summary: '日本語の説明', sections: [], bodyMarkdown: null });
    api.load
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finish = resolve;
          })
      )
      .mockResolvedValueOnce(response);
    const wrapper = editor('zh-CN');
    await wrapper.setProps({ locale: 'ja-JP' });
    await flushPromises();
    finish(release(1));
    await flushPromises();
    expect(wrapper.find('input').element.value).toBe('日本語の説明');
    await saveButton(wrapper).trigger('click');
    expect(api.save).toHaveBeenCalledWith(1, 'ja-JP', expect.objectContaining({ summary: '日本語の説明' }));
    wrapper.unmount();
  });

  it('Failed B load clears A form and disables saving', async () => {
    api.load.mockResolvedValueOnce(release(1)).mockResolvedValueOnce({ data: null, error: new Error('offline') });
    const wrapper = editor('zh-CN');
    await flushPromises();
    expect(wrapper.find('input').element.value).toBe('Translation 1');
    await wrapper.setProps({ releaseId: 2 });
    await flushPromises();
    expect(wrapper.find('input').exists()).toBe(false);
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined();
    await saveButton(wrapper).trigger('click');
    expect(api.save).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('Late A responses cannot overwrite B; saves submit only B translation', async () => {
    let finish!: (value: ReturnType<typeof release>) => void;
    api.load
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finish = resolve;
          })
      )
      .mockResolvedValueOnce(release(2));
    const wrapper = editor('zh-CN');
    await wrapper.setProps({ releaseId: 2 });
    await flushPromises();
    finish(release(1));
    await flushPromises();
    expect(wrapper.find('input').element.value).toBe('Translation 2');
    await saveButton(wrapper).trigger('click');
    await flushPromises();
    expect(api.save).toHaveBeenCalledWith(2, 'zh-CN', { summary: 'Translation 2', sections: [], bodyMarkdown: null });
    wrapper.unmount();
  });
});
