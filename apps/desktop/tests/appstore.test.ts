import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, ref } from 'vue';
import { createOnUi } from '@opennavo/ui';
import MasAlsoPanel from '@/components/appstore/MasAlsoPanel.vue';
import CommandPalette from '@/components/palette/CommandPalette.vue';
import { MAS_SEARCH_DEBOUNCE_MS } from '@/composables/useMasSearch';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import AppStorePage from '@/pages/AppStorePage.vue';
import SearchPage from '@/pages/SearchPage.vue';
import { startLocalState, useSettingsStore } from '@/stores';
import { setup } from './helpers';

// Mac App Store (store/security plan §5): App Store results beside the catalog, the detail page, and the palette group.
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});

const debounce = async () => {
  await new Promise(resolve => setTimeout(resolve, MAS_SEARCH_DEBOUNCE_MS + 30));
  await flushPromises();
};

async function mountSearch(flags = '') {
  if (flags) history.replaceState(null, '', `/?mock=${flags}`);
  const { pinia, router } = await setup('/search');
  await startLocalState();
  const wrapper = mount(SearchPage, { global: { plugins: [pinia, router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  return { wrapper, router };
}

async function mountDetail(id: string) {
  const { pinia, router } = await setup(`/appstore/${id}`);
  await startLocalState();
  // Shared components read their own locale (Open/打开); the app installs it from settings.
  const ui = createOnUi({ locale: ref('zh-CN' as const) });
  const wrapper = mount(AppStorePage, { props: { id }, global: { plugins: [pinia, router, i18n, ui] } });
  wrappers.push(wrapper);
  await flushPromises();
  await flushPromises();
  return { wrapper, router };
}

describe('App Store search', () => {
  it('Lists App Store results after the debounce, and the source filter narrows the page to them', async () => {
    const search = vi.spyOn(commands, 'masSearch');
    const { wrapper, router } = await mountSearch();
    await wrapper.find('input').setValue('markdown');
    await flushPromises();
    // Nothing is asked of Apple until typing pauses.
    expect(search).not.toHaveBeenCalled();
    await debounce();
    expect(search).toHaveBeenCalledTimes(1);
    expect(wrapper.text()).toContain('来自 Apple');
    expect(wrapper.text()).toContain('MarkEdit');

    const appStore = wrapper.findAll('[role="radio"]').find(option => option.text().startsWith('App Store'));
    expect(appStore?.text()).toMatch(/App Store\s*\d+/);
    await appStore?.trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.query).toMatchObject({ q: 'markdown', source: 'appstore' });
    // Only the App Store group is left; its rows open the App Store detail page.
    const links = wrapper.findAll('a').map(link => link.attributes('href') ?? '');
    expect(links.length).toBeGreaterThan(0);
    expect(links.every(href => href.startsWith('/appstore/') || href.includes('appstore'))).toBe(true);

    // The same query is answered from the cache: no second call to Apple.
    await wrapper.find('input').setValue('markdow');
    await wrapper.find('input').setValue('markdown');
    await debounce();
    expect(search.mock.calls.filter(([term]) => term === 'markdown')).toHaveLength(1);
  });

  it('Explains a rate limit instead of showing an empty App Store group', async () => {
    const { wrapper } = await mountSearch('mas-rate-limit');
    await wrapper.find('input').setValue('markdown');
    await debounce();
    expect(wrapper.text()).toContain('App Store 搜索太频繁了');
    expect(wrapper.text()).not.toContain('App Store 里没有找到');
  });
});

describe('App Store detail', () => {
  it('Get opens the product page while the App Store helper cannot install yet', async () => {
    const open = vi.spyOn(commands, 'masOpenStore');
    const { wrapper } = await mountDetail('1666327168');
    expect(wrapper.find('h1').text()).toBe('MarkEdit');
    expect(wrapper.text()).toContain('新功能 · 1.24.0');
    expect(wrapper.text()).toContain('不经过 OpenNavo 的服务器');
    const get = wrapper.findAll('button').find(button => button.text() === '获取');
    await get?.trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalledWith(1666327168);
  });

  it('Installed apps show their version and offer Open instead of Get', async () => {
    const { wrapper } = await mountDetail('1435957248');
    expect(wrapper.text()).toContain('已安装 48.0');
    expect(wrapper.findAll('button').some(button => button.text() === '打开')).toBe(true);
    expect(wrapper.findAll('button').some(button => button.text() === '获取')).toBe(false);
  });

  it('Paid apps show the price, and unknown IDs explain the storefront', async () => {
    // Downie is paid and not installed: the button shows its price and still opens the App Store.
    const open = vi.spyOn(commands, 'masOpenStore');
    const paid = await mountDetail('1480068668');
    const price = paid.wrapper.findAll('button').find(button => button.text() === '$19.99');
    await price?.trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalledWith(1480068668);
    const missing = await mountDetail('999');
    expect(missing.wrapper.text()).toContain('这款 App 在');
    expect(missing.wrapper.text()).toContain('不可用');
  });
});

describe('Command palette App Store group', () => {
  it('Adds App Store results after the catalog, and Enter opens their detail page', async () => {
    const context = await setup('/discover', { tickMs: 1000 });
    await startLocalState();
    const open = ref(true);
    const Host = defineComponent({
      setup: () => () =>
        h(CommandPalette, { open: open.value, 'onUpdate:open': (value: boolean) => (open.value = value) })
    });
    wrappers.push(mount(Host, { global: { plugins: [context.pinia, context.router, i18n] }, attachTo: document.body }));
    await flushPromises();
    const input = document.body.querySelector<HTMLInputElement>('[role="combobox"]');
    if (!input) throw new Error('面板没有打开');
    input.value = 'markedit';
    input.dispatchEvent(new Event('input'));
    await flushPromises();
    await debounce();
    const options = [...document.body.querySelectorAll('[role="option"]')].map(option => option.textContent ?? '');
    const index = options.findIndex(text => text.includes('MarkEdit'));
    expect(index).toBeGreaterThanOrEqual(0);
    for (let step = 0; step < index; step++) {
      input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
      await flushPromises();
    }
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    await flushPromises();
    expect(context.router.currentRoute.value.path).toBe('/appstore/1666327168');
    expect(open.value).toBe(false);
  });
});

describe('Also in the App Store', () => {
  async function mountPanel(bundleIds: string[]) {
    const { pinia, router } = await setup('/package/cask/netnewswire');
    await startLocalState();
    const ui = createOnUi({ locale: ref('zh-CN' as const) });
    const wrapper = mount(MasAlsoPanel, { props: { bundleIds }, global: { plugins: [pinia, router, i18n, ui] } });
    wrappers.push(wrapper);
    await flushPromises();
    await flushPromises();
    return wrapper;
  }

  it('Shows the App Store copy found by bundle ID, linking to its detail page', async () => {
    const lookup = vi.spyOn(commands, 'masLookupBundles');
    const wrapper = await mountPanel(['com.ranchero.NetNewsWire-Evergreen']);
    expect(lookup).toHaveBeenCalledWith(['com.ranchero.NetNewsWire-Evergreen']);
    expect(wrapper.text()).toContain('也可以这样获取');
    expect(wrapper.find('a').attributes('href')).toBe('/appstore/1176895641');
  });

  it('Stays hidden when Apple has no match, and asks nothing with App Store results turned off', async () => {
    expect((await mountPanel(['com.example.unknown'])).find('section').exists()).toBe(false);
    wrappers.splice(0).forEach(wrapper => wrapper.unmount());
    const lookup = vi.spyOn(commands, 'masLookupBundles');
    const { pinia, router } = await setup('/package/cask/netnewswire');
    await startLocalState();
    await useSettingsStore(pinia).update('appStoreSearch', false);
    const wrapper = mount(MasAlsoPanel, {
      props: { bundleIds: ['com.ranchero.NetNewsWire-Evergreen'] },
      global: { plugins: [pinia, router, i18n, createOnUi({ locale: ref('zh-CN' as const) })] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    expect(lookup).not.toHaveBeenCalled();
    expect(wrapper.find('section').exists()).toBe(false);
  });
});
