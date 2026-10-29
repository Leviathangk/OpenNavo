import { flushPromises, mount } from '@vue/test-utils';
import { NAlert, NButton, NEmpty, NRadioButton, NRadioGroup, NSkeleton, NTable, NText } from 'naive-ui';
import { beforeEach, expect, it, vi } from 'vitest';
import { createI18n } from 'vue-i18n';
import enPages from '../src/locales/langs/pages/en-us';
import zhPages from '../src/locales/langs/pages/zh-cn';
import type { Schemas } from '../src/typings/api/opennavo';
import UsageIntraday from '../src/views/dashboard/modules/usage-intraday.vue';

const { fetchIntraday, appState } = vi.hoisted(() => ({ fetchIntraday: vi.fn(), appState: { locale: 'en-US' } }));
vi.mock('@/service/api/dashboard', () => ({ fetchDashboardUsageIntraday: fetchIntraday }));
vi.mock('@/locales', () => ({
  $t: (key: string, values: Record<string, unknown> = {}) => {
    if (key === 'page.dashboard.intraday.asOf') {
      const i18n = createI18n({
        legacy: false,
        locale: appState.locale,
        messages: { 'en-US': { page: enPages }, 'zh-CN': { page: zhPages } }
      });
      return i18n.global.t(key, values);
    }
    return Object.keys(values).length ? `${key}(${Object.values(values).join(',')})` : key;
  }
}));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => appState }));
vi.mock('@/store/modules/theme', () => ({ useThemeStore: () => ({ darkMode: false }) }));
const components = { NAlert, NButton, NEmpty, NRadioButton, NRadioGroup, NSkeleton, NTable, NText };

function report(date = '2026-10-29', lastHour = 2): Schemas['UsageIntraday'] {
  let desktop = 0;
  let web = 0;
  return {
    date,
    timeZone: 'Asia/Shanghai',
    asOf: `${date}T${String(lastHour).padStart(2, '0')}:30:00+08:00`,
    hours: Array.from({ length: 24 }, (_, hour) => {
      const active = hour <= lastHour;
      const d = hour === 0 ? 3 : 0;
      const w = hour === 2 ? 5 : 0;
      desktop += active ? d : 0;
      web += active ? w : 0;
      return {
        hour,
        desktop: active ? d : null,
        web: active ? w : null,
        cumulativeDesktop: active ? desktop : null,
        cumulativeWeb: active ? web : null
      };
    })
  };
}

beforeEach(() => {
  appState.locale = 'en-US';
  fetchIntraday.mockReset();
  fetchIntraday.mockResolvedValue({ data: report(), error: null });
});

const mountTrend = () => mount(UsageIntraday, { props: { refreshAt: new Date(0) }, global: { components } });

it('shows first activity by platform, leaves future hours blank, and exposes hourly and cumulative data', async () => {
  const wrapper = mountTrend();
  expect(wrapper.findComponent(NSkeleton).exists()).toBe(true);
  await flushPromises();
  expect(wrapper.text()).toContain('page.dashboard.intraday.title');
  expect(wrapper.text()).toContain('page.dashboard.intraday.description');
  expect(wrapper.get('[data-testid="intraday-as-of"]').text()).toBe('As of 2026-10-29 02:30 (Beijing time)');
  expect(wrapper.findAll('rect[data-series="desktop"]')).toHaveLength(3);
  expect(wrapper.find('rect[data-hour="3"]').exists()).toBe(false);
  expect(wrapper.findAll('tbody tr')).toHaveLength(24);
  const cells = (hour: number) =>
    wrapper
      .get(`[data-table-hour="${hour}"]`)
      .findAll('td')
      .map(cell => cell.text());
  expect(cells(0)).toEqual(['3', '0', '3', '0']);
  expect(cells(1)).toEqual(['0', '0', '3', '0']);
  expect(cells(2)).toEqual(['0', '5', '3', '5']);
  expect(cells(3)).toEqual(['—', '—', '—', '—']);
  await wrapper.findAllComponents(NRadioButton)[1]!.get('input').setValue(true);
  expect(wrapper.findAll('rect[data-series]')).toHaveLength(0);
  expect(wrapper.findAll('polyline')).toHaveLength(2);
  expect(wrapper.get('polyline[data-series="web"]').attributes('points')!.split(' ')).toHaveLength(3);
  wrapper.unmount();
});

it.each([
  ['zh-CN', '数据截至 2026-10-29 02:07 (北京时间)'],
  ['en-US', 'As of 2026-10-29 02:07 (Beijing time)']
])('shows the sample timestamp in Beijing time for %s', async (locale, expected) => {
  appState.locale = locale;
  const data = { ...report(), asOf: '2026-10-28T18:07:00Z' };
  fetchIntraday.mockResolvedValueOnce({ data, error: null });
  const wrapper = mountTrend();
  await flushPromises();
  const timestamp = wrapper.get('time[data-testid="intraday-as-of"]');
  expect(timestamp.text()).toBe(expected);
  expect(timestamp.attributes('datetime')).toBe(data.asOf);
  wrapper.unmount();
});

it('plots exactly 24 hourly samples per cumulative series using straight, unstepped segments', async () => {
  fetchIntraday.mockResolvedValueOnce({ data: report('2026-10-29', 23), error: null });
  const wrapper = mountTrend();
  await flushPromises();
  await wrapper.findAllComponents(NRadioButton)[1]!.get('input').setValue(true);
  const lines = wrapper.findAll('polyline[data-series]');
  expect(lines).toHaveLength(2);
  expect(wrapper.find('path[d]').exists()).toBe(false);
  for (const line of lines) {
    const points = line
      .attributes('points')!
      .split(' ')
      .map(point => point.split(',').map(Number));
    expect(points).toHaveLength(24);
    expect(points.flat().every(Number.isFinite)).toBe(true);
    for (let hour = 1; hour < points.length; hour += 1) {
      expect(points[hour]![0]).toBeGreaterThan(points[hour - 1]![0]!);
      expect(points[hour]![1]).toBeLessThanOrEqual(points[hour - 1]![1]!);
    }
    // Zero-activity hours retain their real plateau instead of adding interpolated samples.
    expect(points[0]![1]).toBe(points[1]![1]);
    expect(new Set(points.slice(2).map(point => point[1])).size).toBe(1);
    if (line.attributes('data-series') === 'web') {
      expect(points[2]![1]).toBeLessThan(points[1]![1]!);
    } else {
      expect(new Set(points.map(point => point[1])).size).toBe(1);
    }
  }
  for (let hour = 0; hour < 24; hour += 1) {
    await wrapper.get(`[data-hover-hour="${hour}"]`).trigger('pointerenter');
    expect(wrapper.get('[role="status"]').text()).toContain(`${String(hour).padStart(2, '0')}:00`);
    expect(
      wrapper
        .get('[role="status"]')
        .findAll('strong')
        .map(value => value.text())
    ).toEqual(['3', hour < 2 ? '0' : '5']);
  }
  wrapper.unmount();
});

it('supports pointer and keyboard inspection, preserves the hour on refresh, and resets across midnight', async () => {
  const wrapper = mountTrend();
  await flushPromises();
  await wrapper.get('[data-hover-hour="0"]').trigger('pointerenter');
  expect(wrapper.get('[role="status"]').text()).toContain('00:00');
  const frame = wrapper.get('[tabindex="0"]');
  await frame.trigger('keydown', { key: 'ArrowRight' });
  expect(wrapper.get('[role="status"]').text()).toContain('01:00');
  fetchIntraday.mockResolvedValueOnce({ data: report('2026-10-29', 3), error: null });
  await wrapper.setProps({ refreshAt: new Date(60_000) });
  await flushPromises();
  expect(fetchIntraday).toHaveBeenCalledTimes(2);
  expect(wrapper.get('[role="status"]').text()).toContain('01:00');
  await frame.trigger('keydown', { key: 'End' });
  expect(wrapper.get('[role="status"]').text()).toContain('03:00');
  await frame.trigger('keydown', { key: 'ArrowRight' });
  expect(wrapper.get('[role="status"]').text()).toContain('03:00');
  await frame.trigger('keydown', { key: 'Home' });
  expect(wrapper.get('[role="status"]').text()).toContain('00:00');
  await wrapper.get('svg').trigger('pointerleave');
  expect(wrapper.find('[role="status"]').exists()).toBe(false);
  await frame.trigger('focus');
  expect(wrapper.get('[role="status"]').text()).toContain('03:00');
  fetchIntraday.mockResolvedValueOnce({ data: report('2026-10-30', 0), error: null });
  await wrapper.setProps({ refreshAt: new Date(120_000) });
  await flushPromises();
  expect(wrapper.find('[role="status"]').exists()).toBe(false);
  expect(wrapper.findAll('rect[data-series="web"]')).toHaveLength(1);
  wrapper.unmount();
});

it('shows a load error and retries without inventing data', async () => {
  fetchIntraday.mockRejectedValueOnce(new Error('test request failed'));
  const wrapper = mountTrend();
  await flushPromises();
  expect(wrapper.get('[role="alert"]').text()).toContain('page.dashboard.intraday.loadFailed');
  expect(wrapper.find('svg[role="img"]').exists()).toBe(false);
  await wrapper.get('button').trigger('click');
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  expect(wrapper.find('svg[role="img"]').exists()).toBe(true);
  wrapper.unmount();
});

it('retains the last successful sample with its timestamp when a refresh fails', async () => {
  const wrapper = mountTrend();
  await flushPromises();
  fetchIntraday.mockResolvedValueOnce({ data: null, error: new Error('test refresh failed') });
  await wrapper.setProps({ refreshAt: new Date(60_000) });
  await flushPromises();
  expect(wrapper.find('[role="alert"]').exists()).toBe(true);
  expect(wrapper.get('[data-testid="intraday-as-of"]').text()).toBe('As of 2026-10-29 02:30 (Beijing time)');
  expect(wrapper.findAll('rect[data-series="desktop"]')).toHaveLength(3);
  wrapper.unmount();
});

it('shows the empty state and distinguishes elapsed zero counts from future nulls', async () => {
  const empty = report();
  for (const entry of empty.hours) {
    if (entry.web === null) continue;
    entry.web = entry.desktop = entry.cumulativeWeb = entry.cumulativeDesktop = 0;
  }
  fetchIntraday.mockResolvedValueOnce({ data: empty, error: null });
  const wrapper = mountTrend();
  await flushPromises();
  expect(wrapper.text()).toContain('page.dashboard.usage.empty');
  expect(wrapper.find('svg[role="img"]').exists()).toBe(false);
  expect(wrapper.get('[data-table-hour="0"]').text()).toContain('0');
  expect(wrapper.get('[data-table-hour="3"]').text()).toContain('—');
  wrapper.unmount();
});
