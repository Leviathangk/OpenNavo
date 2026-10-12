import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { formatDate, formatTime } from '@opennavo/shared';
import type { ReleaseEntry } from '@opennavo/api';
import { providePackage } from '@/composables/usePackageDetail';
import { i18n } from '@/i18n';
import PackageVersions from '@/pages/package/PackageVersions.vue';
import { startLocalState } from '@/stores';
import { setup } from './helpers';

// M4-08: the date shown for updating to a version matches local tasks (fixture: gh auto-updated from 2.101.0 to 2.102.0 today).
function release(version: string, isLatest = false): ReleaseEntry {
  return {
    id: null,
    version,
    title: null,
    publishedAt: '2026-09-30T08:00:00Z',
    brewCommittedAt: '2026-09-30T09:00:00Z',
    source: 'github_release',
    sourceUrl: `https://github.com/cli/cli/releases/tag/v${version}`,
    isLatest,
    isPrerelease: false,
    hasNotes: true,
    summary: null,
    sections: [{ area: '命令', items: ['改进输出'] }],
    bodyMarkdown: null,
    translation: { status: 'manual', locale: 'zh-CN' }
  };
}

const scenario = vi.hoisted(() => ({ older: false }));
afterEach(() => {
  scenario.older = false;
});

const ok = (data: unknown) => ({
  data: { code: '0000', msg: 'ok', data },
  error: undefined,
  response: new Response()
});

vi.mock('@/api', () => ({
  api: {
    GET: vi.fn(
      async (
        path: string,
        options: {
          params?: { query?: { onlyWithNotes?: boolean; size?: number; current?: number } };
        }
      ) => {
        if (path === '/packages/{kind}/{token}/releases') {
          const records = scenario.older
            ? Array.from({ length: 45 }, (_, index) => release(`2.${102 - index}.0`, index === 0))
            : [release('2.102.0', true), release('2.101.0'), release('2.100.0')];
          const size = options.params?.query?.size ?? 20;
          return ok({
            current: options.params?.query?.current ?? 1,
            size,
            total: records.length,
            records: records.slice(
              ((options.params?.query?.current ?? 1) - 1) * size,
              (options.params?.query?.current ?? 1) * size
            ),
            stats: {
              count30d: 3,
              cadence: 'weekly',
              brewLag: { medianMinutes: 30, earlierCount: 1, compared: 3 }
            }
          });
        }
        return ok({ kind: 'formula', token: 'gh', autoUpdates: false, releaseStats: null });
      }
    ),
    POST: vi.fn(async () => ok([]))
  }
}));

describe('Desktop release history', () => {
  it('Local annotations appear only on successfully updated task versions with matching time/duration', async () => {
    const context = await setup('/package/formula/gh/versions', { tickMs: 1000 });
    await startLocalState();
    const Host = defineComponent({
      setup() {
        providePackage({ kind: 'formula', token: 'gh' });
        return () => h(PackageVersions);
      }
    });
    const wrapper = mount(Host, { global: { plugins: [context.pinia, context.router, i18n] } });
    await flushPromises();
    await flushPromises();

    const task = context.state.history.find(item => item.target?.token === 'gh' && item.toVersion === '2.102.0');
    expect(task?.finishedAt).toBeTruthy();
    const at = `${formatDate(task!.finishedAt!, { locale: 'zh-CN' })} ${formatTime(task!.finishedAt!)}`;

    const items = wrapper.findAll('li').filter(item => item.find('article').exists());
    expect(items).toHaveLength(3);
    const notes = items.map(item => item.find('.on-version-entry__local'));
    expect(notes[0]?.text()).toBe(`你在 ${at} 通过 OpenNavo 从 2.101.0 更新到此版本 · 用时 38 秒`);
    expect(notes[1]?.exists()).toBe(false);
    expect(notes[2]?.exists()).toBe(false);
    expect(wrapper.text()).toContain('我装过的 1');
  });
});

it('Previously installed finds page-three versions and local history entry 201', async () => {
  scenario.older = true;
  const context = await setup('/package/formula/gh/versions');
  const sample = context.state.history.find(item => item.target?.token === 'gh')!;
  context.state.history = Array.from({ length: 201 }, (_, index) => ({
    ...sample,
    id: `old-${index}`,
    toVersion: index === 200 ? '2.60.0' : '2.102.0',
    finishedAt: Date.now() - index * 1000
  }));
  await startLocalState();
  const Host = defineComponent({
    setup() {
      providePackage({ kind: 'formula', token: 'gh' });
      return () => h(PackageVersions);
    }
  });
  const wrapper = mount(Host, { global: { plugins: [context.pinia, context.router, i18n] } });
  await flushPromises();
  await flushPromises();
  expect(wrapper.text()).toContain('我装过的 2');
  const mine = wrapper.findAll('button').find(button => button.text().includes('我装过的'))!;
  await mine.trigger('click');
  await flushPromises();
  await flushPromises();
  const versions = wrapper.findAll('article').map(item => item.text());
  expect(versions).toHaveLength(2);
  expect(versions.some(text => text.includes('2.60.0'))).toBe(true);
  wrapper.unmount();
});
