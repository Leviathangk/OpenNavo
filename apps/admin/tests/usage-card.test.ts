import { mount } from '@vue/test-utils';
import { NCard, NEmpty, NTable, NText } from 'naive-ui';
import { expect, it, vi } from 'vitest';
import type { Schemas } from '../src/typings/api/opennavo';
import UsageCard from '../src/views/dashboard/modules/usage-card.vue';

vi.mock('@/locales', () => ({ $t: (key: string) => key }));
const components = { NCard, NEmpty, NTable, NText };
it('keeps platforms separate, shows today and provides chart data as an accessible table', () => {
  const usage: Schemas['UsageOverview'] = {
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-22T08:00:00Z',
    days: [
      { date: '2026-10-21', desktop: 12, web: 40, newDesktop: 3, newWeb: 9 },
      { date: '2026-10-22', desktop: 15, web: 50, newDesktop: 2, newWeb: 6 }
    ],
    countries: [
      { country: 'JP', desktop: 15, web: 40 },
      { country: 'ZZ', desktop: 0, web: 10 }
    ],
    versions: [{ version: '0.1.3', count: 15 }]
  };
  const wrapper = mount(UsageCard, { props: { usage }, global: { components } });
  expect(wrapper.text()).toContain('page.dashboard.usage.desktop: 15');
  expect(wrapper.text()).toContain('page.dashboard.usage.web: 50');
  expect(wrapper.findAll('polyline')).toHaveLength(2);
  expect(wrapper.get('summary').text()).toBe('page.dashboard.usage.details');
  expect(wrapper.findAll('th[scope="row"]').map(row => row.text())).toEqual([
    '2026-10-22',
    '2026-10-21',
    '0.1.3',
    'JP',
    'page.dashboard.usage.unknownCountry'
  ]);
  wrapper.unmount();
});
it('shows an honest empty state rather than an invented trend', () => {
  const wrapper = mount(UsageCard, {
    props: {
      usage: {
        timeZone: 'Asia/Shanghai',
        asOf: '2026-10-22T08:00:00Z',
        days: [{ date: '2026-10-22', desktop: 0, web: 0, newDesktop: 0, newWeb: 0 }],
        versions: []
      }
    },
    global: { components }
  });
  expect(wrapper.text()).toContain('page.dashboard.usage.empty');
  expect(wrapper.find('svg[role="img"]').exists()).toBe(false);
  wrapper.unmount();
});
