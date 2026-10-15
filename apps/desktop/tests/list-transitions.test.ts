import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { OnPackageListView, OnPagination } from '@opennavo/ui';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import type { ListQuery } from '@/ipc/bindings';
import { startLocalState } from '@/stores';
import CategoryPage from '@/pages/CategoryPage.vue';
import RankingsPage from '@/pages/RankingsPage.vue';
import { setup } from './helpers';

const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
});

/** Holds catalog list reads that match until release(), as a slow local read would. */
function holdList(matches: (query: ListQuery) => boolean) {
  const list = commands.catalogList;
  let release: () => void = () => {};
  const held = new Promise<void>(resolve => (release = resolve));
  vi.spyOn(commands, 'catalogList').mockImplementation(async query => {
    if (matches(query)) await held;
    return list(query);
  });
  return () => release();
}

const button = (wrapper: VueWrapper, label: string) => wrapper.findAll('button').find(item => item.text() === label)!;

describe('Lists keep their content while the next read lands', () => {
  it('Rankings keep the current list and its counts while another period loads', async () => {
    const { pinia, router } = await setup('/rankings');
    await startLocalState();
    const release = holdList(query => query.sort === 'installs90d');
    const wrapper = mount(RankingsPage, { global: { plugins: [pinia, router, i18n] } });
    wrappers.push(wrapper);
    await flushPromises();
    const rows = () => wrapper.findAll('ol > li').map(item => item.text());
    const before = rows();
    expect(before.length).toBeGreaterThan(1);

    await button(wrapper, '90 天').trigger('click');
    await flushPromises();
    expect(rows()).toEqual(before);

    release();
    await flushPromises();
    expect(rows()).toHaveLength(before.length);
  });

  it('A category keeps its cards and count line while another sort loads', async () => {
    const { pinia, router } = await setup('/categories/communication');
    await startLocalState();
    const release = holdList(query => query.sort === 'updated');
    const wrapper = mount(CategoryPage, {
      props: { slug: 'communication' },
      global: { plugins: [pinia, router, i18n] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    const cards = () => wrapper.findAll('article').length;
    const subtitle = () => wrapper.find('header p').text();
    expect(cards()).toBe(5);
    expect(subtitle()).toBe('5 个');

    await button(wrapper, '最近更新').trigger('click');
    await flushPromises();
    expect(cards()).toBe(5);
    expect(subtitle()).toBe('5 个');

    release();
    await flushPromises();
    expect(cards()).toBe(5);
  });
});

it.each(['page', 'sort', 'size'] as const)(
  'A failed category %s change hides cards from the previous query',
  async change => {
    const { pinia, router } = await setup('/categories/communication');
    await startLocalState();
    const list = commands.catalogList;
    let fail = false;
    vi.spyOn(commands, 'catalogList').mockImplementation(async query => {
      if (fail) throw new Error('read failed');
      const result = await list(query);
      return result.status === 'ok' ? { ...result, data: { ...result.data, total: 100 } } : result;
    });
    const wrapper = mount(CategoryPage, {
      props: { slug: 'communication' },
      global: { plugins: [pinia, router, i18n] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    expect(wrapper.findAll('article')).toHaveLength(5);
    fail = true;
    if (change === 'page') wrapper.findComponent(OnPagination).vm.$emit('update:page', 2);
    if (change === 'sort') wrapper.findComponent(OnPackageListView).vm.$emit('update:sort', 'updated');
    if (change === 'size') wrapper.findComponent(OnPackageListView).vm.$emit('page-size', 12);
    await flushPromises();
    expect(wrapper.findAll('article')).toHaveLength(0);
    expect(wrapper.findComponent(OnPagination).exists()).toBe(true);
    fail = false;
    if (change === 'page') wrapper.findComponent(OnPagination).vm.$emit('update:page', 1);
    if (change === 'sort') wrapper.findComponent(OnPackageListView).vm.$emit('update:sort', 'popular');
    if (change === 'size') wrapper.findComponent(OnPackageListView).vm.$emit('page-size', 24);
    await flushPromises();
    expect(wrapper.findAll('article').length).toBeGreaterThan(0);
  }
);
