import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import CollectionsPage from '@/pages/CollectionsPage.vue';
import { i18n } from '@/i18n';
import { setup } from './helpers';

let unmount: (() => void) | undefined;

beforeEach(() => localStorage.clear());
afterEach(() => {
  unmount?.();
  unmount = undefined;
});

describe('Collections and My List', () => {
  it('Keeps My List inside Collections and switches back to the collection grid', async () => {
    const { pinia, router } = await setup('/collections#list');
    const wrapper = mount(CollectionsPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    unmount = () => wrapper.unmount();
    await flushPromises();

    expect(wrapper.text()).toContain('清单');
    expect(wrapper.text()).toContain('清单是空的');

    const collections = wrapper.findAll('button').find(button => button.text().includes('合集'));
    await collections!.trigger('click');
    await flushPromises();

    expect(router.currentRoute.value.hash).toBe('');
    expect(wrapper.text()).toContain('编辑挑选的软件组合');
  });
});
