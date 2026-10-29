import { mount } from '@vue/test-utils';
import { NButton, NCard, NDrawer, NDrawerContent, NEmpty, NTable, NText } from 'naive-ui';
import { nextTick } from 'vue';
import { expect, it, vi } from 'vitest';
import type { Schemas } from '../src/typings/api/opennavo';
import UsageCard from '../src/views/dashboard/modules/usage-card.vue';

vi.mock('@/locales', () => ({
  $t: (key: string, values: Record<string, unknown> = {}) =>
    Object.keys(values).length ? `${key}(${Object.values(values).join(',')})` : key
}));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => ({ locale: 'en-US' }) }));
vi.mock('@/store/modules/theme', () => ({ useThemeStore: () => ({ darkMode: true }) }));
const components = { NButton, NCard, NDrawer, NDrawerContent, NEmpty, NTable, NText };
const day = (date: string, desktop: number, web: number, newDesktop = 0, newWeb = 0) => ({
  date,
  desktop,
  web,
  newDesktop,
  newWeb
});
const mountCard = (usage: Schemas['UsageOverview']) =>
  mount(UsageCard, { props: { usage }, global: { components, stubs: { UsageIntraday: true } } });

it('keeps platforms separate, shows today and provides chart data as an accessible table', () => {
  const wrapper = mountCard({
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-22T08:00:00Z',
    days: [day('2026-10-21', 12, 40, 3, 9), day('2026-10-22', 15, 50, 2, 6)],
    countries: [
      { country: 'JP', desktop: 15, web: 40 },
      { country: 'ZZ', desktop: 0, web: 10 }
    ],
    versions: [{ version: '0.1.3', count: 15 }]
  });
  const metric = (key: string) => wrapper.get(`[data-metric="${key}"]`).text();
  expect(metric('desktop')).toContain('page.dashboard.usage.desktop');
  expect(metric('desktop')).toContain('15');
  expect(metric('desktop')).toContain('page.dashboard.usage.yesterday(12)');
  expect(metric('web')).toContain('50');
  expect(metric('newWeb')).toContain('page.dashboard.usage.yesterday(9)');
  expect(wrapper.text()).toContain('page.dashboard.usage.asOf(16:00)');
  expect(wrapper.findAll('polyline')).toHaveLength(2);
  expect(wrapper.get('summary').text()).toBe('page.dashboard.usage.details');
  expect(wrapper.find('[data-testid="usage-all-countries"]').exists()).toBe(false);
  expect(wrapper.findAll('th[scope="row"]').map(row => row.text())).toEqual([
    '0.1.3',
    'Japan',
    'page.dashboard.usage.unknownCountry',
    '2026-10-22',
    '2026-10-21'
  ]);
  wrapper.unmount();
});

it('shows the top five countries and opens the complete sorted list without changing source data', async () => {
  const countries = [
    { country: 'DE', desktop: 2, web: 2 },
    { country: 'JP', desktop: 5, web: 1 },
    { country: 'CN', desktop: 13, web: 19 },
    { country: 'SG', desktop: 1, web: 3 },
    { country: 'US', desktop: 6, web: 47 },
    { country: 'ZZ', desktop: 27, web: 10 },
    { country: 'HK', desktop: 9, web: 4 }
  ];
  const original = structuredClone(countries);
  const usage: Schemas['UsageOverview'] = {
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-24T08:00:00Z',
    days: [day('2026-10-24', 63, 86)],
    countries,
    versions: []
  };
  const wrapper = mountCard(usage);
  const preview = () => wrapper.get('[data-testid="usage-countries"]');
  const names = () =>
    preview()
      .findAll('tbody th')
      .map(row => row.text());
  expect(names()).toEqual([
    'United States',
    'page.dashboard.usage.unknownCountry',
    'China',
    'Hong Kong SAR China',
    'Japan'
  ]);
  expect(
    preview()
      .findAll('tbody tr')[0]
      .findAll('td')
      .map(cell => cell.text())
  ).toEqual(['6', '47']);
  expect(countries).toEqual(original);
  expect(preview().text()).toContain('page.dashboard.usage.countryOrder');

  await wrapper.get('[data-testid="usage-all-countries"]').trigger('click');
  await vi.waitFor(() => expect(document.querySelector('.n-drawer-content tbody')).not.toBeNull());
  const drawerNames = () => Array.from(document.querySelectorAll('.n-drawer-content tbody th'), row => row.textContent);
  expect(drawerNames()).toEqual([...names(), 'Germany', 'Singapore']);
  expect(wrapper.get('[data-testid="usage-all-countries"]').attributes('aria-expanded')).toBe('true');

  // Refreshes update both views while the drawer is open.
  await wrapper.setProps({ usage: { ...usage, countries: [...countries, { country: 'FR', desktop: 80, web: 20 }] } });
  expect(names()).toHaveLength(5);
  expect(names()[0]).toBe('France');
  expect(drawerNames()).toHaveLength(8);
  expect(drawerNames()[0]).toBe('France');

  (document.querySelector('.n-drawer-content .n-base-close') as HTMLElement).click();
  await nextTick();
  expect(wrapper.findComponent(NDrawer).props('show')).toBe(false);
  expect(wrapper.get('[data-testid="usage-all-countries"]').attributes('aria-expanded')).toBe('false');
  expect(names()).toHaveLength(5);
  wrapper.unmount();
});

it('does not show a country panel when there are no countries', () => {
  const wrapper = mountCard({
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-24T08:00:00Z',
    days: [day('2026-10-24', 1, 1)],
    countries: [],
    versions: []
  });
  expect(wrapper.find('[data-testid="usage-countries"]').exists()).toBe(false);
  expect(wrapper.find('[data-testid="usage-all-countries"]').exists()).toBe(false);
  wrapper.unmount();
});

it('starts the trend at the first reported day instead of drawing the collection gap as zero', async () => {
  const wrapper = mountCard({
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-23T08:00:00Z',
    days: [
      day('2026-10-20', 0, 0),
      day('2026-10-21', 0, 0),
      day('2026-10-22', 5, 91, 5, 91),
      day('2026-10-23', 64, 125)
    ],
    versions: []
  });
  expect(wrapper.text()).toContain('page.dashboard.usage.trend(Oct 22,Oct 23)');
  expect(wrapper.get('polyline[data-series="desktop"]').attributes('points')!.split(' ')).toHaveLength(2);
  expect(wrapper.findAll('th[scope="row"]').map(row => row.text())).toEqual(['2026-10-23', '2026-10-22']);
  // Hover and keyboard focus read out every series for the day.
  await wrapper.findAll('svg rect').at(-1)!.trigger('pointerenter');
  const tooltip = wrapper.get('[role="status"]').text();
  for (const value of ['Oct 23', '64', '125', 'page.dashboard.usage.newDesktop']) expect(tooltip).toContain(value);
  await wrapper.get('svg').trigger('pointerleave');
  expect(wrapper.find('[role="status"]').exists()).toBe(false);
  const frame = wrapper.get('[tabindex="0"]');
  await frame.trigger('focus');
  await frame.trigger('keydown', { key: 'ArrowLeft' });
  expect(wrapper.get('[role="status"]').text()).toContain('Oct 22');
  wrapper.unmount();
});

it('keeps the selected day across refreshes and drops it once the day is gone', async () => {
  const usage = (days: ReturnType<typeof day>[]): Schemas['UsageOverview'] => ({
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-23T08:00:00Z',
    days,
    versions: []
  });
  const wrapper = mountCard(usage([day('2026-10-21', 3, 4), day('2026-10-22', 5, 91), day('2026-10-23', 64, 125)]));
  const tooltip = () => wrapper.find('[role="status"]');
  await wrapper.findAll('svg rect').at(-1)!.trigger('pointerenter');
  expect(tooltip().text()).toContain('Oct 23');
  // The minute refresh can shift the window: the same day stays selected at its new position.
  await wrapper.setProps({
    usage: usage([day('2026-10-22', 5, 91), day('2026-10-23', 64, 125), day('2026-10-24', 7, 9)])
  });
  expect(tooltip().text()).toContain('Oct 23');
  expect(wrapper.findAll('circle[r="5"]')).toHaveLength(2);
  // A shorter list without that day closes the tooltip instead of reading past its end.
  await wrapper.setProps({ usage: usage([day('2026-10-24', 7, 9)]) });
  expect(tooltip().exists()).toBe(false);
  expect(wrapper.findAll('circle')).toHaveLength(2);
  wrapper.unmount();
});

it('marks yesterday as unknown on the first reported day', () => {
  const wrapper = mountCard({
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-22T08:00:00Z',
    days: [day('2026-10-21', 0, 0), day('2026-10-22', 5, 91, 5, 91)],
    versions: []
  });
  expect(wrapper.get('[data-metric="desktop"]').text()).toContain('page.dashboard.usage.yesterday(—)');
  wrapper.unmount();
});

it('shows an honest empty state rather than an invented trend', () => {
  const wrapper = mountCard({
    timeZone: 'Asia/Shanghai',
    asOf: '2026-10-22T08:00:00Z',
    days: [day('2026-10-22', 0, 0)],
    versions: []
  });
  expect(wrapper.text()).toContain('page.dashboard.usage.empty');
  expect(wrapper.find('svg[role="img"]').exists()).toBe(false);
  expect(wrapper.find('summary').exists()).toBe(false);
  wrapper.unmount();
});
