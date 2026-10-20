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
  it('Shows a failed links sync, keeps cached results, and refreshes after retry', async () => {
    const { pinia, router } = await setup('/search?q=code');
    const status = commands.catalogStatus;
    let failed = true;
    vi.spyOn(commands, 'catalogStatus').mockImplementation(async () => {
      const response = await status();
      if (response.status === 'ok') response.data.linksError = failed ? 'E_NETWORK' : null;
      return response;
    });
    // A links failure does not fail the sync: the casks synced, and the error travels in the catalog status.
    const sync = vi
      .spyOn(commands, 'catalogSync')
      .mockResolvedValue({ status: 'ok', data: { mode: 'none', applied: 0, cursor: 60 } });
    const search = vi.spyOn(commands, 'catalogSearch');
    const wrapper = mount(SearchPage, { global: { plugins: [pinia, router, i18n] } });
    wrappers.push(wrapper);
    await flushPromises();
    const hint = '第三方应用目录同步失败，搜索结果可能不完整。';
    expect(wrapper.text()).toContain(hint);
    const rows = wrapper.findAll('li').length;
    expect(rows).toBeGreaterThan(0);
    const retry = () => wrapper.findAll('button').find(button => button.text() === '重试')!;
    await retry().trigger('click');
    await flushPromises();
    expect(sync).toHaveBeenCalledTimes(1);
    expect(wrapper.text()).toContain(hint);
    expect(wrapper.findAll('li')).toHaveLength(rows);
    failed = false;
    const before = search.mock.calls.length;
    await retry().trigger('click');
    await flushPromises();
    expect(wrapper.text()).not.toContain(hint);
    expect(search.mock.calls.length).toBeGreaterThan(before);
  });

  it('Keeps the last results and the count line while the next search is in flight', async () => {
    const { pinia, router } = await setup('/search');
    await startLocalState();
    const search = commands.catalogSearch;
    // One search reads casks and link apps in parallel; hold every read for "cod" until released.
    const held: (() => void)[] = [];
    const release = () => held.splice(0).forEach(resolve => resolve());
    vi.spyOn(commands, 'catalogSearch').mockImplementation(async query => {
      if (query.q === 'cod') await new Promise<void>(resolve => held.push(resolve));
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
