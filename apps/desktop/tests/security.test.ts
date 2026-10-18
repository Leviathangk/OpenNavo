import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { watch } from 'vue';
import type { Component } from 'vue';
import { OnMenu } from '@opennavo/ui';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import { events } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import PasteGuard from '@/components/security/PasteGuard.vue';
import SecurityNotifier from '@/components/security/SecurityNotifier.vue';
import SecurityProtectionPanel from '@/components/security/SecurityProtectionPanel.vue';
import SecuritySettings from '@/components/settings/SecuritySettings.vue';
import HistoryList from '@/components/updates/HistoryList.vue';
import SecurityPage from '@/pages/SecurityPage.vue';
import { startLocalState, usePermissionsStore, useSettingsStore, useTasksStore } from '@/stores';
import { useSecurityStore } from '@/stores/security';
import { useDownloadsStore } from '@/stores/downloads';
import DownloadNotifier from '@/components/package/DownloadNotifier.vue';
import { describeVerdict } from '@/components/package/downloadVerdict';
import type { DownloadVerdict, InstallScanResult } from '@/ipc/bindings';
import { setup } from './helpers';
import type { TestContext } from './helpers';

// Security (store/security plan §6): consent before the database download, scans that find the EICAR test file,
// quarantine, and startup items with Apple items hidden and removal that can be restored.
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  useToasts().clear();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  document.body.innerHTML = '';
});

const TICK = 15;
const wait = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

async function mountPage(path: string, flags = '') {
  if (flags) history.replaceState(null, '', `/?mock=${flags}`);
  const context = await setup(path, { tickMs: TICK });
  await startLocalState();
  const wrapper = mount(SecurityPage, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  await flushPromises();
  await flushPromises();
  return { ...context, wrapper };
}

const button = (wrapper: VueWrapper, text: string) => wrapper.findAll('button').find(item => item.text() === text);
/** The folded list of everything that is not an app's startup item. */
const others = (wrapper: VueWrapper) => wrapper.findAll('button').find(item => /^其他 \d+ 项/.test(item.text()))!;

/** Another component in the same mock app, such as the shell's notifier or the settings section. */
function mountIn(context: TestContext, component: Component) {
  const wrapper = mount(component, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  return wrapper;
}

const lastToast = () => useToasts().items.value.at(-1);
const bodyButton = (text: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === text);

describe('Security overview', () => {
  it('Leads with what needs attention: Gatekeeper is off and a new startup item waits', async () => {
    const { wrapper } = await mountPage('/security');
    expect(wrapper.find('h2').text()).toBe('有 2 项需要你看一下');
    expect(wrapper.text()).toContain('Gatekeeper 已关闭');
    expect(wrapper.text()).toContain('新增 1 个开机启动项');
    // Without the database the main action sets it up instead of scanning.
    expect(button(wrapper, '下载病毒库')).toBeTruthy();
    expect(useSecurityStore().attention).toBe(1);
  });
});

describe('Real-machine findings', () => {
  it('Names the sharing services that are on, and says none are when Rust reports info', async () => {
    const { wrapper } = await mountPage('/security');
    expect(wrapper.text()).toContain('已开启：屏幕共享');
    wrappers.splice(0).forEach(item => item.unmount());

    const real = commands.securityOverview;
    vi.spyOn(commands, 'securityOverview').mockImplementation(async () => {
      const result = await real();
      if (result.status !== 'ok') return result;
      const posture = result.data.posture.map(check =>
        check.id === 'sharing'
          ? { ...check, state: 'info' as const, facts: { 'com.apple.screensharing': false, 'com.openssh.sshd': false } }
          : check
      );
      return { ...result, data: { ...result.data, posture } };
    });
    const off = await mountPage('/security');
    expect(off.wrapper.text()).toContain('没有开启远程共享服务');
    expect(off.wrapper.text()).not.toContain('已开启远程共享服务');
  });

  it('Lists the most serious signature problems first', async () => {
    const { wrapper } = await mountPage('/security');
    const real = commands.appSignatureCheck;
    vi.spyOn(commands, 'appSignatureCheck').mockImplementation(async () => {
      const result = await real();
      return result.status === 'ok'
        ? { ...result, data: { ...result.data, apps: [...result.data.apps].reverse() } }
        : result;
    });
    await button(wrapper, '检查签名')!.trigger('click');
    await flushPromises();
    const rows = wrapper.findAll('section[aria-labelledby="security-signatures"] li').map(item => item.text());
    expect(rows[0]).toContain('Lumen');
    expect(rows.at(-1)).toContain('Side Project');
  });

  it('Reads startup items once at a time, so change notices cannot stack scans', async () => {
    await mountPage('/security');
    const scan = vi.spyOn(commands, 'startupScan');
    const store = useSecurityStore();
    await Promise.all([store.loadStartup(), store.loadStartup()]);
    expect(scan).toHaveBeenCalledOnce();
  });
});

describe('Before the first startup read', () => {
  it('Says startup items have not been read yet instead of counting zero', async () => {
    // The first read can take a minute or more on a real Mac; until it lands there is no baseline.
    const real = commands.securityOverview;
    vi.spyOn(commands, 'securityOverview').mockImplementation(async () => {
      const result = await real();
      if (result.status !== 'ok') return result;
      const startup = {
        ...result.data.startup,
        total: 0,
        thirdParty: 0,
        untrusted: 0,
        pendingReview: 0,
        changedAt: null
      };
      return { ...result, data: { ...result.data, startup } };
    });
    const { wrapper } = await mountPage('/security');
    expect(wrapper.text()).toContain('还没有读取过启动项');
    expect(wrapper.text()).not.toContain('0 个第三方启动项');
  });
});

describe('Virus scan', () => {
  it('Asks before downloading the database, then a quick scan finds and quarantines the EICAR test file', async () => {
    const { wrapper } = await mountPage('/security/scan');
    expect(wrapper.text()).toContain('先下载官方病毒库');
    expect(wrapper.text()).toContain('113 MB');
    const quick = () => button(wrapper, '快速扫描');
    expect(quick()?.attributes('disabled')).toBeDefined();

    await button(wrapper, '同意并下载')?.trigger('click');
    await flushPromises();
    await wait(TICK * 6);
    await flushPromises();
    expect(wrapper.text()).not.toContain('先下载官方病毒库');
    expect(quick()?.attributes('disabled')).toBeUndefined();

    await quick()?.trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('正在快速扫描');
    await wait(TICK * 9);
    await flushPromises();
    expect(wrapper.text()).toContain('扫描完成');
    expect(wrapper.text()).toContain('Eicar-Test-Signature');

    await button(wrapper, '隔离')?.trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('已隔离');
    const quarantine = await unwrap(commands.quarantineList());
    expect(quarantine.map(entry => entry.name)).toContain('eicar.com');

    // Restored from the quarantine, the threat is back where it was: the scan on screen offers to quarantine it again
    // rather than still saying it is quarantined (found on the real Mac, 2026-10-16).
    const store = useSecurityStore();
    await store.restore([quarantine.find(entry => entry.name === 'eicar.com')!.id]);
    await flushPromises();
    expect(wrapper.text()).not.toContain('已隔离');
    expect(button(wrapper, '隔离')).toBeTruthy();
    await button(wrapper, '隔离')?.trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('已隔离');
    const again = (await unwrap(commands.quarantineList())).find(entry => entry.name === 'eicar.com')!;
    await store.purge([again.id]);
    await flushPromises();
    expect(wrapper.text()).toContain('已删除');
  });
});

describe('Downloads watch', () => {
  it('A threat in any finished download says so and scans just that file to deal with it', async () => {
    // Any file, not only installers (plan §6, S1); found on the real Mac with an EICAR test file in Downloads.
    const context = await setup('/security/overview', { tickMs: TICK });
    await startLocalState();
    mountIn(context, DownloadNotifier);
    await flushPromises();
    const path = '/Users/me/Downloads/opennavo-eicar-watch.com';
    useDownloadsStore().verified({
      path,
      sha256: '275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f',
      size: 68,
      format: 'other',
      signature: { status: 'not_code', teamId: null, signer: null, notarized: null },
      teamMatches: null,
      avSkipped: null,
      av: { clean: false, signature: 'Eicar-Test-Signature', scannedAt: Date.now() },
      quarantined: false
    });
    await flushPromises();
    const toasts = useToasts();
    const toast = toasts.items.value.find(item => item.actionLabel === '扫描并处理');
    expect(toast?.title).toBe('下载的文件有威胁：opennavo-eicar-watch.com');
    expect(toast?.description).toBe('Eicar-Test-Signature · 扫描这个文件后可以把它移到隔离区');
    const scan = vi.spyOn(commands, 'avScanStart');
    toasts.runAction(toast!.id);
    await flushPromises();
    await flushPromises();
    expect(context.router.currentRoute.value.path).toBe('/security/scan');
    expect(scan).toHaveBeenCalledWith('custom', [path]);
  });
});

describe('Background scans', () => {
  it('A scheduled scan takes over the finished scan on screen, with its progress and result', async () => {
    // Found on the real Mac: the scheduled scan ran while the panel kept showing an older custom scan.
    const { wrapper } = await mountPage('/security/scan', 'av-ready');
    await button(wrapper, '快速扫描')!.trigger('click');
    await flushPromises();
    await vi.waitFor(() => expect(useSecurityStore().scan?.state).toBe('done'));
    await flushPromises();
    expect(wrapper.text()).toContain('扫描完成');
    const shown = useSecurityStore().scan!.scanId;
    // What the schedule does in Rust: start a quick scan without the page asking for it.
    const { scanId } = await unwrap(commands.avScanStart('quick', null));
    expect(scanId).not.toBe(shown);
    await vi.waitFor(() => expect(useSecurityStore().scan?.scanId).toBe(scanId));
    await flushPromises();
    expect(useSecurityStore().scan?.scanId).toBe(scanId);
    expect(wrapper.text()).toContain('正在快速扫描');
    await vi.waitFor(() => expect(useSecurityStore().scan?.state).toBe('done'));
    await flushPromises();
    expect(useSecurityStore().scan?.scanId).toBe(scanId);
    expect(wrapper.text()).toContain('扫描完成');
  });
});

describe('Scan failures', () => {
  it('Stops an old mock scan before installing a new session', async () => {
    vi.useFakeTimers();
    try {
      history.replaceState(null, '', '/?mock=av-ready');
      await setup('/security/scan', { tickMs: TICK });
      await unwrap(commands.avScanStart('quick', null));
      await setup('/security/scan', { tickMs: TICK });
      const finished = vi.fn();
      const unlisten = await events.avFinished.listen(finished);
      try {
        await vi.advanceTimersByTimeAsync(TICK * 20);
        expect(finished).not.toHaveBeenCalled();
      } finally {
        unlisten();
      }
    } finally {
      vi.useRealTimers();
    }
  });

  it('Names the folders macOS refused and offers the privacy setting', async () => {
    const { wrapper } = await mountPage('/security/scan', 'av-ready,av-denied-downloads');
    await button(wrapper, '快速扫描')!.trigger('click');
    await flushPromises();
    await wait(TICK * 2);
    await flushPromises();
    const note = wrapper.get('[role="status"]');
    expect(note.text()).toContain('没有权限读取：下载');
    expect(note.find('button').text()).toBe('打开设置');
  });

  it('Says why a scan stopped when the engine did not start', async () => {
    const { wrapper } = await mountPage('/security/scan', 'av-ready,av-engine-fail');
    await button(wrapper, '快速扫描')!.trigger('click');
    await flushPromises();
    await wait(TICK * 2);
    await flushPromises();
    expect(wrapper.text()).toContain('扫描中断');
    expect(wrapper.get('[role="alert"]').text()).toBe('病毒扫描引擎没有正常启动，请稍后重试');
  });
});

describe('Startup items', () => {
  it('Lists the new unsigned daemon first, hides Apple items, and removes into quarantine after confirming', async () => {
    const { wrapper } = await mountPage('/security/startup');
    const names = () => wrapper.findAll('li button').map(item => item.text());
    expect(names()[0]).toBe('com.helper.updater');
    expect(names()).not.toContain('com.apple.metadata.mds');

    await wrapper.find('li button').trigger('click');
    await flushPromises();
    expect(document.body.textContent).toContain('临时签名，无法确认开发者');
    const remove = [...document.body.querySelectorAll('button')].find(item => item.textContent?.trim() === '移除');
    remove?.click();
    await flushPromises();
    // The confirmation explains the administrator prompt for system-wide items.
    expect(document.body.textContent).toContain('需要输入一次管理员密码');
    const confirm = [...document.body.querySelectorAll('[role="alertdialog"] button, [role="dialog"] button')].find(
      item => item.textContent?.trim() === '移除'
    ) as HTMLButtonElement | undefined;
    confirm?.click();
    await flushPromises();
    await flushPromises();
    expect(names()).not.toContain('com.helper.updater');
    const quarantine = await unwrap(commands.quarantineList());
    expect(quarantine.some(entry => entry.reason.kind === 'startup')).toBe(true);
  });

  it('Lists apps with what each starts; macOS items are left out and the rest waits under Other', async () => {
    const { wrapper } = await mountPage('/security/startup');
    const titles = () => wrapper.findAll('li > div > div > p > button').map(item => item.text());
    // The new unsigned daemon belongs to no app but leads anyway; then apps that start something, then those that
    // do not.
    expect(titles()).toEqual([
      'com.helper.updater',
      'Adobe Photoshop 2026',
      'Docker',
      'Example Remote',
      'Raycast',
      'Google Chrome',
      'Visual Studio Code'
    ]);
    expect(wrapper.text()).not.toContain('com.apple.metadata.mds');
    // Extensions and plugins load into other programs: they wait under Other with tools no app owns.
    expect(wrapper.text()).not.toContain('uBlock Origin Lite');
    await others(wrapper).trigger('click');
    for (const name of ['homebrew.mxcl.postgresql@16', '~/bin/sync.sh', 'uBlock Origin Lite', 'Example VPN'])
      expect(wrapper.text()).toContain(name);
  });

  it("Switches all of an app's startup items at once, or one at a time", async () => {
    const { wrapper } = await mountPage('/security/startup');
    const row = () => wrapper.findAll('li').find(item => item.text().includes('Docker'))!;
    const act = vi.spyOn(commands, 'startupItemAct');
    // One switch for the app: every item in one request, so system items ask for the password once.
    await row().get('[role="switch"][aria-label="启用 Docker"]').trigger('click');
    await flushPromises();
    await flushPromises();
    expect(act).toHaveBeenCalledTimes(1);
    expect(act.mock.calls[0]).toEqual([['launch:system:com.docker.vmnetd', 'btm:4.com.docker.helper'], 'disable']);
    expect(row().text()).toContain('已停用');
    await row()
      .findAll('button')
      .find(item => item.text() === '2 项')!
      .trigger('click');
    expect(row().findAll('[role="switch"]')).toHaveLength(3);
    await row().get('[role="switch"][aria-label="启用 DockerHelper"]').trigger('click');
    await flushPromises();
    await flushPromises();
    expect(act).toHaveBeenLastCalledWith(['btm:4.com.docker.helper'], 'enable');
    expect(row().text()).toContain('1 项已启用');
    // An app that only registered itself to open at login, then was switched off, switches back on here.
    const code = wrapper.findAll('li').find(item => item.text().includes('Visual Studio Code'))!;
    expect(code.text()).toContain('登录项');
    await code.get('[role="switch"]').trigger('click');
    await flushPromises();
    await flushPromises();
    expect(act).toHaveBeenLastCalledWith(['login:/Applications/Visual Studio Code.app'], 'enable');
  });

  it('Adds an app to open at login and lists it at once', async () => {
    const { wrapper } = await mountPage('/security/startup');
    await button(wrapper, '添加开机启动项')!.trigger('click');
    await flushPromises();
    await flushPromises();
    const sheet = () => document.body.querySelector('[role="dialog"]')!;
    const offered = () => [...sheet().querySelectorAll('li button')].map(item => item.textContent ?? '');
    // Apps that already open at login are not offered again.
    expect(offered().some(text => text.includes('Spotify'))).toBe(true);
    expect(offered().some(text => text.includes('Raycast'))).toBe(false);
    [...sheet().querySelectorAll<HTMLButtonElement>('li button')]
      .find(item => item.textContent?.includes('Spotify'))!
      .click();
    await flushPromises();
    await flushPromises();
    expect(lastToast()?.title).toBe('已添加，Spotify 会在登录时打开');
    const row = wrapper.findAll('li').find(item => item.text().includes('Spotify'))!;
    expect(row.text()).toContain('登录项');
    expect(row.text()).toContain('已启用');
    expect(row.classes()).not.toContain('bg-status-warning-subtle');
  });

  it('Shows the last list at once while a new scan runs', async () => {
    vi.spyOn(commands, 'startupScan').mockReturnValue(
      new Promise(() => undefined) as ReturnType<typeof commands.startupScan>
    );
    const { wrapper } = await mountPage('/security/startup');
    expect(wrapper.text()).toContain('正在检查启动项…');
    expect(wrapper.text()).toContain('Docker');
  });

  it.each([false, true])('Finishes switches without waiting for overview probes (scan pending: %s)', async scanning => {
    const { wrapper } = await mountPage('/security/startup');
    const store = useSecurityStore();
    const oldScan = structuredClone(await commands.startupScan());
    let finishScan!: (value: Awaited<ReturnType<typeof commands.startupScan>>) => void;
    let pendingScan: Promise<void> | undefined;
    if (scanning) {
      vi.spyOn(commands, 'startupScan').mockReturnValueOnce(new Promise(resolve => (finishScan = resolve)));
      pendingScan = store.loadStartup();
      await flushPromises();
    }
    const overview = await commands.securityOverview();
    let finishOverview!: (value: typeof overview) => void;
    vi.spyOn(commands, 'securityOverview').mockReturnValue(new Promise(resolve => (finishOverview = resolve)));
    const toggle = () => wrapper.get('[role="switch"][aria-label="启用 Docker"]');
    try {
      for (const checked of ['false', 'true']) {
        await toggle().trigger('click');
        await flushPromises();
        await flushPromises();
        expect(toggle().attributes('aria-checked')).toBe(checked);
        expect(toggle().attributes('disabled')).toBeUndefined();
        expect(store.busy.size).toBe(0);
        expect(store.startupRefreshing).toBe(scanning);
      }
      // Leave a different state from the old scan so its delayed response must not undo the action.
      await toggle().trigger('click');
      await flushPromises();
      await flushPromises();
      if (scanning) {
        finishScan(oldScan);
        await pendingScan;
        await flushPromises();
      }
      expect(toggle().attributes('aria-checked')).toBe('false');
      expect(toggle().attributes('disabled')).toBeUndefined();
    } finally {
      finishOverview(overview);
      if (scanning) finishScan(oldScan);
      await pendingScan;
      await flushPromises();
    }
  });

  it('Grades signatures: changed after signing is a warning, configuration files make no signing claim', async () => {
    const { wrapper } = await mountPage('/security/startup');
    const row = (name: string) => wrapper.findAll('li').find(item => item.text().includes(name));
    await others(wrapper).trigger('click');
    const modified = row('com.example.remote.helper');
    expect(modified?.text()).toContain('签名有效，但签名后有文件被改动');
    expect(modified?.find('.text-status-warning').exists()).toBe(true);
    expect(modified?.find('.text-status-danger').exists()).toBe(false);
    const config = row('.zshrc');
    expect(config?.text()).toContain('不是程序文件，不适用签名');
    expect(config?.text()).not.toContain('没有签名');
    // Only the login item pane manages these; a shell file has nowhere in System Settings to go.
    expect(config?.text()).not.toContain('在系统设置中管理');
  });

  it('Reports incomplete categories and offers access when Full Disk Access is denied', async () => {
    const { wrapper } = await mountPage('/security/startup', 'no-fda');
    expect(wrapper.text()).toContain('未能完整检查：后台项目');
    expect(wrapper.text()).toContain('后台项目');
    expect(button(wrapper, '开启权限')).toBeTruthy();
    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    await button(wrapper, '开启权限')!.trigger('click');
    expect(guide).toHaveBeenCalledWith('full_disk_access');
  });

  it.each(['granted', 'unknown', 'pending'] as const)(
    'Does not blame incomplete Dock plugin reads on Full Disk Access when permission is %s',
    async state => {
      const { wrapper } = await mountPage('/security/startup');
      const permissions = usePermissionsStore();
      permissions.apply({
        ...permissions.status!,
        fullDiskAccess: state === 'pending' ? 'denied' : state,
        fullDiskAccessPending: state === 'pending',
        relaunchRequired: state === 'pending'
      });
      const security = useSecurityStore();
      security.startup = { ...security.startup!, missing: ['dock_tiles'] };
      await flushPromises();
      expect(wrapper.text()).toContain('未能完整检查：程序坞插件');
      expect(wrapper.text()).not.toContain('没有「完全磁盘访问权限」');
      expect(button(wrapper, '开启权限')).toBeUndefined();
    }
  );
});

describe('Protection', () => {
  it('Download scanning waits for the database; features that need Apple approval say so', async () => {
    const { wrapper } = await mountPage('/security/protection');
    expect(wrapper.text()).toContain('需要先下载病毒库');
    expect(wrapper.text()).toContain('S3 · 等待 Apple 授权');
    const toggles = wrapper.findAll('[role="switch"]');
    expect(toggles.length).toBeGreaterThan(2);
  });

  it('Paste protection switches on here and asks for Accessibility only when the user clicks', async () => {
    const { wrapper } = await mountPage('/security/protection');
    const request = vi.spyOn(commands, 'pasteProtectionRequestAccess');
    // No longer a "coming later" row: it is a live S2 switch.
    expect(wrapper.text()).not.toContain('需要辅助功能权限，后续版本提供');
    await wrapper.get('[role="switch"][aria-label="粘贴命令保护"]').trigger('click');
    await flushPromises();
    await flushPromises();
    expect(useSecurityStore().securitySettings.pasteProtection).toBe(true);
    expect(request).not.toHaveBeenCalled();
    await button(wrapper, '开启权限')!.trigger('click');
    await flushPromises();
    expect(request).toHaveBeenCalledOnce();
    expect(button(wrapper, '开启权限')).toBeUndefined();
  });
});

describe('Scheduled scans and signature check-up (S2)', () => {
  it('Schedules a weekly quick scan, then says when it runs next and why the last one was skipped', async () => {
    const context = await setup('/settings/security', { tickMs: TICK });
    await startLocalState();
    const wrapper = mountIn(context, SecuritySettings);
    await flushPromises();
    expect(wrapper.text()).toContain('只在 OpenNavo 运行时进行，不会唤醒 Mac');
    // The boundary of the scan before installing is stated where it is switched.
    expect(wrapper.text()).toContain('安装过程中额外下载的内容');
    await wrapper
      .findAll('button[role="radio"]')
      .find(item => item.text() === '每周')!
      .trigger('click');
    await flushPromises();
    await flushPromises();
    expect(useSecurityStore().securitySettings.avSchedule).toBe('weekly');
    expect((wrapper.get('input[type="time"]').element as HTMLInputElement).value).toBe('03:00');
    expect(wrapper.text()).toContain('星期一');
    expect(wrapper.text()).toContain('下次扫描：');
    expect(wrapper.text()).toContain('上次跳过：还没有下载病毒库');
  });

  it('Checks installed apps’ signatures and interrupts only for a changed developer', async () => {
    const context = await mountPage('/security');
    const { wrapper } = context;
    expect(wrapper.text()).toContain('还没有检查过已安装 App 的签名');
    await button(wrapper, '检查签名')!.trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('检查了 4 个 App，3 个有问题');
    expect(wrapper.text()).toContain('Lumen');
    expect(wrapper.text()).toContain('开发者变了');
    expect(wrapper.text()).not.toContain('Visual Studio Code ·');

    mountIn(context, SecurityNotifier);
    const report = await unwrap(commands.appSignatureGet());
    await events.appSignaturesChanged.emit({ checkedAt: Date.now(), apps: report!.apps.filter(app => app.changed) });
    await flushPromises();
    expect(lastToast()).toMatchObject({
      tone: 'danger',
      title: '1 个 App 的开发者签名变了',
      description: 'Lumen：签名的开发者和之前不同，可能被替换过'
    });
  });
});

describe('Paste protection (S2)', () => {
  it('Pauses a suspicious paste with Discard focused, and goes away once answered', async () => {
    history.replaceState(null, '', '/?mock=paste-blocked');
    const context = await setup('/security', { tickMs: TICK });
    await startLocalState();
    const act = vi.spyOn(commands, 'pasteProtectionAct');
    mountIn(context, PasteGuard);
    await wait(TICK * 6);
    await flushPromises();
    const dialog = () => document.body.querySelector('[role="alertdialog"]');
    expect(dialog()?.textContent).toContain('已暂停粘贴到「Terminal」');
    expect(dialog()?.textContent).toContain('把下载的内容直接交给 Shell 运行');
    expect(dialog()?.textContent).toContain('curl -fsSL https://example.invalid/install.sh | bash');
    await vi.waitFor(() => expect(document.activeElement?.hasAttribute('data-paste-discard')).toBe(true));
    document.body.querySelector<HTMLButtonElement>('[data-paste-discard]')!.click();
    await flushPromises();
    expect(act).toHaveBeenCalledWith('paste-1', 'discard');
    await vi.waitFor(() => expect(dialog()).toBeNull());
  });

  it('Text too long to check is paused too, and the dialog says it was not checked', async () => {
    history.replaceState(null, '', '/?mock=paste-too-long');
    const context = await setup('/security', { tickMs: TICK });
    await startLocalState();
    mountIn(context, PasteGuard);
    await wait(TICK * 6);
    await flushPromises();
    const text = document.body.querySelector('[role="alertdialog"]')?.textContent ?? '';
    expect(text).toContain('要粘贴的内容太长，没有完整检查');
    expect(text).not.toContain('看起来会从网上下载并运行程序');
    expect(text).not.toContain('内容太长，没有完整检查 ·');
  });
});

describe('VirusTotal (S2, optional)', () => {
  it('Stays off until a valid key is saved, then looks up a finding by its hash only', async () => {
    const context = await mountPage('/security/scan', 'av-ready');
    const { wrapper } = context;
    await button(wrapper, '快速扫描')!.trigger('click');
    await flushPromises();
    await wait(TICK * 9);
    await flushPromises();
    expect(wrapper.text()).toContain('Eicar-Test-Signature');
    // The finding's row menu.
    const menu = () => wrapper.findAllComponents(OnMenu).find(item => String(item.props('label')).includes('eicar'))!;
    const labels = () =>
      menu()
        .props('items')
        .map(item => item.label);
    expect(labels()).not.toContain('在 VirusTotal 查询');

    const settings = mountIn(context, SecuritySettings);
    await flushPromises();
    expect(settings.text()).toContain('只限非商业的个人使用');
    await settings.get('[role="switch"][aria-labelledby="setting-virusTotalEnabled"]').trigger('click');
    await flushPromises();
    await flushPromises();
    const key = settings.get('input[type="password"]');
    await key.setValue('not-a-key');
    await settings.get('form').trigger('submit');
    await flushPromises();
    expect(lastToast()?.description).toBe('VirusTotal API key 无效，请检查后重新填写');
    await settings.get('input[type="password"]').setValue('a'.repeat(64));
    await settings.get('form').trigger('submit');
    await flushPromises();
    expect(settings.text()).toContain('已保存在钥匙串里');

    const lookup = vi.spyOn(commands, 'virusTotalLookup');
    expect(labels()).toContain('在 VirusTotal 查询');
    menu().vm.$emit('select', 'virusTotal');
    await flushPromises();
    await flushPromises();
    expect(lookup).toHaveBeenCalledWith('275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f');
    expect(lastToast()).toMatchObject({ tone: 'danger', title: 'VirusTotal：62 个引擎报毒', actionLabel: '查看报告' });
    expect(lastToast()?.description).toContain('共 70 个引擎检测');
  });
});

describe('Restoring removed startup items (S2)', () => {
  it('Removes a crontab line with a backup and restores it in place, outside the quarantine', async () => {
    const { wrapper } = await mountPage('/security/startup');
    const row = () => wrapper.findAll('li').find(item => item.text().includes('~/bin/sync.sh'));
    await others(wrapper).trigger('click');
    await row()!.get('button').trigger('click');
    await flushPromises();
    bodyButton('移除')!.click();
    await flushPromises();
    expect(document.body.textContent).toContain('会先备份再移除，之后可以在启动项列表里恢复。');
    const confirm = [...document.body.querySelectorAll<HTMLButtonElement>('[role="alertdialog"] button')].find(
      item => item.textContent?.trim() === '移除'
    );
    confirm!.click();
    await flushPromises();
    await flushPromises();
    expect(lastToast()?.title).toBe('已移除 0 * * * * ~/bin/sync.sh，可以在这里恢复');
    expect(row()?.text()).toContain('已移除');
    expect(await unwrap(commands.quarantineList())).toHaveLength(0);

    await row()!
      .findAll('button')
      .find(item => item.text() === '恢复')!
      .trigger('click');
    await flushPromises();
    await flushPromises();
    expect(lastToast()?.title).toBe('已恢复 0 * * * * ~/bin/sync.sh');
    expect(row()?.text()).toContain('已启用');
  });
});

describe('Scan before installing (S2)', () => {
  async function upgradeWget(flags: string, options: { consent?: boolean; enabled?: boolean; revoke?: boolean } = {}) {
    history.replaceState(null, '', `/?mock=${flags}`);
    const context = await setup('/updates', { tickMs: 5 });
    await startLocalState();
    const settings = useSettingsStore();
    if (options.consent !== undefined) await settings.update('avConsent', options.consent);
    if (options.enabled !== undefined) await settings.update('avScanBeforeInstall', options.enabled);
    if (options.revoke) await settings.update('avConsent', false);
    mountIn(context, SecurityNotifier);
    const tasks = useTasksStore();
    const results: InstallScanResult[] = [];
    const stopResults = await events.installScanned.listen(event => results.push(event.payload));
    // Every phase the running task passes through, as the task card shows it, and what the progress rings show.
    const phases = new Set<string>();
    const rings: { phase: string | null; percent: number | null; shown: number }[] = [];
    const stop = watch(
      () => tasks.running && { ...tasks.running },
      task => {
        if (!task) return;
        if (task.phase) phases.add(task.phase);
        rings.push({ phase: task.phase, percent: task.percent, shown: tasks.shownPercent(task) });
      }
    );
    await tasks.enqueue('upgrade', { kind: 'formula', token: 'wget' });
    await vi.waitFor(() => expect(tasks.finished.some(task => task.target?.token === 'wget')).toBe(true), {
      timeout: 3000
    });
    stop();
    await flushPromises();
    stopResults();
    const list = mount(HistoryList, {
      props: { tasks: tasks.finished },
      global: { plugins: [context.pinia, context.router, i18n] }
    });
    wrappers.push(list);
    await flushPromises();
    const task = tasks.finished.find(entry => entry.target?.token === 'wget')!;
    const scan = await unwrap(commands.installScanGet(task.id));
    return { context, list, phases, rings, task, scan, results };
  }

  it.each(['av-ready,install-threat', 'install-threat'])(
    'Does not scan or block an install without consent (%s)',
    async flags => {
      const result = await upgradeWget(flags);
      expect(result.task.state).toBe('succeeded');
      expect(result.phases.has('scanning')).toBe(false);
      expect(result.scan).toMatchObject({ state: 'skipped', avSkipped: 'no_consent', filesScanned: 0, threats: [] });
      expect(result.results).toEqual([result.scan]);
      expect(useSecurityStore().installScan).toEqual(result.scan);
      expect((await unwrap(commands.protectionEvents(50))).some(event => event.kind === 'install_threat')).toBe(false);
      expect(useToasts().items.value.some(toast => toast.title === '安装包里发现威胁，已停止安装')).toBe(false);
      expect(result.list.text()).not.toContain('安装前扫描');
    }
  );

  it('Reports a missing database after explicit consent and emits the saved skipped result', async () => {
    const result = await upgradeWget('install-threat', { consent: true });
    expect(result.task.state).toBe('succeeded');
    expect(result.phases.has('scanning')).toBe(false);
    expect(result.scan).toMatchObject({ state: 'skipped', avSkipped: 'no_database', filesScanned: 0, threats: [] });
    expect(result.results).toEqual([result.scan]);
  });

  it.each([false, true])('Disabled scans take precedence over consent (%s) and database readiness', async consent => {
    const result = await upgradeWget('av-ready,install-threat', { consent, enabled: false });
    expect(result.task.state).toBe('succeeded');
    expect(result.phases.has('scanning')).toBe(false);
    expect(result.scan).toMatchObject({ state: 'disabled', avSkipped: null, filesScanned: 0, threats: [] });
    expect(result.results).toEqual([result.scan]);
    expect((await unwrap(commands.protectionEvents(50))).some(event => event.kind === 'install_threat')).toBe(false);
  });

  it('Revoking consent keeps the database but skips later installation scans', async () => {
    const result = await upgradeWget('av-ready,install-threat', { consent: true, revoke: true });
    expect((await unwrap(commands.avEngineStatus())).db).not.toBeNull();
    expect((await unwrap(commands.settingsGet())).avConsent).toBe(false);
    expect(result.task.state).toBe('succeeded');
    expect(result.phases.has('scanning')).toBe(false);
    expect(result.scan).toMatchObject({ state: 'skipped', avSkipped: 'no_consent', filesScanned: 0, threats: [] });
    expect(result.results).toEqual([result.scan]);
    expect((await unwrap(commands.protectionEvents(50))).some(event => event.kind === 'install_threat')).toBe(false);
  });

  it('Stops an update whose download has a threat and says what was found', async () => {
    const { context, list, task, scan, results } = await upgradeWget('av-ready,install-threat', { consent: true });
    expect(scan).toMatchObject({
      state: 'threat',
      avSkipped: null,
      filesScanned: 3,
      threats: ['Eicar-Test-Signature']
    });
    expect(results).toEqual([scan]);
    expect(task.state).toBe('failed');
    expect(task.error?.code).toBe('E_INSTALL_THREAT');
    // It stopped while scanning what Homebrew downloaded, before installing anything.
    expect(task.phase).toBe('scanning');
    expect(useToasts().items.value.some(toast => toast.title === '安装包里发现威胁，已停止安装')).toBe(true);
    expect(list.text()).toContain('安装包里发现威胁，已停止安装');
    expect(list.text()).toContain('安装前扫描发现：Eicar-Test-Signature');
    // The protection record names the stopped task, not its id.
    const panel = mountIn(context, SecurityProtectionPanel);
    await flushPromises();
    expect(panel.text()).toContain('安装包有威胁，已停止安装：wget');
  });

  it('A clean scan is noted on the finished update; without the database nothing is claimed', async () => {
    const clean = await upgradeWget('av-ready', { consent: true });
    expect(clean.scan).toMatchObject({ state: 'clean', avSkipped: null, filesScanned: 3, threats: [] });
    expect(clean.results).toEqual([clean.scan]);
    expect(clean.task.state).toBe('succeeded');
    expect([...clean.phases]).toEqual(expect.arrayContaining(['downloading', 'scanning', 'installing']));
    // The scan has no measurable progress: the card goes indeterminate, while rings hold the downloaded share.
    const scanning = clean.rings.filter(ring => ring.phase === 'scanning');
    expect(scanning.length).toBeGreaterThan(0);
    expect(scanning.every(ring => ring.percent === null && ring.shown === 70)).toBe(true);
    const shown = clean.rings.map(ring => ring.shown);
    expect(shown).toEqual([...shown].sort((a, b) => a - b));
    expect(clean.list.text()).toContain('安装前扫描：检查了 3 个文件，没有发现威胁');
    wrappers.splice(0).forEach(wrapper => wrapper.unmount());

    const unscanned = await upgradeWget('');
    expect(unscanned.task.state).toBe('succeeded');
    expect(unscanned.phases.has('scanning')).toBe(false);
    expect(unscanned.list.text()).not.toContain('安装前扫描');
  });
});

describe('Download verdicts for the new signature states', () => {
  const t = (key: string) => key;
  const verdict = (signature: Partial<DownloadVerdict['signature']>, format: DownloadVerdict['format'] = 'dmg') =>
    ({
      path: '/Users/mock/Downloads/x',
      sha256: 'a'.repeat(64),
      size: 1,
      format,
      signature: { status: 'developer_id', teamId: 'ABCDE12345', signer: 'Dev', notarized: true, ...signature },
      teamMatches: true,
      av: { clean: true, signature: null, scannedAt: 0 },
      quarantined: false,
      avSkipped: null
    }) as DownloadVerdict;

  it('Treats a download changed after signing as dangerous, like an invalid signature', () => {
    const result = describeVerdict(verdict({ status: 'modified' }), t);
    expect(result.tone).toBe('danger');
    expect(result.parts[0]).toBe('package.link.verdict.modified');
  });

  it('Explains an unopened ZIP instead of calling it unsigned, and flags an unchecked signature', () => {
    const zip = describeVerdict(verdict({ status: 'not_code', teamId: null, signer: null, notarized: null }, 'zip'), t);
    expect(zip.parts[0]).toBe('package.link.verdict.zip');
    expect(zip.tone).toBe('success');
    const unknown = describeVerdict(verdict({ status: 'unknown', notarized: null }), t);
    expect(unknown.parts[0]).toBe('security.signature.unknown');
    expect(unknown.tone).toBe('warning');
  });
});
