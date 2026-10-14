import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { h } from 'vue';
import type { ReleaseEntry } from '@opennavo/api';
import { formatDate } from '@opennavo/shared';
import OnVersionEntry from '../src/components/OnVersionEntry.vue';
import OnVersionTimeline from '../src/components/OnVersionTimeline.vue';
import { isMachineTranslated, isPatchVersion, parseInline } from '../src/utils/version';
import { withLocale } from './helpers';

function entry(version: string, patch: Partial<ReleaseEntry> = {}): ReleaseEntry {
  return {
    id: 1,
    version,
    title: null,
    publishedAt: '2026-09-30T08:00:00Z',
    brewCommittedAt: '2026-09-30T14:53:00Z',
    source: 'webpage',
    sourceUrl: `https://code.visualstudio.com/updates/v${version}`,
    isLatest: false,
    isPrerelease: false,
    hasNotes: true,
    summary: null,
    sections: [{ area: '智能体', items: ['多文件夹会话，可把任务**委派给远程 Agent 主机**，配置写在 `.mcp.json`'] }],
    bodyMarkdown: null,
    translation: { status: 'manual', locale: 'zh-CN' },
    ...patch
  };
}

describe('Release highlights and version numbers', () => {
  it('Recognizes only bold and code inline', () => {
    expect(parseInline('提升**约 12 倍**，见 `a.json` 与 *斜体*')).toEqual([
      { type: 'text', text: '提升' },
      { type: 'strong', text: '约 12 倍' },
      { type: 'text', text: '，见 ' },
      { type: 'code', text: 'a.json' },
      { type: 'text', text: ' 与 *斜体*' }
    ]);
  });

  it('Recognizes three-part numeric patch versions whose final segment is nonzero', () => {
    expect(isPatchVersion('1.139.1')).toBe(true);
    expect(isPatchVersion('1.140.0')).toBe(false);
    expect(isPatchVersion('143.0')).toBe(false);
    expect(isPatchVersion('2026.1.2-beta')).toBe(false);
  });

  it('Labels displayed machine translations but not human corrections, originals, or source fallbacks', () => {
    expect(isMachineTranslated({ translation: { status: 'machine' } })).toBe(true);
    expect(isMachineTranslated({ translation: { status: 'pending', machineTranslated: true } })).toBe(true);
    expect(isMachineTranslated({ translation: { status: 'manual' } })).toBe(false);
    expect(isMachineTranslated({ translation: { status: 'source' } })).toBe(false);
    expect(isMachineTranslated({ translation: { status: 'machine', machineTranslated: false } })).toBe(false);
  });
});

describe('OnVersionEntry', () => {
  it.each([false, true])('Falls back from missing or invalid release dates (collapsed=%s)', async collapsed => {
    const brewCommittedAt = '2026-09-23T08:07:03Z';
    const wrapper = mount(OnVersionEntry, {
      props: { entry: entry('5.6.2', { source: 'github_release', brewCommittedAt }), collapsed },
      global: withLocale('zh-CN')
    });
    const dateText = () => wrapper.find('span.text-12\\.5px').text();
    // Valid publication dates still take precedence over the adoption date.
    expect(dateText()).toBe(formatDate('2026-09-30T08:00:00Z', { locale: 'zh-CN', weekday: !collapsed }));
    for (const publishedAt of [null, '', '0001-01-01T00:00:00Z', 'invalid-date']) {
      await wrapper.setProps({
        entry: entry('5.6.2', { source: 'github_release', publishedAt, brewCommittedAt })
      });
      expect(dateText()).toBe(formatDate(brewCommittedAt, { locale: 'zh-CN', weekday: !collapsed }));
      expect(wrapper.text()).not.toContain('1年1月1日');
      expect(wrapper.text()).not.toContain('NaN');
    }
    for (const invalid of [null, '0001-01-01T00:00:00Z', 'invalid-date']) {
      await wrapper.setProps({
        entry: entry('5.6.2', { publishedAt: invalid, brewCommittedAt: invalid })
      });
      expect(dateText()).toBe('');
      expect(wrapper.text()).not.toContain('Homebrew 收录');
    }
  });

  it('Displays summaries and grouped highlights together and shows descriptions for summary-only releases', async () => {
    const summary = '保留远程执行环境。\n\n适用于跨平台 MCP 启动场景。';
    const wrapper = mount(OnVersionEntry, {
      props: { entry: entry('0.160.1', { summary }) },
      global: withLocale('zh-CN')
    });
    expect(wrapper.find('p.whitespace-pre-line').text()).toBe(summary);
    expect(wrapper.find('dl').exists()).toBe(true);
    await wrapper.setProps({ entry: entry('0.160.1', { summary, sections: [] }) });
    expect(wrapper.find('p.whitespace-pre-line').text()).toBe(summary);
    expect(wrapper.find('dl').exists()).toBe(false);
    await wrapper.setProps({ collapsed: true });
    expect(wrapper.text()).not.toContain(summary);
  });

  it('Displays the latest badge, source line, and grouped highlights with bold and code rendered by segment', () => {
    const wrapper = mount(OnVersionEntry, {
      props: { entry: entry('1.140.0', { isLatest: true, translation: { status: 'machine', locale: 'zh-CN' } }) },
      global: withLocale('zh-CN')
    });
    expect(wrapper.classes()).toContain('on-version-entry--latest');
    expect(wrapper.text()).toContain('最新');
    expect(wrapper.text()).toContain('官网更新页');
    expect(wrapper.text()).toContain('Homebrew 收录');
    expect(wrapper.text()).toContain('AI 翻译');
    expect(wrapper.find('strong').text()).toBe('委派给远程 Agent 主机');
    expect(wrapper.find('code').text()).toBe('.mcp.json');
    expect(wrapper.find('a').attributes('href')).toBe('https://code.visualstudio.com/updates/v1.140.0');
  });

  it('Omits the AI translation label in source mode and provides an original link when body content is missing', async () => {
    const wrapper = mount(OnVersionEntry, {
      props: {
        entry: entry('1.139.0', { hasNotes: false, sections: [], translation: { status: 'machine', locale: 'zh-CN' } }),
        showOriginal: true
      },
      global: withLocale('zh-CN')
    });
    expect(wrapper.text()).not.toContain('AI 翻译');
    expect(wrapper.text()).toContain('此版本没有单独的更新说明');
    await wrapper.find('a[href$="1.139.0"]').trigger('click');
    expect(wrapper.emitted('openSource')).toHaveLength(1);
  });

  it('Delegates Markdown-only bodies to the markdown slot and places the local installation marker in the notice bar', () => {
    const wrapper = mount(OnVersionEntry, {
      props: { entry: entry('1.139.1', { sections: [], bodyMarkdown: '## 修复\n\n修复问题' }), installed: true },
      slots: {
        markdown: ({ source }: { source: string }) => h('div', { class: 'md' }, source),
        local: () => h('span', '你在 9月26日 10:12 更新到此版本')
      },
      global: withLocale('zh-CN')
    });
    expect(wrapper.find('.md').text()).toContain('## 修复');
    expect(wrapper.text()).toContain('当前安装');
    expect(wrapper.text()).toContain('补丁');
    expect(wrapper.find('.on-version-entry__local').text()).toContain('你在 9月26日 10:12');
  });

  it('Shows one collapsed line, emits toggle on click, and uses English copy', async () => {
    const wrapper = mount(OnVersionEntry, {
      props: { entry: entry('1.138.0'), collapsed: true },
      global: withLocale('en-US')
    });
    expect(wrapper.find('dl').exists()).toBe(false);
    expect(wrapper.text()).toContain('Expand');
    await wrapper.find('button').trigger('click');
    expect(wrapper.emitted('toggle')).toHaveLength(1);
  });
});

describe('OnVersionTimeline', () => {
  const entries = [
    entry('1.140.0', { isLatest: true }),
    entry('1.139.1'),
    entry('1.139.0'),
    entry('1.138.0'),
    entry('1.137.0')
  ];

  it('Initially expands three releases, allows collapsing after expansion, and marks the installed release with a hollow green dot', async () => {
    const wrapper = mount(OnVersionTimeline, {
      props: { entries, installedVersion: '1.139.1', moreCount: 12 },
      global: withLocale('zh-CN')
    });
    const items = wrapper.findAll('li');
    expect(items).toHaveLength(5);
    expect(items[0]?.find('.on-version-timeline__dot--latest').exists()).toBe(true);
    expect(items[1]?.find('.on-version-timeline__dot--installed').exists()).toBe(true);
    expect(items[3]?.find('dl').exists()).toBe(false);
    await items[3]?.find('button').trigger('click');
    expect(wrapper.findAll('li')[3]?.find('dl').exists()).toBe(true);
    expect(wrapper.findAll('li')[3]?.text()).toContain('收起');
    expect(wrapper.text()).toContain('显示更早的 12 个版本');
  });

  it('Shows Update to this version according to the consumer decision and emits update', async () => {
    const wrapper = mount(OnVersionTimeline, {
      props: { entries, installedVersion: '1.139.1', canUpdate: (item: ReleaseEntry) => item.version === '1.140.0' },
      global: withLocale('zh-CN')
    });
    const button = wrapper.findAll('button').find(item => item.text() === '更新到此版本');
    expect(button).toBeDefined();
    await button?.trigger('click');
    expect(wrapper.emitted('update')?.[0]?.[0]).toMatchObject({ version: '1.140.0' });
  });
  it('Labels build entries and marks the installed build on its own entry or its base version entry', () => {
    const builds = [
      entry('0.1,49', { isLatest: true, versionBase: '0.1', build: '49' }),
      entry('0.1,48', { versionBase: '0.1', build: '48' }),
      entry('0.1', { versionBase: '0.1' })
    ];
    const own = mount(OnVersionTimeline, {
      props: { entries: builds, installedVersion: '0.1,48' },
      global: withLocale('en-US')
    });
    const items = own.findAll('li');
    expect(items[0]?.find('h3').text()).toBe('0.1 (49)');
    expect(items[0]?.find('h3').attributes('title')).toBe('0.1,49');
    expect(items[2]?.find('h3').text()).toBe('0.1');
    expect(items[1]?.find('.on-version-timeline__dot--installed').exists()).toBe(true);
    const shared = mount(OnVersionTimeline, {
      props: { entries: builds, installedVersion: '0.1,40' },
      global: withLocale('en-US')
    });
    expect(shared.findAll('li')[2]?.find('.on-version-timeline__dot--installed').exists()).toBe(true);
    expect(shared.findAll('li')[1]?.find('.on-version-timeline__dot--installed').exists()).toBe(false);
  });
});
