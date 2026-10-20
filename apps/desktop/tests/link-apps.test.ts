import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import { toBrewfile } from '@opennavo/shared';
import { createOnUi } from '@opennavo/ui';
import LinkDownloadPanel from '@/components/package/LinkDownloadPanel.vue';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import DownloadVerdictBanner from '@/components/package/DownloadVerdictBanner.vue';
import CategoryPage from '@/pages/CategoryPage.vue';
import SearchPage from '@/pages/SearchPage.vue';
import { startLocalState } from '@/stores';
import { setup } from './helpers';

// Link apps (store/security plan §4): mixed into search, Download opens the registered address (official page first,
// then GitHub), an app already on this Mac opens instead, and nothing about them ever reaches Homebrew.
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.restoreAllMocks();
});

const ui = createOnUi({ locale: ref('zh-CN' as const) });

async function search(q: string) {
  const { pinia, router } = await setup('/search');
  await startLocalState();
  const wrapper = mount(SearchPage, {
    global: { plugins: [pinia, router, i18n, ui] }
  });
  wrappers.push(wrapper);
  await flushPromises();
  await wrapper.find('input').setValue(q);
  await flushPromises();
  await flushPromises();
  return wrapper;
}

const row = (wrapper: VueWrapper, name: string) => wrapper.findAll('li').find(item => item.text().includes(name));

describe('Link apps in search', () => {
  it('Download opens GitHub releases when there is no official download page', async () => {
    const open = vi.spyOn(commands, 'linkOpen');
    const wrapper = await search('lumen');
    const item = row(wrapper, 'Lumen 笔记');
    expect(item?.find('a').attributes('href')).toBe('/package/link/lumen-notes');
    const button = item?.findAll('button').find(node => node.text() === '下载');
    await button?.trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalledWith('lumen-notes', 'github');
  });

  it('A match on the token adds no alias chip, since the token is already shown', async () => {
    const wrapper = await search('notes');
    const item = row(wrapper, 'Lumen 笔记');
    expect(item?.text()).toContain('lumen-notes');
    expect(item?.text()).not.toContain('别名');
  });

  it('Download prefers the official download page', async () => {
    const open = vi.spyOn(commands, 'linkOpen');
    const wrapper = await search('kite');
    await row(wrapper, '风筝剪贴板')
      ?.findAll('button')
      .find(node => node.text() === '下载')
      ?.trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalledWith('kite-clipboard', 'download');
  });

  it('An app already on this Mac (same bundle ID) opens instead of downloading', async () => {
    const open = vi.spyOn(commands, 'localAppOpen');
    const download = vi.spyOn(commands, 'linkOpen');
    const wrapper = await search('photoshop');
    const button = row(wrapper, 'Adobe Photoshop')
      ?.findAll('button')
      .find(node => node.text() === '打开');
    expect(button).toBeTruthy();
    await button?.trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalled();
    expect(download).not.toHaveBeenCalled();
  });

  it('Link apps never become Homebrew installs', async () => {
    await setup('/discover');
    const result = await commands.taskEnqueue('install', { kind: 'link', token: 'lumen-notes' }, {}, 'manual');
    expect(result.status === 'error' && result.error.code).toBe('E_NOT_INSTALLABLE');
    expect((await unwrap(commands.taskListActive())).some(task => task.target?.kind === 'link')).toBe(false);
    expect(
      toBrewfile([
        { kind: 'link', token: 'lumen-notes' },
        { kind: 'cask', token: 'zed' }
      ])
    ).toBe('cask "zed"\n');
  });
});

describe('Link apps in categories', () => {
  it('A category lists its link apps beside casks, with Download as the action', async () => {
    const list = vi.spyOn(commands, 'catalogList');
    const { pinia, router } = await setup('/categories/writing');
    await startLocalState();
    const wrapper = mount(CategoryPage, {
      props: { slug: 'writing' },
      global: { plugins: [pinia, router, i18n, ui] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    await flushPromises();
    expect(list).toHaveBeenCalledWith(
      expect.objectContaining({
        kind: 'cask',
        includeLinks: true,
        category: 'writing'
      })
    );
    const card = wrapper.find('a[href="/package/link/lumen-notes"]');
    expect(card.exists()).toBe(true);
    // Command-line tools never join, links only on request.
    const plain = await unwrap(
      commands.catalogList({
        kind: 'cask',
        category: 'writing',
        sort: 'popular',
        includeFonts: true,
        includeLibraries: false,
        includeDisabled: false,
        limit: 100,
        offset: 0
      })
    );
    expect(plain.items.some(item => item.kind === 'link')).toBe(false);
  });
});

describe('Link download panel', () => {
  it('Lists the release files with the one for this Mac first, and downloads the chosen file', async () => {
    const { pinia, router } = await setup('/package/link/lumen-notes');
    const open = vi.spyOn(commands, 'linkOpen');
    const item = await unwrap(commands.catalogGet('link', 'lumen-notes'));
    const wrapper = mount(LinkDownloadPanel, {
      props: { token: 'lumen-notes', link: item!.link! },
      global: { plugins: [pinia, router, i18n, ui] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    const names = wrapper.findAll('li').map(node => node.find('p').text());
    expect(names[0]).toBe('Lumen-2.4.1-arm64.dmg');
    expect(wrapper.findAll('li')[0]?.text()).toContain('推荐');
    expect(wrapper.text()).toContain('OpenNavo 不托管安装包');
    await wrapper.findAll('li')[0]?.find('button').trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalledWith('lumen-notes', {
      asset: 'Lumen-2.4.1-arm64.dmg'
    });
  });
});

describe('Link download panel: the installers that suit this Mac', () => {
  const file = (
    name: string,
    arch: 'arm64' | 'x86_64' | 'universal' | null,
    format: 'dmg' | 'zip' | 'pkg' | 'other'
  ) => ({
    name,
    arch,
    format,
    size: 1_000_000,
    url: `https://example.org/${name}`,
    sha256: null
  });
  // A suite released for Mac, Linux and Windows: two apps per chip, plus packages that no Mac can use.
  const assets = [
    file('ElyDoc-0.1.9-arm64.dmg', 'arm64', 'dmg'),
    file('ElyMark-0.1.9-arm64.dmg', 'arm64', 'dmg'),
    file('ElyDoc-0.1.9-x64.dmg', 'x86_64', 'dmg'),
    file('ely-doc_0.1.9-1_amd64.deb', 'x86_64', 'other'),
    file('ely-doc-0.1.9-1.x86_64.rpm', 'x86_64', 'other'),
    file('latest-mac.yml', null, 'other')
  ];
  async function panel(arch: 'arm64' | 'x86_64', list = assets) {
    const { pinia, router } = await setup('/package/link/lumen-notes');
    const { useEnvStore } = await import('@/stores');
    const env = useEnvStore();
    await env.detect();
    env.info!.arch = arch;
    const item = await unwrap(commands.catalogGet('link', 'lumen-notes'));
    const link = item!.link!;
    link.github!.latest!.assets = list;
    const wrapper = mount(LinkDownloadPanel, {
      props: { token: 'lumen-notes', link },
      global: { plugins: [pinia, router, i18n, ui] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    return wrapper;
  }
  const names = (wrapper: VueWrapper, list: number) => {
    const lists = wrapper.findAll('ul');
    return lists[list]!.findAll('li').map(item => item.find('p').text());
  };

  it('Lists only the Mac installers for an Apple silicon Mac, and puts the rest behind "other installers"', async () => {
    const wrapper = await panel('arm64');
    expect(names(wrapper, 0)).toEqual(['ElyDoc-0.1.9-arm64.dmg', 'ElyMark-0.1.9-arm64.dmg']);
    expect(wrapper.text()).toContain('已按这台 Mac（Apple 芯片）筛选');
    // Two apps fit: neither is "the" recommendation.
    expect(wrapper.text()).not.toContain('推荐');
    const summary = wrapper.find('details summary');
    expect(summary.text()).toBe('其他安装包（4）');
    expect(wrapper.find('details').attributes('open')).toBeUndefined();
    expect(names(wrapper, 1)).toEqual([
      'ElyDoc-0.1.9-x64.dmg',
      'ely-doc_0.1.9-1_amd64.deb',
      'ely-doc-0.1.9-1.x86_64.rpm',
      'latest-mac.yml'
    ]);
    // A Linux package is not an "Intel" installer.
    expect(row(wrapper, 'ely-doc_0.1.9-1_amd64.deb')?.text()).not.toContain('Intel');
    expect(row(wrapper, 'ElyDoc-0.1.9-x64.dmg')?.text()).toContain('Intel');
  });

  it('Lists the Intel installer on an Intel Mac, and recommends it as the only one that fits', async () => {
    const wrapper = await panel('x86_64');
    expect(names(wrapper, 0)).toEqual(['ElyDoc-0.1.9-x64.dmg']);
    expect(wrapper.text()).toContain('已按这台 Mac（Intel）筛选');
    expect(wrapper.findAll('li')[0]?.text()).toContain('推荐');
    expect(wrapper.find('details summary').text()).toBe('其他安装包（5）');
  });

  it('Hides nothing when no file can be taken for a Mac installer that fits', async () => {
    const odd = [file('Lumen.tar.gz', null, 'other'), file('Lumen-linux.zip', null, 'zip')];
    const wrapper = await panel('arm64', odd);
    expect(names(wrapper, 0)).toEqual(['Lumen-linux.zip', 'Lumen.tar.gz']);
    expect(wrapper.find('details').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('已按这台 Mac');
  });

  it('Downloads a file from the collapsed list as well', async () => {
    const open = vi.spyOn(commands, 'linkOpen');
    const wrapper = await panel('arm64');
    await row(wrapper, 'ElyDoc-0.1.9-x64.dmg')?.find('button')?.trigger('click');
    await flushPromises();
    expect(open).toHaveBeenCalledWith('lumen-notes', { asset: 'ElyDoc-0.1.9-x64.dmg' });
  });
});

describe('Download check', () => {
  async function download(flags = '') {
    history.replaceState(null, '', flags ? `/?mock=${flags}` : '/');
    const { pinia, router } = await setup('/package/link/lumen-notes', {
      tickMs: 5
    });
    await startLocalState();
    const wrapper = mount(DownloadVerdictBanner, {
      props: { token: 'lumen-notes' },
      global: { plugins: [pinia, router, i18n, ui] }
    });
    wrappers.push(wrapper);
    await unwrap(commands.linkOpen('lumen-notes', { asset: 'Lumen-2.4.1-arm64.dmg' }));
    await vi.waitFor(() => expect(wrapper.text()).toContain('已核验'));
    return wrapper;
  }

  afterEach(() => history.replaceState(null, '', '/'));

  it('Shows the verdict of the downloaded file and opens only that file', async () => {
    const act = vi.spyOn(commands, 'downloadAct');
    const wrapper = await download();
    const text = wrapper.text();
    expect(text).toContain('已核验 Lumen-2.4.1-arm64.dmg');
    for (const part of ['与登记一致', '已经 Apple 公证', '病毒库未发现威胁']) expect(text).toContain(part);
    await wrapper
      .findAll('button')
      .find(node => node.text() === '打开安装包')!
      .trigger('click');
    await flushPromises();
    expect(act).toHaveBeenCalledWith('/Users/mock/Downloads/Lumen-2.4.1-arm64.dmg', 'open');
  });

  it('Warns when the file is signed by someone else and offers no Open', async () => {
    const wrapper = await download('download-mismatch');
    expect(wrapper.text()).toContain('与登记的开发者不一致');
    expect(wrapper.text()).toContain('没有经过 Apple 公证');
    expect(wrapper.findAll('button').some(node => node.text() === '打开安装包')).toBe(false);
    expect(wrapper.findAll('button').some(node => node.text() === '在访达中显示')).toBe(true);
  });
});

describe('Download recommendation platform compatibility', () => {
  it.each(['arm64', 'x86_64'] as const)('Recommends only macOS installers for %s', async arch => {
    const { pinia, router } = await setup('/package/link/lumen-notes');
    const { useEnvStore } = await import('@/stores');
    const env = useEnvStore();
    await env.detect();
    env.info!.arch = arch;
    const item = await unwrap(commands.catalogGet('link', 'lumen-notes'));
    const link = item!.link!;
    const asset = (name: string, assetArch: 'arm64' | 'x86_64' | 'universal' = arch) => ({
      name,
      arch: assetArch,
      format: 'zip' as const,
      size: 100,
      url: `https://example.org/${name}`,
      sha256: null
    });
    const incompatible = [asset('AAA-linux.zip'), asset('AAA-windows.zip', 'universal'), asset('AAA-unknown.zip')];
    link.github!.latest!.assets = [...incompatible, asset('ZZZ-darwin.zip')];
    const wrapper = mount(LinkDownloadPanel, {
      props: { token: 'lumen-notes', link },
      global: { plugins: [pinia, router, i18n, ui] }
    });
    wrappers.push(wrapper);
    expect(wrapper.findAll('li')[0]?.text()).toContain('ZZZ-darwin.zip');
    expect(wrapper.findAll('li')[0]?.text()).toContain('推荐');
    for (const name of incompatible.map(incompatibleAsset => incompatibleAsset.name)) {
      expect(row(wrapper, name)?.text()).not.toContain('推荐');
    }
    await wrapper.setProps({
      link: {
        ...link,
        github: {
          ...link.github!,
          latest: {
            ...link.github!.latest!,
            assets: incompatible
          }
        }
      }
    });
    expect(wrapper.text()).not.toContain('推荐');
    await wrapper.setProps({
      link: {
        ...link,
        github: {
          ...link.github!,
          latest: {
            ...link.github!.latest!,
            assets: [...incompatible, asset('ZZZ-macos.zip', 'universal')]
          }
        }
      }
    });
    expect(wrapper.findAll('li')[0]?.text()).toContain('ZZZ-macos.zip');
    expect(wrapper.findAll('li')[0]?.text()).toContain('推荐');
  });
});
