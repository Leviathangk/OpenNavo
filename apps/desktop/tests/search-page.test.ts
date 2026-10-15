import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { startLocalState } from '@/stores';
import SearchPage from '@/pages/SearchPage.vue';
import { setup } from './helpers';

const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
});

describe('Search page', () => {
  it('Keeps the last results and the count line while the next search is in flight', async () => {
    const { pinia, router } = await setup('/search');
    await startLocalState();
    const search = commands.catalogSearch;
    let release: () => void = () => {};
    vi.spyOn(commands, 'catalogSearch').mockImplementation(async query => {
      if (query.q === 'cod') await new Promise<void>(resolve => (release = resolve));
      return search(query);
    });
    const wrapper = mount(SearchPage, { global: { plugins: [pinia, router, i18n] } });
    wrappers.push(wrapper);
    await flushPromises();
    const subtitle = () => wrapper.find('header p');
    const prompt = '输入名称、用途或命令开始搜索';

    // Without a query the count line still holds its height, so typing never moves the field.
    expect(subtitle().exists()).toBe(true);
    expect(wrapper.find('h3').text()).toBe(prompt);

    const field = wrapper.find('input');
    await field.setValue('co');
    await flushPromises();
    const rows = wrapper.findAll('li').length;
    expect(rows).toBeGreaterThan(0);
    expect(subtitle().text()).toBe(`「co」的 ${rows} 个结果`);

    // The next search has not landed: the page keeps the previous results instead of emptying.
    await field.setValue('cod');
    await flushPromises();
    expect(wrapper.findAll('li')).toHaveLength(rows);
    expect(subtitle().text()).toBe(`「co」的 ${rows} 个结果`);
    release();
    await flushPromises();
    expect(subtitle().text()).toMatch(/^「cod」的 \d+ 个结果$/);

    // Clearing the field shows the prompt at once, not the previous query's results.
    await field.setValue('');
    await flushPromises();
    expect(wrapper.findAll('li')).toHaveLength(0);
    expect(wrapper.find('h3').text()).toBe(prompt);
    expect(subtitle().exists()).toBe(true);
  });
});
