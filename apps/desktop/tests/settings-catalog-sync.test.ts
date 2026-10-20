import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import SettingsPage from '@/pages/SettingsPage.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { useToasts } from '@/composables/useToasts';
import { useCatalogStore } from '@/stores';
import { setup } from './helpers';

vi.mock('@/composables/useClientConfig', () => ({ fetchClientConfig: async () => null }));

const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  useToasts().clear();
  vi.restoreAllMocks();
});

async function aboutPage() {
  const { pinia, router } = await setup('/settings/about');
  await useCatalogStore().loadStatus();
  const wrapper = mount(SettingsPage, { global: { plugins: [pinia, router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  const sync = wrapper.findAll('button').find(button => button.text() === i18n.global.t('settings.about.sync'))!;
  expect(sync).toBeTruthy();
  return sync;
}
const titles = () => useToasts().items.value.map(toast => toast.title);
const synced = { status: 'ok', data: { mode: 'none', applied: 0, cursor: 60 } } as const;

describe('Settings: sync the catalog', () => {
  it('A clean sync says nothing and forces a full pass', async () => {
    const sync = await aboutPage();
    const call = vi.spyOn(commands, 'catalogSync').mockResolvedValue(synced);
    await sync.trigger('click');
    await flushPromises();
    expect(call).toHaveBeenCalledWith(true);
    expect(titles()).toEqual([]);
  });

  it('Tells the user when the third-party source stayed down although the sync itself succeeded', async () => {
    const sync = await aboutPage();
    const status = commands.catalogStatus;
    vi.spyOn(commands, 'catalogStatus').mockImplementation(async () => {
      const response = await status();
      if (response.status === 'ok') response.data.linksError = 'E_NETWORK';
      return response;
    });
    vi.spyOn(commands, 'catalogSync').mockResolvedValue(synced);
    await sync.trigger('click');
    await flushPromises();
    expect(titles()).toEqual([i18n.global.t('search.catalogIncomplete')]);
  });

  it('Reports a failed sync once and lets the button be used again', async () => {
    const sync = await aboutPage();
    const call = vi.spyOn(commands, 'catalogSync').mockResolvedValue({
      status: 'error',
      error: { code: 'E_NETWORK', message: 'offline', detail: null }
    });
    await sync.trigger('click');
    await flushPromises();
    expect(titles()).toEqual([i18n.global.t('common.refreshFailed')]);
    expect(sync.attributes('disabled')).toBeUndefined();
    await sync.trigger('click');
    await flushPromises();
    expect(call).toHaveBeenCalledTimes(2);
  });
});
