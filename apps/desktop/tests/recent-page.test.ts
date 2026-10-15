import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { PackageSummary } from '@opennavo/api';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import type { ListQuery } from '@/ipc/bindings';
import { refreshPage } from '@/composables/usePageRefresh';
import { startLocalState } from '@/stores';
import RecentPage from '@/pages/RecentPage.vue';
import PackageLayout from '@/pages/package/PackageLayout.vue';
import { setup } from './helpers';

const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const recentQuery: ListQuery = {
  kind: 'cask',
  category: null,
  sort: 'updated',
  includeFonts: true,
  includeLibraries: false,
  includeDisabled: false,
  limit: 24,
  offset: 0
};

function app(token: string, version: string): PackageSummary {
  return {
    kind: 'cask',
    token,
    name: token,
    displayName: token.toUpperCase(),
    summary: null,
    iconUrl: null,
    accentColor: null,
    version,
    primaryCategory: null,
    installs30d: 1742,
    rank30d: null,
    autoUpdates: false,
    deprecated: false,
    disabled: false,
    isFont: false,
    isLibrary: false,
    editorChoice: false,
    versionChangedAt: '2026-10-14T00:00:00Z'
  };
}

const cardLinks = (wrapper: VueWrapper) =>
  wrapper
    .findAll('article a.on-stretched')
    .map(link => new URL(link.attributes('href')!, 'https://example.invalid').pathname);
const requestUrl = (input: RequestInfo | URL) => new URL(input instanceof Request ? input.url : String(input));
const OFFLINE = '无法连接 OpenNavo 服务';

async function mountPage() {
  const { pinia, router } = await setup('/recent');
  await startLocalState();
  const wrapper = mount(RecentPage, { global: { plugins: [pinia, router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  return wrapper;
}

describe('Recently Updated page', () => {
  // Runs first: failed requests are never cached, while a successful API page stays cached for five minutes.
  it('Shows a retryable error instead of the offline notice when the local catalog fails too', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    vi.spyOn(commands, 'catalogList').mockResolvedValue({
      status: 'error',
      error: { code: 'E_IO', message: 'catalog_unreadable', detail: null }
    });
    const wrapper = await mountPage();
    expect(wrapper.text()).not.toContain(OFFLINE);
    expect(wrapper.findAll('article')).toHaveLength(0);
    expect(wrapper.findComponent({ name: 'OnErrorState' }).props('retryable')).toBe(true);
  });

  it('Falls back to the local catalog offline, returns to the API list once reachable, and pages through it', async () => {
    let reachable = false;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (!reachable) throw new TypeError('Failed to fetch');
      const url = requestUrl(input);
      if (!url.pathname.endsWith('/packages')) return new Response(null, { status: 404 });
      const current = Number(url.searchParams.get('current'));
      const records = current === 1 ? [app('newest', '2.0'), app('older', '1.9')] : [app('next-page', '3.1')];
      return new Response(JSON.stringify({ code: '0000', msg: '', data: { current, size: 24, total: 30, records } }), {
        headers: { 'Content-Type': 'application/json' }
      });
    });
    vi.stubGlobal('fetch', fetcher);
    const list = vi.spyOn(commands, 'catalogList');
    const wrapper = await mountPage();

    // Offline: the local catalog, newest version first, under the offline notice.
    expect(list).toHaveBeenCalledWith(recentQuery);
    const local = await unwrap(commands.catalogList(recentQuery));
    expect(local.items.length).toBeGreaterThan(1);
    expect(cardLinks(wrapper)).toEqual(local.items.map(item => `/package/cask/${item.token}`));
    expect(wrapper.find('h1').text()).toBe('最近更新');
    expect(wrapper.text()).toContain(OFFLINE);

    // The API error stays visible to recovery, so the shell's next attempt brings back the online list.
    reachable = true;
    await refreshPage(false);
    await flushPromises();
    const first = requestUrl(fetcher.mock.calls.at(-1)![0]);
    expect(Object.fromEntries(first.searchParams)).toEqual({
      kind: 'cask',
      sort: 'updated',
      current: '1',
      size: '24',
      includeFonts: 'true'
    });
    expect(cardLinks(wrapper)).toEqual(['/package/cask/newest', '/package/cask/older']);
    expect(
      new URL(wrapper.get('article a.on-stretched').attributes('href')!, 'https://example.invalid').searchParams.get(
        'returnTo'
      )
    ).toBe('/recent?page=1&size=24');
    expect(wrapper.text()).not.toContain(OFFLINE);
    // Same card stats as Discover's section: version first, then 30-day installs.
    const stats = wrapper.get('article').text();
    expect(stats.indexOf('2.0')).toBeLessThan(stats.indexOf('1,742'));

    const localReads = list.mock.calls.length;
    await wrapper
      .findAll('nav button')
      .find(button => button.text() === '2')!
      .trigger('click');
    await flushPromises();
    expect(requestUrl(fetcher.mock.calls.at(-1)![0]).searchParams.get('current')).toBe('2');
    expect(cardLinks(wrapper)).toEqual(['/package/cask/next-page']);
    expect(list.mock.calls.length).toBe(localReads);
  });
});

it('Uses Recently Updated as the parent and keeps it when switching detail tabs', async () => {
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Offline')));
  const returnTo = '/recent?page=2&size=28';
  const { pinia, router } = await setup(`/package/cask/ghostty?returnTo=${encodeURIComponent(returnTo)}`);
  const wrapper = mount(PackageLayout, {
    props: { kind: 'cask', token: 'ghostty' },
    global: { plugins: [pinia, router, i18n] }
  });
  wrappers.push(wrapper);
  await flushPromises();
  const crumbs = wrapper.findComponent({ name: 'OnBreadcrumb' });
  expect(crumbs.props('items')[0]).toEqual({ label: '最近更新', href: returnTo });
  const versions = wrapper.findAll('a').find(link => link.attributes('href')?.includes('/versions?'))!;
  expect(versions.exists()).toBe(true);
  await versions.trigger('click');
  await flushPromises();
  expect(router.currentRoute.value.query.returnTo).toBe(returnTo);
  expect(crumbs.props('items')[0].href).toBe(returnTo);
});

it('Paging keeps the cards and pagination until the next page lands; a failed page falls back to the local catalog', async () => {
  let held: Promise<void> | undefined;
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = requestUrl(input);
    if (!url.pathname.endsWith('/packages')) return new Response(null, { status: 404 });
    const current = Number(url.searchParams.get('current'));
    if (current === 3) throw new TypeError('Failed to fetch');
    if (current === 2) await held;
    const records = [app(`page-${current}`, '1.0')];
    return new Response(JSON.stringify({ code: '0000', msg: '', data: { current, size: 12, total: 36, records } }), {
      headers: { 'Content-Type': 'application/json' }
    });
  });
  vi.stubGlobal('fetch', fetcher);
  const list = vi.spyOn(commands, 'catalogList');
  // A page size of its own keeps this case clear of pages other cases cached.
  const { pinia, router } = await setup('/recent?size=12');
  await startLocalState();
  const wrapper = mount(RecentPage, { global: { plugins: [pinia, router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  const page = (label: string) =>
    wrapper
      .findAll('nav button')
      .find(button => button.text() === label)!
      .trigger('click');
  expect(cardLinks(wrapper)).toEqual(['/package/cask/page-1']);

  let release: () => void = () => {};
  held = new Promise(resolve => (release = resolve));
  await page('2');
  await flushPromises();
  expect(cardLinks(wrapper)).toEqual(['/package/cask/page-1']);
  expect(wrapper.findAll('nav button').some(button => button.text() === '3')).toBe(true);
  release();
  await flushPromises();
  expect(cardLinks(wrapper)).toEqual(['/package/cask/page-2']);

  await page('3');
  await flushPromises();
  expect(list).toHaveBeenCalledWith(expect.objectContaining({ sort: 'updated', limit: 12, offset: 24 }));
  expect(cardLinks(wrapper)).not.toContain('/package/cask/page-2');
  expect(wrapper.text()).toContain(OFFLINE);
});
