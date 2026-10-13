import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import AboutEditor from '../src/views/release/config/about-editor.vue';

vi.mock('@/locales', () => ({ $t: (key: string) => key }));
const container = defineComponent({
  setup:
    (_, { slots }) =>
    () =>
      h('div', slots.default?.())
});
const tabs = defineComponent({
  props: ['defaultValue'],
  setup:
    (props, { slots }) =>
    () =>
      h('div', { 'data-default-locale': props.defaultValue }, slots.default?.())
});
const input = defineComponent({
  props: ['value', 'disabled'],
  emits: ['update:value'],
  setup:
    (props, { emit }) =>
    () =>
      h('input', {
        value: props.value,
        disabled: props.disabled,
        onInput: (event: Event) => emit('update:value', (event.target as HTMLInputElement).value)
      })
});
function editor(sourceLocale?: string | null) {
  return mount(AboutEditor, {
    props: {
      modelValue: JSON.stringify({
        ...(sourceLocale !== undefined ? { sourceLocale } : {}),
        locales: {
          'en-US': [{ key: 'intro', title: 'English', paragraphs: ['English content'], draft: true }],
          'zh-CN': [{ key: 'intro', title: '中文', paragraphs: ['中文内容'], draft: true }]
        },
        modules: []
      })
    },
    global: {
      stubs: {
        NTabs: tabs,
        NTabPane: container,
        NFormItem: container,
        NInput: input,
        NCheckbox: true,
        NText: container
      }
    }
  });
}

describe('About authoring locale', () => {
  it.each([undefined, null, ''])(
    'defaults sourceLocale=%s to English without removing Chinese data',
    async sourceLocale => {
      const wrapper = editor(sourceLocale);
      const supplied = JSON.parse(wrapper.props('modelValue'));
      expect(supplied.sourceLocale).toBe(sourceLocale);
      expect(wrapper.find('[data-default-locale]').attributes('data-default-locale')).toBe('en-US');
      const inputs = wrapper.findAll('input');
      expect(inputs[0].element.disabled).toBe(false);
      expect(inputs[2].element.disabled).toBe(true);
      await inputs[0].setValue('Updated English');
      const saved = JSON.parse(wrapper.emitted('update:modelValue')![0][0] as string);
      expect(saved.sourceLocale).toBe('en-US');
      expect(saved.locales['en-US'][0].title).toBe('Updated English');
      expect(saved.locales['zh-CN'][0].title).toBe('中文');
      wrapper.unmount();
    }
  );

  it('preserves an existing explicit Chinese source locale when editing', async () => {
    const wrapper = editor('zh-CN');
    expect(wrapper.find('[data-default-locale]').attributes('data-default-locale')).toBe('zh-CN');
    const inputs = wrapper.findAll('input');
    expect(inputs[0].element.disabled).toBe(true);
    expect(inputs[2].element.disabled).toBe(false);
    await inputs[2].setValue('更新后的中文');
    const saved = JSON.parse(wrapper.emitted('update:modelValue')![0][0] as string);
    expect(saved.sourceLocale).toBe('zh-CN');
    expect(saved.locales['zh-CN'][0].title).toBe('更新后的中文');
    expect(saved.locales['en-US'][0].title).toBe('English');
    wrapper.unmount();
  });
});
