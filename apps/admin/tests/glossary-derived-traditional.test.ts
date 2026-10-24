import { shallowMount } from '@vue/test-utils';
import { expect, it, vi } from 'vitest';
import GlossaryForm from '../src/views/content/glossary/modules/glossary-form.vue';
import { upsertGlossaryTerm } from '../src/service/api';

vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/service/api', () => ({ upsertGlossaryTerm: vi.fn().mockResolvedValue({ error: null }) }));

it.each([
  { editing: false, protectedTerm: false },
  { editing: true, protectedTerm: false },
  { editing: true, protectedTerm: true }
])('saves glossary terms without derived translations: %o', async ({ editing, protectedTerm }) => {
  vi.mocked(upsertGlossaryTerm).mockClear();
  const wrapper = shallowMount(GlossaryForm, {
    props: {
      show: true,
      term: editing
        ? ({
            term: 'Memory',
            doNotTranslate: protectedTerm,
            translations: { 'zh-CN': '内存', 'zh-Hant': '記憶體' }
          } as any)
        : null
    },
    global: { stubs: ['NModal', 'NForm', 'NFormItem', 'NInput', 'NSwitch', 'NText', 'NButton'] }
  });
  const vm = wrapper.vm as any;
  if (!editing) {
    vm.model.term = 'Memory';
    vm.model.translations['zh-CN'] = '内存';
  }
  await vm.save();
  const body = vi.mocked(upsertGlossaryTerm).mock.calls[0][0];
  expect(body.translations).not.toHaveProperty('zh-Hant');
  expect(body.translations['zh-CN']).toBe(protectedTerm ? null : '内存');
  expect(body.doNotTranslate).toBe(protectedTerm);
  expect(wrapper.emitted('saved')).toHaveLength(1);
  wrapper.unmount();
});
