import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { OnConfirm } from '@opennavo/ui';
import BrewfilePage from '@/pages/BrewfilePage.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { useBrewfileStore } from '@/stores/brewfile';
import { useLibraryStore, useTasksStore } from '@/stores';
import { setup } from './helpers';

const wrappers: { unmount: () => void }[] = [];
beforeEach(() => localStorage.clear());
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
});

async function page() {
  const { pinia, router } = await setup('/brewfile');
  await useLibraryStore().load();
  const list = useBrewfileStore();
  list.add('cask', 'zed');
  list.add('cask', 'visual-studio-code'); // Already installed in mock library.
  list.add('cask', 'missing-app');
  const wrapper = mount(BrewfilePage, { global: { plugins: [pinia, router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  return { wrapper, list, tasks: useTasksStore() };
}

describe('My List page', () => {
  it('requires confirmation and only queues available uninstalled apps', async () => {
    const { wrapper, tasks } = await page();
    const enqueue = vi.spyOn(tasks, 'enqueueMany').mockResolvedValue([]);
    const installButton = wrapper.findAll('button').find(button => button.text() === '安装 1 个软件');
    expect(installButton).toBeDefined();
    await installButton!.trigger('click');
    expect(enqueue).not.toHaveBeenCalled();
    expect(wrapper.findComponent(OnConfirm).props('open')).toBe(true);
    wrapper.findComponent(OnConfirm).vm.$emit('confirm');
    await flushPromises();
    expect(enqueue).toHaveBeenCalledWith('install', [{ kind: 'cask', token: 'zed' }], 'bundle');
  });
  it('keeps app names while a reorder rereads the list', async () => {
    const { wrapper, list } = await page();
    expect(wrapper.text()).toContain('VS Code 编辑器');
    const get = commands.catalogGet;
    let release: () => void = () => {};
    const held = new Promise<void>(resolve => (release = resolve));
    vi.spyOn(commands, 'catalogGet').mockImplementation(async (kind, token) => {
      await held;
      return get(kind, token);
    });
    list.move(2, 0);
    await flushPromises();
    // The reread is in flight: rows keep their names instead of falling back to tokens.
    expect(wrapper.text()).toContain('VS Code 编辑器');
    expect(wrapper.text()).toContain('Zed 代码编辑器');
    release();
    await flushPromises();
    expect(wrapper.text()).toContain('VS Code 编辑器');
  });
  it('exports the saved list, including unavailable and installed apps, in the selected order', async () => {
    const { wrapper, list } = await page();
    list.move(2, 0);
    await flushPromises();
    const save = vi.spyOn(commands, 'brewfileExport').mockResolvedValue({ status: 'ok', data: 3 });
    await wrapper
      .findAll('button')
      .find(button => button.text() === '导出 Brewfile')!
      .trigger('click');
    await flushPromises();
    expect(save).toHaveBeenCalledWith('~/Brewfile', [
      { kind: 'cask', token: 'missing-app' },
      { kind: 'cask', token: 'zed' },
      { kind: 'cask', token: 'visual-studio-code' }
    ]);
  });
});
