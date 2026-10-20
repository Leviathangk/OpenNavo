import { mount } from '@vue/test-utils';
import { defineComponent, h } from 'vue';
import { expect, it, vi } from 'vitest';
import FixDrawer from '../src/views/changelog/review/modules/fix-drawer.vue';
import { APP_CONTENT_LOCALES, CONTENT_LOCALES } from '../src/utils/content-locale';

vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/service/api', () => ({ fixTranslation: vi.fn() }));

const passthrough = defineComponent({
  setup:
    (_, { slots }) =>
    () =>
      h('div', [slots.default?.(), slots.footer?.()])
});
const select = defineComponent({
  props: ['value', 'options'],
  setup: props => () =>
    h(
      'select',
      { value: props.value },
      props.options.map((option: { value: string }) => h('option', { value: option.value }, option.value))
    )
});

it.each(['desktop_release', 'package'])('limits %s fixes to its supported languages', entity => {
  const i18n = Object.fromEntries(
    APP_CONTENT_LOCALES.map(locale => [
      locale,
      { status: locale === 'en-US' ? 'source' : 'manual', fields: { notes: 'Release notes' } }
    ])
  );
  const wrapper = mount(FixDrawer, {
    props: { show: true, item: { ref: { entity, id: 1 }, sourceLocale: 'en-US', i18n, label: 'Release' } as any },
    global: {
      stubs: {
        NDrawer: passthrough,
        NDrawerContent: passthrough,
        NText: passthrough,
        NSelect: select,
        NInput: true,
        NButton: true,
        NEmpty: true
      }
    }
  });
  const options = wrapper.findAll('option').map(option => option.attributes('value'));
  expect(options).toEqual(
    (entity === 'desktop_release' ? APP_CONTENT_LOCALES : CONTENT_LOCALES).filter(code => code !== 'en-US')
  );
  const selected = (wrapper.find('select').element as HTMLSelectElement).value;
  expect(options).toContain(selected);
  if (entity === 'desktop_release') expect(APP_CONTENT_LOCALES).toContain(selected);
  wrapper.unmount();
});
