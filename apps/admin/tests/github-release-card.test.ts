import { mount } from '@vue/test-utils';
import { NCard, NEmpty, NTable, NText } from 'naive-ui';
import { expect, it, vi } from 'vitest';
import type { Schemas } from '../src/typings/api/opennavo';
import GitHubReleaseCard from '../src/views/dashboard/modules/github-release-card.vue';

vi.mock('@/locales', () => ({
  $t: (key: string, values: Record<string, unknown> = {}) =>
    Object.keys(values).length ? `${key}(${Object.values(values).join(',')})` : key
}));
vi.mock('@/store/modules/app', () => ({ useAppStore: () => ({ locale: 'en-US' }) }));
vi.mock('@/store/modules/theme', () => ({ useThemeStore: () => ({ darkMode: true }) }));
const components = { NCard, NEmpty, NTable, NText };
const fixture = (): Schemas['GitHubReleaseOverview'] => ({
  downloads: { today: 0, yesterday: null, total: 953 },
  updates: { today: 2, yesterday: null, total: 582 },
  timeZone: 'Asia/Shanghai',
  lastSnapshotAt: '2026-10-29T09:10:00Z',
  dailyStartDate: '2026-10-26',
  days: [
    { date: '2026-10-25', downloads: null, updates: null },
    { date: '2026-10-26', downloads: 10, updates: 5 },
    { date: '2026-10-27', downloads: 12, updates: 0 },
    { date: '2026-10-28', downloads: null, updates: null },
    { date: '2026-10-29', downloads: 0, updates: 2 }
  ]
});
const mountCard = (stats = fixture()) => mount(GitHubReleaseCard, { props: { stats }, global: { components } });

it('shows cumulative counts, actual zero daily counts, unavailable days and the Beijing snapshot time', () => {
  const card = mountCard();
  const downloads = card.get('[data-metric="downloads"]');
  expect(downloads.text()).toContain('953');
  expect(downloads.text()).toContain('today(0)');
  expect(downloads.text()).toContain('yesterday(—)');
  expect(card.get('[data-metric="updates"]').text()).toContain('582');
  expect(card.get('[data-testid="github-release-snapshot"]').text()).toContain('17:10');
  expect(card.text()).toContain('dailyStart(2026-10-26)');
  expect(card.text()).toContain('downloadsHint');
  expect(card.text()).toContain('updatesHint');
  expect(card.text()).toContain('releasesHint');
  expect(card.text()).toContain('dailyHint');
  const rows = card.findAll('tbody tr');
  expect(rows).toHaveLength(4);
  expect(rows[1]!.text()).toContain('—');
  expect(card.text()).not.toContain('2026-10-25');
});

it('breaks the trend at missed days, preserves zeroes and exposes values by keyboard', async () => {
  const card = mountCard();
  expect(card.findAll('polyline[data-series="downloads"]')).toHaveLength(2);
  expect(card.findAll('polyline[data-series="updates"]')).toHaveLength(2);
  expect(card.get('polyline[data-series="updates"]').attributes('stroke-dasharray')).toBe('5 4');
  const frame = card.get('[tabindex="0"]');
  await frame.trigger('focus');
  expect(card.get('[role="status"]').text()).toContain('2');
  await frame.trigger('keydown', { key: 'ArrowLeft' });
  expect(card.get('[role="status"]').text()).toContain('—');
  await frame.trigger('keydown', { key: 'Home' });
  expect(card.get('[role="status"]').text()).toContain('10');
  await frame.trigger('keydown', { key: 'End' });
  expect(card.get('[role="status"]').text()).toContain('2');
  await frame.trigger('blur');
  expect(card.find('[role="status"]').exists()).toBe(false);
});

it('keeps the selected calendar day across a dashboard refresh', async () => {
  const card = mountCard();
  const frame = card.get('[tabindex="0"]');
  await frame.trigger('keydown', { key: 'Home' });
  const stats = fixture();
  stats.days.shift();
  stats.days.push({ date: '2026-10-30', downloads: 22, updates: 11 });
  await card.setProps({ stats });
  expect(card.get('[role="status"]').text()).toContain('10');
  stats.days.shift();
  await card.setProps({ stats: { ...stats } });
  expect(card.find('[role="status"]').exists()).toBe(false);
});

it('shows totals only for the first snapshot day and an actionable empty state before any snapshot', () => {
  const stats = fixture();
  stats.dailyStartDate = null;
  stats.downloads.today = null;
  stats.updates.today = null;
  const first = mountCard(stats);
  expect(first.get('[data-metric="downloads"]').text()).toContain('953');
  expect(first.text()).toContain('pending');
  expect(first.find('svg[role="img"]').exists()).toBe(false);
  expect(first.find('details').exists()).toBe(false);
  const empty = mountCard({
    ...stats,
    lastSnapshotAt: null,
    downloads: { today: null, yesterday: null, total: null },
    updates: { today: null, yesterday: null, total: null }
  });
  expect(empty.text()).toContain('never');
  expect(empty.text()).toContain('githubReleases.empty');
  expect(empty.get('[data-metric="downloads"]').text()).toContain('—');
});
