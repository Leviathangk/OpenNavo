import { flushPromises, mount } from '@vue/test-utils';
import { formatBytes } from '@opennavo/shared';
import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import CleanupResult from '@/components/cleanup/CleanupResult.vue';
import CleanupCategoryCard from '@/components/cleanup/CleanupCategoryCard.vue';
import CleanupSettings from '@/components/settings/CleanupSettings.vue';
import LibraryRail from '@/components/rails/LibraryRail.vue';
import RunningTask from '@/components/updates/RunningTask.vue';
import AppShell from '@/layouts/AppShell.vue';
import CleanupPage from '@/pages/CleanupPage.vue';
import { i18n } from '@/i18n';
import type { CategoryReport, CleanupCategory, CleanupProgress } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { startLocalState, usePermissionsStore, useSettingsStore } from '@/stores';
import { usePermissionGateStore } from '@/stores/permissionGate';
import { useTasksStore } from '@/stores/tasks';
import { groupKey, useCleanupStore } from '@/stores/cleanup';
import { useToasts } from '@/composables/useToasts';
import { mockMatchMedia, setup } from './helpers';

// Clean Up Mac (cleanup proposal §03–§09): scan defaults, read-only progress, skips, protection, execution and entry points.
const text = () => document.body.textContent ?? '';
const size = (bytes: number) => formatBytes(bytes, { locale: 'zh-CN' });

async function context(path = '/cleanup', mock = '') {
  window.history.replaceState(null, '', `/?mock=full_disk_access${mock ? ',' + mock : ''}`);
  const result = await setup(path, { brew: true, tickMs: 5 });
  await startLocalState();
  return result;
}

/** Scan through the store; with no page mounted, publish once the bar would have reached 100%. */
async function scanned() {
  const store = useCleanupStore();
  void store.scan();
  await vi.waitFor(() => expect(store.stage).toBe('finishing'), { timeout: 3000 });
  store.finishScan(store.requestId!);
  expect(store.stage).toBe('review');
  return store;
}

const entry = (path: string) => useCleanupStore().entries.find(item => item.item.path.endsWith(path));

/** Unfold a category and each of its groups, as clicking through the card would. */
function unfold(key: CleanupCategory) {
  const store = useCleanupStore();
  const category = store.report!.categories.find(item => item.key === key)!;
  for (const id of [key, ...category.groups.map(group => groupKey(key, group))])
    if (!store.expanded.includes(id)) store.toggleExpanded(id);
  return category;
}

beforeAll(() => {
  mockMatchMedia(() => true);
});

afterEach(() => {
  document.body.innerHTML = '';
  useToasts().clear();
  vi.restoreAllMocks();
});

describe('Scan defaults', () => {
  it('Selects every rebuildable item and old review items; recent leftovers wait for the user', async () => {
    await context();
    const store = await scanned();
    const rebuildable = store.entries.filter(item => item.category.risk === 'rebuildable');
    expect(rebuildable.length).toBeGreaterThan(0);
    // Every rebuildable item starts checked except the newest device support version per device (slow to rebuild)
    // and caches the next build has to download again.
    const latest = rebuildable.filter(
      item => item.item.reason === 'device_support_latest' || item.item.reason === 'tool_download_cache'
    );
    expect(latest.length).toBeGreaterThan(0);
    expect(latest.some(item => store.isChecked(item.item))).toBe(false);
    expect(
      rebuildable
        .filter(item => item.item.reason !== 'device_support_latest' && item.item.reason !== 'tool_download_cache')
        .every(item => store.isChecked(item.item))
    ).toBe(true);
    expect(rebuildable.every(item => item.item.deleteMode === 'delete')).toBe(true);
    expect(store.isChecked(entry('Application Support/com.bohemiancoding.sketch3')!.item)).toBe(true);
    expect(store.isChecked(entry('com.loom.desktop.plist')!.item)).toBe(false);
    // Background items of deleted apps that still load are always suggested, and they start unfolded down to their
    // groups; every other group starts folded.
    expect(store.activeServices).toHaveLength(2);
    expect(store.activeServices.every(item => store.isChecked(item.item))).toBe(true);
    const leftovers = store.categories.find(category => category.key === 'leftovers')!;
    const unfolded = leftovers.groups.filter(group => store.expanded.includes(groupKey('leftovers', group)));
    expect(unfolded.map(group => group.title)).toEqual(['TeamViewer', 'Figma Agent']);
    expect(store.expanded).toEqual(['leftovers', ...unfolded.map(group => groupKey('leftovers', group))]);

    const { totals } = store;
    expect(totals.count).toBe(store.chosen.length);
    expect(totals.nowBytes + totals.trashBytes).toBe(totals.bytes);
    expect(totals.trashBytes).toBeGreaterThan(0);
    store.onlyRebuildable();
    expect(store.chosen.every(item => item.category.risk === 'rebuildable')).toBe(true);
    store.recommended();
    expect(store.isChecked(entry('Application Support/com.bohemiancoding.sketch3')!.item)).toBe(true);
  });
});

describe('Scanning', () => {
  it('Progress covers the whole scan and results appear only after the bar reaches 100%', async () => {
    const { pinia, router } = await context();
    const wrapper = mount(CleanupPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    const store = useCleanupStore();
    const seen: CleanupProgress[] = [];
    let atFinish: CleanupProgress | null = null;
    store.$onAction(({ name, args }) => {
      if (name === 'applyProgress') seen.push(args[0] as CleanupProgress);
      if (name === 'finishScan') atFinish = store.progress;
    });
    await wrapper
      .findAll('button')
      .find(button => button.text() === '开始扫描')!
      .trigger('click');
    await flushPromises();
    expect(store.stage).toBe('scanning');
    expect(wrapper.find('[data-testid="cleanup-scanning"]').exists()).toBe(true);
    await vi.waitFor(() => expect(store.stage).toBe('review'), { timeout: 3000 });

    expect(seen.length).toBeGreaterThan(2);
    expect(seen.every(event => event.total === seen[0]!.total)).toBe(true);
    expect(seen.every((event, index) => index === 0 || event.foundBytes >= seen[index - 1]!.foundBytes)).toBe(true);
    expect(atFinish).toMatchObject({ checked: seen[0]!.total, total: seen[0]!.total });
    expect(text()).toContain('应用缓存');
    expect(text()).toContain(`清理 ${size(store.totals.bytes)}`);
    wrapper.unmount();
  });

  it('Canceling publishes nothing, even after the scan would have finished', async () => {
    await context();
    const store = useCleanupStore();
    const cancel = vi.spyOn(commands, 'cleanupCancel');
    void store.scan();
    await vi.waitFor(() => expect(store.progress).not.toBeNull(), { timeout: 3000 });
    await store.cancel();
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(store.stage).toBe('idle');
    await new Promise(resolve => setTimeout(resolve, 200));
    expect(store.stage).toBe('idle');
    expect(store.report).toBeNull();
  });
});

describe('Skips and permissions', () => {
  it('Measures caches of running apps without selecting them; the summary and the card show the space they hold', async () => {
    const { pinia, router } = await context('/cleanup', 'running-apps');
    const store = await scanned();
    const browsers = store.categories.find(category => category.key === 'browser_caches')!;
    const chrome = browsers.skipped.find(group => group.title === 'Google Chrome')!;
    expect(chrome.reason).toBe('running');
    expect(chrome.bytes).toBeGreaterThan(0);
    expect(store.entries.some(item => item.group.title === 'Google Chrome')).toBe(false);
    // Full Disk Access also exposes caches held by running apps inside protected containers.
    expect(store.inUse).toBe(2_620_000_000);
    // Personal files (backups, archives) are listed but never counted as reclaimable.
    expect(store.totals.reclaimable).toBe(
      store.entries
        .filter(item => item.category.risk !== 'personal')
        .reduce((sum, item) => sum + (item.item.sizeBytes ?? 0), 0)
    );

    const wrapper = mount(CleanupPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    // Routine skips get sizes, not a notice naming every running app.
    expect(text()).toContain(`另有 ${size(store.inUse)} 正在使用`);
    expect(wrapper.get('[data-category="browser_caches"]').text()).toContain(`${size(chrome.bytes!)} 正在使用`);
    expect(text()).not.toContain('相关缓存先跳过了');
    wrapper.unmount();
  });

  it('A locked scan does not start until the user explicitly continues', async () => {
    const { pinia, router } = await context();
    const permissions = usePermissionsStore();
    permissions.apply({ ...permissions.status!, fullDiskAccess: 'denied' });
    vi.spyOn(commands, 'permissionsGet').mockImplementation(async () => ({ status: 'ok', data: permissions.status! }));
    const scan = vi.spyOn(commands, 'cleanupScan');
    const store = useCleanupStore();
    const wrapper = mount(CleanupPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    const request = store.scan();
    await flushPromises();
    expect(store.stage).toBe('idle');
    expect(scan).not.toHaveBeenCalled();
    permissions.apply({ ...permissions.status!, fullDiskAccess: 'granted' });
    await flushPromises();
    expect(scan).not.toHaveBeenCalled();
    usePermissionGateStore().proceed();
    await request;
    expect(scan).toHaveBeenCalledTimes(1);
    wrapper.unmount();
  });

  it('With Full Disk Access containers and Safari are scanned too', async () => {
    await context('/cleanup', 'full_disk_access');
    const store = await scanned();
    expect(store.report?.incomplete).toEqual([]);
    expect(store.entries.some(item => item.group.title === 'Safari')).toBe(true);
    // WeChat runs in the fixture: its container cache is measured but skipped.
    const wechat = store.categories
      .find(category => category.key === 'app_caches')
      ?.skipped.find(group => group.title === 'WeChat');
    expect(wechat?.reason).toBe('running');
    expect(wechat?.bytes).toBeGreaterThan(0);
  });
});

describe('Category card', () => {
  it('Merges skipped folders of one app, lists the largest first, and sums what running apps hold', async () => {
    const { pinia } = await context();
    const store = useCleanupStore();
    store.toggleExpanded('app_caches');
    const category: CategoryReport = {
      key: 'app_caches',
      risk: 'rebuildable',
      bytes: 0,
      complete: true,
      groups: [],
      skipped: [
        { title: 'Slack', appId: 'local-slack', bytes: 200_000_000, reason: 'running', processes: ['Slack'] },
        { title: '1Password', appId: null, bytes: null, reason: 'protected', processes: [] },
        { title: 'WeChat', appId: 'cask-wechat', bytes: 1_000_000_000, reason: 'running', processes: ['WeChat'] },
        { title: 'WeChat', appId: 'cask-wechat', bytes: 500_000_000, reason: 'running', processes: ['WeChat Helper'] }
      ]
    };
    const wrapper = mount(CleanupCategoryCard, {
      props: { category },
      global: { plugins: [pinia, i18n] }
    });
    expect(wrapper.text()).toContain(`${size(1_700_000_000)} 正在使用`);
    const rows = wrapper.findAll('[data-category] > div:last-child > div > div').map(row => row.text());
    expect(rows).toHaveLength(3);
    expect(rows[0]).toContain('WeChat');
    expect(rows[0]).toContain(size(1_500_000_000));
    // What holds a skipped group is named, merged across its folders.
    expect(rows[0]).toContain('WeChat、WeChat Helper 正在运行，退出后可以清理');
    expect(rows[2]).toContain('受保护');
    expect(rows[1]).toContain('Slack');
    expect(rows[2]).toContain('1Password');
    expect(wrapper.text()).toContain('已跳过 3 项');
  });

  it('Unfolds to one row per group with its own tick; the items of a group stay folded until it is unfolded', async () => {
    const { pinia } = await context();
    const store = await scanned();
    store.toggleExpanded('dev_caches');
    const category = store.report!.categories.find(item => item.key === 'dev_caches')!;
    const wrapper = mount(CleanupCategoryCard, { props: { category }, global: { plugins: [pinia, i18n] } });
    expect(wrapper.text()).toContain('iPhone16,2');
    expect(wrapper.text()).not.toContain('24A437');
    const row = wrapper.findAll('button[aria-expanded]').find(button => button.text().includes('iPhone16,2'))!;
    expect(row.attributes('aria-expanded')).toBe('false');
    await row.trigger('click');
    expect(row.attributes('aria-expanded')).toBe('true');
    expect(wrapper.text()).toContain('iPhone16,2 27.0 (24A437)');
    // Only that group unfolds.
    expect(wrapper.text()).not.toContain('24A5418b');

    // The newest version starts unchecked, so the group's tick is mixed; it ticks and unticks the whole group only.
    const group = category.groups.find(item => item.title === 'iPhone16,2')!;
    const tick = wrapper.get<HTMLInputElement>('input[aria-label="iPhone16,2"]');
    expect(tick.element.indeterminate).toBe(true);
    await tick.setValue(true);
    expect(store.groupState(group)).toBe('on');
    await tick.setValue(false);
    expect(store.groupState(group)).toBe('off');
    expect(store.isChecked(entry('iPad13,4 26.5 (23F77)')!.item)).toBe(true);

    await row.trigger('click');
    expect(wrapper.text()).not.toContain('24A437');
  });
});

describe('Scan coverage (C2)', () => {
  it('Keeps the newest device support version per device and checks the older ones', async () => {
    const { pinia } = await context();
    const store = await scanned();
    const latest = entry('iPhone16,2 27.0 (24A437)')!;
    expect(latest.item.reason).toBe('device_support_latest');
    expect(store.isChecked(latest.item)).toBe(false);
    expect(store.isChecked(entry('iPhone16,2 26.5 (23F77)')!.item)).toBe(true);
    expect(store.isChecked(entry('iPad13,4 26.5 (23F77)')!.item)).toBe(true);

    const category = unfold('dev_caches');
    const wrapper = mount(CleanupCategoryCard, { props: { category }, global: { plugins: [pinia, i18n] } });
    expect(wrapper.text()).toContain('Xcode 设备支持文件 · iOS');
    const chip = wrapper.findAll('[title]').find(node => node.text() === '最新版本');
    expect(chip?.attributes('title')).toContain('下次连接这台设备时 Xcode 会重新准备');
    // The other new small items are listed in their categories.
    expect(entry('C/clang/ModuleCache')).toBeDefined();
    expect(entry('.npm/_logs')).toBeDefined();
    expect(entry('GoogleUpdater/crx_cache')).toBeDefined();
    expect(entry('Application Support/Code/CachedData')).toBeDefined();
  });

  it('A long-running `go run` no longer holds the Go build cache; compiling does', async () => {
    await context();
    let store = await scanned();
    expect(store.isChecked(entry('Library/Caches/go-build')!.item)).toBe(true);

    await context('/cleanup', 'cleanup-go-compiling');
    store = await scanned();
    expect(entry('Library/Caches/go-build')).toBeUndefined();
    const dev = store.report!.categories.find(item => item.key === 'dev_caches')!;
    expect(dev.skipped).toContainEqual(expect.objectContaining({ title: 'Go', reason: 'busy', processes: ['go'] }));
  });

  it('Lists caches that must be downloaded again unchecked, with the reason', async () => {
    const { pinia } = await context();
    const store = await scanned();
    for (const path of ['.gradle/caches', 'go/pkg/mod', 'Caches/ms-playwright']) {
      const found = entry(path)!;
      expect(found.item.reason).toBe('tool_download_cache');
      expect(store.isChecked(found.item)).toBe(false);
    }
    const category = unfold('dev_caches');
    const wrapper = mount(CleanupCategoryCard, { props: { category }, global: { plugins: [pinia, i18n] } });
    const chip = wrapper.findAll('[title]').find(node => node.text() === '要重新下载');
    expect(chip?.attributes('title')).toContain('下次构建或测试时要重新下载');
  });

  it('A tool cache keeps the files a running process has open and says so', async () => {
    const { pinia } = await context();
    await scanned();
    const go = entry('Library/Caches/go-build')!;
    expect(go.item.keptOpenFiles).toBe(3);
    const category = unfold('dev_caches');
    const wrapper = mount(CleanupCategoryCard, { props: { category }, global: { plugins: [pinia, i18n] } });
    expect(wrapper.text()).toContain('保留 3 个正在使用的文件');
  });
});

describe('Scan coverage (C3)', () => {
  it('Offers build output of old projects and lists recent ones; installers, the Trash and backups follow their rules', async () => {
    await context('/cleanup', 'full_disk_access');
    const store = await scanned();
    // Old projects: checked and deleted directly. Recent ones are measured and listed, never offered.
    const target = entry('rust-cli/target')!;
    expect(store.isChecked(target.item)).toBe(true);
    expect(target.item.deleteMode).toBe('delete');
    expect(entry('website/node_modules')).toBeUndefined();
    const projects = store.report!.categories.find(item => item.key === 'project_artifacts')!;
    expect(projects.skipped).toContainEqual(expect.objectContaining({ title: 'website', reason: 'recent' }));
    // Installers go to the Trash; only those of installed apps start checked.
    const docker = entry('Docker-arm64-4.30.0.dmg')!;
    expect(store.isChecked(docker.item)).toBe(true);
    expect(docker.item.deleteMode).toBe('trash');
    expect(store.isChecked(entry('Setup-Tool-2.1.pkg')!.item)).toBe(false);
    // Old temp files are rebuildable and checked.
    expect(store.isChecked(entry('T/go-build3429764944')!.item)).toBe(true);
    // The Trash, backups and archives are never preselected; backups and archives go to the Trash.
    for (const path of ['old-exports', '00008130-001A2B3C4D5E', 'Acme 2026-7-4, 22.41.xcarchive'])
      expect(store.isChecked(entry(path)!.item)).toBe(false);
    expect(entry('00008130-001A2B3C4D5E')!.item.deleteMode).toBe('trash');
    expect(entry('old-exports')!.item.deleteMode).toBe('delete');
    // Picking something in the Trash warns that it is emptied for good.
    expect(store.totals.emptiesTrash).toBe(false);
    store.setItem(entry('old-exports')!.item.id, true);
    expect(store.totals.emptiesTrash).toBe(true);
  });

  it('Names folder-level groups, device backups and Xcode archives', async () => {
    const { pinia } = await context('/cleanup', 'full_disk_access');
    await scanned();
    const card = (key: CleanupCategory) =>
      mount(CleanupCategoryCard, { props: { category: unfold(key) }, global: { plugins: [pinia, i18n] } });
    expect(card('temp').text()).toContain('系统临时文件夹');
    expect(card('trash').text()).toContain('废纸篓里的项目');
    const installers = card('installers');
    expect(installers.text()).toContain('下载和桌面');
    expect(installers.text()).not.toContain('installers');
    expect(installers.text()).toContain('已安装');
    const backups = card('device_backups');
    expect(backups.text()).toContain('iPhone / iPad 备份与 Xcode 归档');
    expect(backups.text()).toContain('iPhone 15 Pro');
    expect(backups.text()).toContain('设备备份 · iPhone16,1');
    expect(backups.text()).toContain('Xcode 归档');
    const projects = card('project_artifacts');
    expect(projects.text()).toContain('~/code/old-shop');
    expect(projects.text()).toContain('最近有改动');
  });
});

describe('Docker (C4)', () => {
  it('Lists what Docker can reclaim as unchecked prune commands, frees it now, and only shows volumes', async () => {
    const { pinia } = await context();
    const store = await scanned();
    const docker = store.report!.categories.find(item => item.key === 'docker')!;
    expect(docker.risk).toBe('review');
    const prunes = store.entries.filter(row => row.category.key === 'docker');
    expect(prunes.map(row => row.item.command)).toEqual([
      'docker image prune --all --force',
      'docker builder prune --all --force',
      'docker container prune --force'
    ]);
    expect(prunes.some(row => store.isChecked(row.item))).toBe(false);
    expect(docker.skipped).toContainEqual(expect.objectContaining({ title: 'Docker', reason: 'volumes' }));
    // A prune frees space right away; nothing goes to the Trash.
    const before = store.totals.nowBytes;
    store.setItem(prunes[0]!.item.id, true);
    expect(store.totals.nowBytes - before).toBe(prunes[0]!.item.sizeBytes);

    unfold('docker');
    const wrapper = mount(CleanupCategoryCard, { props: { category: docker }, global: { plugins: [pinia, i18n] } });
    expect(wrapper.text()).toContain('Docker 可回收空间');
    expect(wrapper.text()).toContain('docker image prune --all --force');
    const images = wrapper.findAll('[title]').find(node => node.text() === '未使用的镜像');
    expect(images?.attributes('title')).toContain('需要时会重新拉取或构建');
    expect(wrapper.text()).toContain('数据卷可能存着数据库等数据，只显示、不清理');
  });

  it('A Docker that is installed but not running is one line asking to open it', async () => {
    const { pinia } = await context('/cleanup', 'cleanup-docker-stopped');
    const store = await scanned();
    expect(store.entries.some(row => row.category.key === 'docker')).toBe(false);
    const docker = store.report!.categories.find(item => item.key === 'docker')!;
    expect(docker.skipped).toEqual([expect.objectContaining({ title: 'Docker', reason: 'tool_stopped', bytes: null })]);
    store.toggleExpanded('docker');
    const wrapper = mount(CleanupCategoryCard, { props: { category: docker }, global: { plugins: [pinia, i18n] } });
    expect(wrapper.text()).toContain('没有运行，打开后重新扫描就能看到可回收空间');
  });
});

describe('Protected list', () => {
  it('Always Keep hides the item right away; undo brings it back with its suggested selection', async () => {
    await context();
    const store = await scanned();
    const spotify = entry('com.spotify.client')!;
    const rule = await store.protect(spotify);
    expect(rule?.value).toBe(spotify.item.path);
    expect(entry('com.spotify.client')).toBeUndefined();
    expect(store.selected[spotify.item.id]).toBeUndefined();
    expect(store.rules).toHaveLength(1);

    await store.unprotect(rule!, spotify);
    expect(entry('com.spotify.client')).toBeDefined();
    expect(store.isChecked(spotify.item)).toBe(true);
    expect(store.rules).toHaveLength(0);
  });
});

describe('Execution', () => {
  it('An expired report is scanned again instead of executed', async () => {
    await context();
    const store = await scanned();
    const execute = vi.spyOn(commands, 'cleanupExecute');
    store.report!.expiresAt = Date.now() - 1;
    void store.confirm();
    await vi.waitFor(() => expect(store.stage).toBe('finishing'), { timeout: 3000 });
    expect(execute).not.toHaveBeenCalled();
    expect(store.rescanned).toBe(true);
  });

  it('A report Rust no longer accepts is scanned again too', async () => {
    await context('/cleanup', 'cleanup-expired');
    const store = await scanned();
    void store.confirm();
    await vi.waitFor(() => expect(store.stage).toBe('finishing'), { timeout: 3000 });
    expect(store.rescanned).toBe(true);
    expect(store.taskId).toBeNull();
  });

  it('Cleans as one task: space freed now and the Trash part are separate, and undo puts files back', async () => {
    const { pinia, router } = await context();
    const store = await scanned();
    const before = store.summary!.reclaimableBytes;
    const { nowBytes, trashBytes } = store.totals;
    await store.confirm();
    expect(store.stage).toBe('running');
    expect(store.task?.op).toBe('clean');

    // The running card names the action, never the report the task targets.
    const card = mount(RunningTask, {
      props: { task: store.task! },
      global: { plugins: [pinia, i18n] }
    });
    expect(card.text()).toContain('正在清理电脑');
    expect(card.text()).not.toContain(store.report!.reportId);
    card.unmount();

    await vi.waitFor(() => expect(store.stage).toBe('done'), { timeout: 5000 });
    expect(store.trashedBytes).toBe(trashBytes);
    expect(store.freedNow).toBe(nowBytes);
    await vi.waitFor(() => expect(store.summary!.reclaimableBytes).toBeLessThan(before));

    const wrapper = mount(CleanupPage, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    expect(text()).toContain(`已释放 ${size(nowBytes)}`);
    expect(text()).toContain(`另有 ${size(trashBytes)} 在废纸篓里`);
    const undo = wrapper.findAll('button').find(button => button.text() === '撤销移到废纸篓的项目');
    expect(undo).toBeDefined();
    await undo!.trigger('click');
    await flushPromises();
    expect(text()).toContain('已撤销，放回原处');
    expect(wrapper.findAll('button').some(button => button.text() === '撤销移到废纸篓的项目')).toBe(false);
    wrapper.unmount();
  });
});

describe('Entry points', () => {
  it('Sidebar shows reclaimable space, or a badge while dead background items still load', async () => {
    const { pinia, router } = await context('/discover');
    const wrapper = mount(AppShell, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    const link = () => wrapper.get('a[href="/cleanup"]');
    expect(link().text()).toBe('清理电脑');

    const store = await scanned();
    await flushPromises();
    expect(link().text()).toBe('清理电脑');
    expect(link().get('[role="img"]').attributes('aria-label')).toBe(
      `${store.activeServices.length} 个失效的后台项目仍在加载`
    );
    store.applySummary({ ...store.summary!, activeBackgroundItems: 0 });
    await flushPromises();
    expect(link().text()).toContain(size(store.summary!.reclaimableBytes));
    wrapper.unmount();
  });

  it('The Installed rail opens Clean Up and starts the first scan', async () => {
    const { pinia, router } = await context('/installed');
    const wrapper = mount(LibraryRail, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    const card = wrapper.get('[data-testid="cleanup-entry"]');
    expect(card.text()).toContain('找出缓存、日志和已删除 App 的残留');
    await card.get('button').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.name).toBe('cleanup');
    expect(useCleanupStore().stage).toBe('scanning');
    wrapper.unmount();
  });

  it('Settings: review files follow the delete mode; folders can be protected and removed', async () => {
    const { pinia, router } = await context('/settings/cleanup');
    const settings = useSettingsStore();
    await vi.waitFor(() => expect(settings.value).toBeTruthy());
    const wrapper = mount(CleanupSettings, {
      global: { plugins: [pinia, router, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    await wrapper
      .findAll('[role="radio"]')
      .find(button => button.text() === '直接删除')!
      .trigger('click');
    await vi.waitFor(() => expect(settings.value?.cleanupDeleteMode).toBe('delete'));

    const store = useCleanupStore();
    await wrapper
      .findAll('button')
      .find(button => button.text() === '添加文件夹…')!
      .trigger('click');
    await vi.waitFor(() => expect(store.rules).toHaveLength(1));
    await flushPromises();
    expect(wrapper.text()).toContain('~/Projects/archive');
    await wrapper
      .findAll('button')
      .find(button => button.text() === '移除')!
      .trigger('click');
    await vi.waitFor(() => expect(store.rules).toHaveLength(0));

    await scanned();
    expect(entry('Application Support/com.bohemiancoding.sketch3')?.item.deleteMode).toBe('delete');
    wrapper.unmount();
  });
});

it.each(['succeeded', 'failed', 'canceled', 'running'] as const)(
  'Keeps early %s events when the queued cleanup response arrives late',
  async state => {
    const contextResult = await context();
    const store = await scanned();
    const queued = { ...contextResult.state.history[0]!, id: 'cleanup-race', state: 'queued' as const };
    vi.spyOn(commands, 'cleanupExecute').mockImplementation(async () => {
      useTasksStore().applyUpdate({ ...queued, state });
      await flushPromises();
      return { status: 'ok', data: queued };
    });
    await store.confirm();
    await flushPromises();
    expect(store.task?.state).toBe(state);
    expect(store.stage).toBe(state === 'running' ? 'running' : 'done');
  }
);

it.each([0, 1])(
  'Keeps undo available after restoring %i items with unresolved paths, then allows retry',
  async restored => {
    const { pinia, state } = await context();
    const store = useCleanupStore();
    store.taskId = 'partial-restore';
    store.stage = 'done';
    useTasksStore().applyUpdate({
      ...state.history[0]!,
      id: store.taskId,
      state: 'succeeded',
      outcome: { freedBytes: 42, trashedBytes: 42, trashed: 2, deleted: 0, skipped: [], restorable: true }
    });
    const restore = vi
      .spyOn(commands, 'uninstallRestore')
      .mockResolvedValueOnce({ status: 'ok', data: { restored, missing: ['/Users/test/conflict.txt'] } })
      .mockResolvedValueOnce({ status: 'ok', data: { restored: 2 - restored, missing: [] } });
    const toast = vi.spyOn(useToasts(), 'push');
    const wrapper = mount(CleanupResult, { global: { plugins: [pinia, i18n] } });
    const undo = () => wrapper.findAll('button').find(button => button.text() === '撤销移到废纸篓的项目');
    await undo()!.trigger('click');
    await flushPromises();
    expect(store.restored).toBe(false);
    expect(undo()).toBeDefined();
    expect(wrapper.text()).not.toContain('已撤销，放回原处');
    expect(toast).toHaveBeenLastCalledWith(
      expect.objectContaining({
        tone: 'warning',
        title: '1 个文件没能放回',
        description: expect.stringContaining('/Users/test/conflict.txt')
      })
    );
    await undo()!.trigger('click');
    await flushPromises();
    expect(restore.mock.calls).toEqual([['partial-restore'], ['partial-restore']]);
    expect(store.restored).toBe(true);
    expect(undo()).toBeUndefined();
    expect(wrapper.text()).toContain('已撤销，放回原处');
    expect(toast).toHaveBeenLastCalledWith(expect.objectContaining({ tone: 'success' }));
    wrapper.unmount();
  }
);
