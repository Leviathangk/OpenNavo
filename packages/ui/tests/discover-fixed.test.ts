import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import { h, nextTick } from 'vue';
import OnDiscoverView from '../src/components/OnDiscoverView.vue';
import { packageSummary } from './fixtures';
import { withLocale } from './helpers';

const labels = {
  categories: 'Browse by category',
  popularApps: 'Popular Apps',
  popularAppsHint: 'Homebrew installs',
  viewRankings: 'See Rankings',
  recentlyUpdated: 'Recently Updated',
  recentlyUpdatedHint: 'Fresh releases',
  collections: 'Collections',
  viewAll: 'See All'
};

describe('OnDiscoverView fixed sets (T1, web)', () => {
  it('Renders the same twelve cards per section at every width and leaves the rows to CSS', async () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get');
    try {
      for (const pixels of [0, 220, 916, 1892]) {
        width.mockReturnValue(pixels);
        const apps = Array.from({ length: 30 }, (_, index) => packageSummary({ token: `app-${index}` }));
        const wrapper = mount(OnDiscoverView, {
          props: {
            chips: [],
            popular: apps,
            recent: apps,
            popularRows: 3,
            fixedCount: 12,
            labels,
            rankingsHref: '/rankings',
            recentHref: '/recent',
            collectionsHref: '/collections'
          },
          slots: { card: ({ section }: { section: string }) => h('article', { 'data-section': section }) },
          global: withLocale('en-US')
        });
        await nextTick();
        expect(wrapper.findAll('[data-section="popular"]')).toHaveLength(12);
        expect(wrapper.findAll('[data-section="recent"]')).toHaveLength(12);
        // Cards are direct children of the grid, which the container queries target.
        expect(wrapper.findAll('.on-discover-fixed')).toHaveLength(2);
        expect(wrapper.get('.on-discover-fixed').element.children).toHaveLength(12);
        wrapper.unmount();
      }
    } finally {
      width.mockRestore();
    }
  });

  it('Keeps width fitting when the host does not fix the set (desktop)', async () => {
    const width = vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(684);
    try {
      const apps = Array.from({ length: 30 }, (_, index) => packageSummary({ token: `app-${index}` }));
      const wrapper = mount(OnDiscoverView, {
        props: {
          chips: [],
          popular: apps,
          recent: apps,
          popularRows: 3,
          labels,
          rankingsHref: '/rankings',
          recentHref: '/recent',
          collectionsHref: '/collections'
        },
        slots: { card: ({ section }: { section: string }) => h('div', { 'data-section': section }) },
        global: withLocale('en-US')
      });
      await nextTick();
      expect(wrapper.findAll('[data-section="popular"]')).toHaveLength(7);
      expect(wrapper.find('.on-discover-fixed').exists()).toBe(false);
      wrapper.unmount();
    } finally {
      width.mockRestore();
    }
  });
});
