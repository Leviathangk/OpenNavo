import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import type { LocationQuery } from 'vue-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import UninstallDialog from '@/components/uninstall/UninstallDialog.vue';
import { openDeepLink, useDeepLinkStore } from '@/composables/useDeepLink';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import type { LocalApp, StartupItem } from '@/ipc/bindings';
import InstalledPage from '@/pages/InstalledPage.vue';
import SecurityPage from '@/pages/SecurityPage.vue';
import { startLocalState, useSettingsStore } from '@/stores';
import { useLocalAppsStore } from '@/stores/localApps';
import { useUninstallStore } from '@/stores/uninstall';
import {
  isPreviewLabel,
  matchLocalApps,
  matchStartupItems,
  scanPreview,
  startupPreview,
  uninstallPreview
} from '@/utils/deepLinkPreview';
import { setup } from './helpers';

// Preview deep links (F1, F5): /security/startup?label=…&preview=1, /security/scan?token=…&preview=1 and
// /uninstall/{token}?preview=1 find and preselect the item or app; nothing is switched, scanned or removed
// until the user acts through the page's own buttons and confirmations, and bad links only say so.
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  useToasts().clear();
  vi.restoreAllMocks();
  history.replaceState(null, '', '/');
  localStorage.removeItem('opennavo.pendingDeepLink');
  document.body.innerHTML = '';
});

const text = () => document.body.textContent ?? '';
const notice = () => document.body.querySelector('[data-testid="deeplink-preview"]')?.textContent ?? '';
const bodyButton = (label: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent?.trim() === label);

async function mountSecurity(path: string, flags = '') {
  if (flags) history.replaceState(null, '', `/?mock=${flags}`);
  const context = await setup(path, { tickMs: 10 });
  await startLocalState();
  const wrapper = mount(SecurityPage, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  await flushPromises();
  return { ...context, wrapper };
}

async function mountInstalled(path: string, flags = 'full_disk_access') {
  history.replaceState(null, '', flags ? `/?mock=${flags}` : '/');
  const context = await setup(path, { brew: true, tickMs: 5 });
  await startLocalState();
  const wrapper = mount(InstalledPage, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  wrappers.push(wrapper);
  await flushPromises();
  return { ...context, wrapper };
}

describe('Preview link parameters', () => {
  it('accepts exactly what Rust sends and nothing else', () => {
    expect(startupPreview({})).toEqual({ state: 'none' });
    expect(startupPreview({ label: 'com.docker.vmnetd', preview: '1' })).toEqual({
      state: 'ok',
      value: 'com.docker.vmnetd'
    });
    const refused: LocationQuery[] = [
      { label: 'com.docker.vmnetd' },
      { label: 'com.docker.vmnetd', preview: '0' },
      { label: ['a', 'b'], preview: '1' },
      { label: 'a', preview: '1', execute: '1' },
      { label: 'a/b', preview: '1' },
      { label: ' a', preview: '1' },
      { label: 'a\nb', preview: '1' }
    ];
    for (const query of refused) expect(startupPreview(query)).toEqual({ state: 'invalid' });
    expect(scanPreview({ token: 'example+pro', preview: '1' })).toEqual({ state: 'ok', value: 'example+pro' });
    expect(scanPreview({ token: '../etc', preview: '1' })).toEqual({ state: 'invalid' });
    expect(scanPreview({ token: 'zed', preview: '1', path: '/tmp/x' })).toEqual({ state: 'invalid' });
    expect(uninstallPreview('zoom', { preview: '1' })).toEqual({ state: 'ok', value: 'zoom' });
    expect(uninstallPreview('zoom', {})).toEqual({ state: 'invalid' });
    expect(uninstallPreview('zoom', { preview: '1', delete: '1' })).toEqual({ state: 'invalid' });
    expect(uninstallPreview('Zoom', { preview: '1' })).toEqual({ state: 'invalid' });
    expect(isPreviewLabel('é'.repeat(128))).toBe(false);
  });

  it('matches labels exactly and apps by cask or catalog token only', () => {
    const items = [{ label: 'com.a' }, { label: 'com.a.helper' }, { label: null }] as StartupItem[];
    expect(matchStartupItems(items, 'com.a')).toHaveLength(1);
    const apps = [
      { id: '1', cask: 'zoom', catalogToken: 'zoom' },
      { id: '2', cask: null, catalogToken: 'zoom' },
      { id: '3', cask: null, catalogToken: null }
    ] as LocalApp[];
    expect(matchLocalApps(apps, 'zoom').map(app => app.id)).toEqual(['1', '2']);
    expect(matchLocalApps(apps, 'zoo')).toEqual([]);
  });
});

describe('Startup item previews', () => {
  it('finds the labeled item and opens its details without acting on it', async () => {
    const act = vi.spyOn(commands, 'startupItemAct');
    await mountSecurity('/security/startup?label=com.docker.vmnetd&preview=1');
    await vi.waitFor(() => expect(notice()).toContain('找到 1 个标签为 com.docker.vmnetd 的启动项'));
    expect(notice()).toContain('还没有做任何改动');
    // The one match is preselected: its details sheet is open, with its own action buttons.
    expect(text()).toContain('/Library/LaunchDaemons/com.docker.vmnetd.plist');
    expect(bodyButton('停用')).toBeTruthy();
    expect(act).not.toHaveBeenCalled();
    expect(await unwrap(commands.quarantineList())).toHaveLength(0);
  });

  it('says so when no startup item has the label', async () => {
    await mountSecurity('/security/startup?label=com.unknown.agent&preview=1');
    await vi.waitFor(() => expect(notice()).toContain('这台 Mac 上没有标签为 com.unknown.agent 的启动项'));
    expect(text()).not.toContain('/Library/LaunchDaemons/com.docker.vmnetd.plist');
  });

  it('refuses a link Rust would not have built', async () => {
    const act = vi.spyOn(commands, 'startupItemAct');
    await mountSecurity('/security/startup?label=com.docker.vmnetd&preview=1&execute=1');
    expect(notice()).toContain('这个链接打不开');
    expect(text()).not.toContain('/Library/LaunchDaemons/com.docker.vmnetd.plist');
    expect(act).not.toHaveBeenCalled();
  });
});

describe('Scan previews', () => {
  it('offers a scan of the app on this Mac and starts only on click', async () => {
    const start = vi.spyOn(commands, 'avScanStart');
    const { wrapper } = await mountSecurity('/security/scan?token=docker-desktop&preview=1', 'av-ready');
    await vi.waitFor(() => expect(notice()).toContain('扫描这个 App'));
    const app = useLocalAppsStore().findCask('docker-desktop')!;
    expect(notice()).toContain(app.path);
    expect(start).not.toHaveBeenCalled();
    await wrapper
      .findAll('button')
      .find(item => item.text() === '扫描这个 App')!
      .trigger('click');
    await flushPromises();
    expect(start).toHaveBeenCalledExactlyOnceWith('custom', [app.path]);
  });

  it('says the app is not here, and never scans', async () => {
    const start = vi.spyOn(commands, 'avScanStart');
    await mountSecurity('/security/scan?token=not-installed&preview=1', 'av-ready');
    await vi.waitFor(() => expect(notice()).toContain('这台 Mac 上没有找到 not-installed'));
    expect(start).not.toHaveBeenCalled();
  });
});

describe('Uninstall previews', () => {
  it('opens the app’s info sheet; the dialog still reviews and waits for confirmation', async () => {
    const execute = vi.spyOn(commands, 'uninstallExecute');
    const { pinia, router } = await mountInstalled('/uninstall/zoom?preview=1');
    await vi.waitFor(() => expect(notice()).toContain('预览卸载'));
    expect(notice()).toContain('还没有删除任何东西');
    expect(text()).toContain('/Applications/zoom.us.app');
    expect(useUninstallStore().stage).toBe('idle');

    mount(UninstallDialog, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
    bodyButton('卸载')!.click();
    await vi.waitFor(() => expect(useUninstallStore().stage).toBe('review'), { timeout: 3000 });
    expect(useUninstallStore().targets.map(app => app.id)).toEqual(['local-zoom']);
    expect(execute).not.toHaveBeenCalled();
  });

  it('says so for an app that is not here or a malformed link', async () => {
    const plan = vi.spyOn(commands, 'uninstallPlan');
    await mountInstalled('/uninstall/not-installed?preview=1');
    await vi.waitFor(() => expect(notice()).toContain('这台 Mac 上没有找到 not-installed'));
    wrappers.splice(0).forEach(wrapper => wrapper.unmount());
    document.body.innerHTML = '';
    await mountInstalled('/uninstall/zoom');
    expect(notice()).toContain('这个链接打不开');
    expect(plan).not.toHaveBeenCalled();
  });

  it('waits for Full Disk Access before it can show the app', async () => {
    await mountInstalled('/uninstall/zoom?preview=1', '');
    await vi.waitFor(() => expect(notice()).toContain('开启「完全磁盘访问权限」之后，才能预览卸载 zoom'));
  });
});

describe('Deep link routing', () => {
  it('navigates to preview routes without queueing anything, and keeps them across onboarding', async () => {
    const context = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    for (const route of [
      '/security/startup?label=com.example.shared&preview=1',
      '/security/scan?token=example%2Bpro&preview=1',
      '/uninstall/example?preview=1'
    ]) {
      await openDeepLink(context.router, { url: '', route, action: null });
      expect(context.router.currentRoute.value.fullPath).toBe(route);
    }
    expect(context.router.currentRoute.value.name).toBe('uninstall-preview');
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);

    // A link deferred by onboarding survives a restart only in the shape Rust writes.
    const kept = {
      url: 'opennavo://startup?label=com.example.shared',
      route: '/security/startup?label=com.example.shared&preview=1',
      action: null
    };
    localStorage.setItem('opennavo.pendingDeepLink', JSON.stringify(kept));
    useDeepLinkStore().$dispose();
    await setup('/welcome');
    expect(useDeepLinkStore().pending).toEqual(kept);
    const forged = { ...kept, route: '/security/startup?label=a&preview=1&execute=1' };
    localStorage.setItem('opennavo.pendingDeepLink', JSON.stringify(forged));
    useDeepLinkStore().$dispose();
    await setup('/welcome');
    expect(useDeepLinkStore().pending).toBeNull();
  });
});
