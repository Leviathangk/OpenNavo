import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import OnPagination from '../src/components/OnPagination.vue';
import { paginationRange } from '../src/utils/pagination';
import { RouterLinkStub, withLocale } from './helpers';

const view = (current: number, total: number, jumps?: number[]) =>
  paginationRange(current, total, 7, jumps).map(item => (item.type === 'page' ? item.page : '…'));

describe('Pagination jump slots (T1)', () => {
  it('Adds the pages ten and a hundred steps away, between ellipses', () => {
    expect(view(1, 211, [10, 100])).toEqual([1, 2, 3, 4, 5, '…', 11, '…', 101, '…', 211]);
    expect(view(50, 211, [10, 100])).toEqual([1, '…', 40, '…', 49, 50, 51, '…', 60, '…', 150, '…', 211]);
    expect(view(205, 211, [10, 100])).toEqual([1, '…', 105, '…', 195, '…', 204, 205, 206, '…', 211]);
    expect(view(210, 211, [10, 100])).toEqual([1, '…', 110, '…', 200, '…', 207, 208, 209, 210, 211]);
  });

  it('Shows a single skipped page instead of an ellipsis and skips jumps already shown or out of range', () => {
    // The window ends at 5 and the jump lands on 7: page 6 is written out.
    expect(view(1, 30, [6])).toEqual([1, 2, 3, 4, 5, 6, 7, '…', 30]);
    expect(view(3, 9, [10, 100])).toEqual([1, 2, 3, 4, 5, '…', 9]);
    expect(view(1, 5, [10])).toEqual([1, 2, 3, 4, 5]);
  });

  it('Keeps the plain range without jumps, and every key is unique', () => {
    expect(view(5, 20)).toEqual([1, '…', 4, 5, 6, '…', 20]);
    const keys = paginationRange(50, 211, 7, [10, 100]).map(item =>
      item.type === 'page' ? `p${item.page}` : item.key
    );
    expect(new Set(keys).size).toBe(keys.length);
  });
});

describe('OnPagination jump links', () => {
  it('Links jump pages by default when pages have URLs', () => {
    const wrapper = mount(OnPagination, {
      props: { page: 1, pageCount: 211, hrefFor: (page: number) => `/apps?page=${page}`, linkAs: RouterLinkStub },
      global: withLocale('en-US')
    });
    expect(wrapper.get('[aria-label="Page 11"]').attributes('href')).toBe('#/apps?page=11');
    expect(wrapper.get('[aria-label="Page 101"]').attributes('href')).toBe('#/apps?page=101');
    expect(wrapper.get('[aria-label="Page 211"]').attributes('href')).toBe('#/apps?page=211');
  });

  it('Keeps button pagination (desktop) without jumps unless asked', async () => {
    const plain = mount(OnPagination, { props: { page: 1, pageCount: 211 }, global: withLocale('en-US') });
    expect(plain.find('[aria-label="Page 11"]').exists()).toBe(false);
    const jumping = mount(OnPagination, {
      props: { page: 1, pageCount: 211, jumps: [10] },
      global: withLocale('en-US')
    });
    await jumping.get('[aria-label="Page 11"]').trigger('click');
    expect(jumping.emitted('update:page')?.[0]).toEqual([11]);
    const none = mount(OnPagination, {
      props: { page: 1, pageCount: 211, hrefFor: (page: number) => `/apps?page=${page}`, jumps: [] },
      global: withLocale('en-US')
    });
    expect(none.find('[aria-label="Page 11"]').exists()).toBe(false);
  });
});
