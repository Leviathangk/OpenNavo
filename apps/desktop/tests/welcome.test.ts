import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { formatMilliseconds } from '@opennavo/shared';
import WelcomePage from '@/pages/WelcomePage.vue';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import type { Locale } from '@/ipc/bindings';
import { startLocalState, usePermissionsStore, useSettingsStore } from '@/stores';
import { setup } from './helpers';

// First-launch welcome (06 §13.1, 08 §10.13, owner revision 2026-10-07): two download-source cards, official and other mirrors.
// Select the lowest-latency source by default and recommend only that source; install from the selected source. App Management and
// Full Disk Access are both required (owner decision 2026-10-12).
const remote = vi.hoisted(() => ({ config: null as unknown }));
vi.mock('@/composables/useClientConfig', () => ({
  fetchClientConfig: async () => {
    if (!remote.config) throw new Error('offline');
    return remote.config;
  }
}));

const TUNA = 'https://mirrors.tuna.tsinghua.edu.cn';
const USTC = 'https://mirrors.ustc.edu.cn';
const MIRRORS = [
  {
    key: 'official',
    name: '官方源',
    probeUrl: 'https://formulae.brew.sh/api/formula.jws.json',
    recommended: false,
    apiDomain: null,
    bottleDomain: null,
    brewGitRemote: null,
    coreGitRemote: null
  },
  {
    key: 'tuna',
    name: '清华 TUNA',
    probeUrl: `${TUNA}/homebrew-bottles/api/formula.jws.json`,
    recommended: true,
    apiDomain: `${TUNA}/homebrew-bottles/api`,
    bottleDomain: `${TUNA}/homebrew-bottles`,
    brewGitRemote: `${TUNA}/git/homebrew/brew.git`,
    coreGitRemote: `${TUNA}/git/homebrew/homebrew-core.git`
  },
  {
    key: 'ustc',
    name: '中科大 USTC',
    probeUrl: `${USTC}/homebrew-bottles/api/formula.jws.json`,
    recommended: false,
    apiDomain: `${USTC}/homebrew-bottles/api`,
    bottleDomain: `${USTC}/homebrew-bottles`,
    brewGitRemote: `${USTC}/brew.git`,
    coreGitRemote: `${USTC}/homebrew-core.git`
  }
];

async function open(options: { brew?: boolean; flags?: string; systemLocale?: Locale; locale?: Locale } = {}) {
  // Simulation switches come from the URL and must be set before installing simulated IPC.
  const params = new URLSearchParams({ 'mock-system-locale': options.systemLocale ?? 'zh-CN' });
  if (options.flags) params.set('mock', options.flags);
  window.history.replaceState(null, '', `/?${params}`);
  const context = await setup('/welcome', { brew: options.brew ?? false, tickMs: 5 });
  await startLocalState();
  if (options.locale) {
    await useSettingsStore().setLanguage(options.locale);
    i18n.global.locale.value = options.locale;
  }
  const wrapper = mount(WelcomePage, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  return { ...context, wrapper };
}

const radios = () => [...document.body.querySelectorAll<HTMLButtonElement>('[role="radio"]')];
const checked = () => radios().find(radio => radio.getAttribute('aria-checked') === 'true')?.textContent ?? '';
const radio = (name: string) => radios().find(item => item.textContent?.includes(name));
const button = (text: string) =>
  [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(
    item => item.textContent?.trim() === text || item.getAttribute('aria-label') === text
  );
const start = () => button('开始使用');

/** Click Enable App Management and wait for it to be enabled in simulated System Settings. */
async function grantAppManagement() {
  button('开启 App 管理')?.click();
  await vi.waitFor(() => expect(usePermissionsStore().appManagement).toBe('granted'), {
    timeout: 3000
  });
  await flushPromises();
}
/** Same for Full Disk Access. */
async function grantFullDiskAccess() {
  button('开启完全磁盘访问权限')?.click();
  await vi.waitFor(() => expect(usePermissionsStore().fullDiskAccess).toBe('granted'), {
    timeout: 3000
  });
  await flushPromises();
}
const text = () => document.body.textContent ?? '';

beforeEach(() => {
  remote.config = { mirrors: MIRRORS };
  // Reduced motion shows environment-check rows immediately instead of animating them.
  window.matchMedia = (query: string) =>
    ({
      matches: query.includes('reduce'),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {}
    }) as unknown as MediaQueryList;
});

afterEach(async () => {
  // Cancel tasks still advancing in the simulated queue so their events cannot leak into the next case's IPC simulation.
  for (const task of await unwrap(commands.taskListActive())) await unwrap(commands.taskCancel(task.id));
  document.body.innerHTML = '';
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('Welcome without Homebrew', () => {
  it.each(['en-US', 'ja-JP', 'es-ES', 'pt-BR', 'ru-RU'] as const)(
    '%s computers only show, probe and install from official, even with Chinese UI',
    async systemLocale => {
      const probe = vi.spyOn(commands, 'mirrorProbe');
      await open({ systemLocale });
      expect(radios()).toHaveLength(1);
      expect(checked()).toContain('官方源安装');
      expect(probe).toHaveBeenCalled();
      expect(probe.mock.calls.every(([inputs]) => inputs.length === 1 && inputs[0]?.key === 'official')).toBe(true);
      const enqueue = vi.spyOn(commands, 'taskEnqueue');
      button('安装 Homebrew')?.click();
      await flushPromises();
      expect(enqueue).toHaveBeenCalledWith('install_homebrew', null, {}, 'manual');
      expect(useSettingsStore().value?.mirror.key).toBe('official');
    }
  );

  it('Chinese computers retain mirrors after switching the UI to English', async () => {
    await open({ systemLocale: 'zh-CN', locale: 'en-US' });
    expect(radios()).toHaveLength(2);
    expect(checked()).toContain('Other Mirror');
  });

  it('Non-Chinese computers do not show mirror-list failures', async () => {
    remote.config = null;
    await open({ systemLocale: 'en-US' });
    expect(radios()).toHaveLength(1);
    expect(checked()).toContain('官方源安装');
    expect(text()).not.toContain('暂时拿不到镜像列表');
  });

  it('Non-Chinese computers do not offer mirrors after official downloads fail', async () => {
    await open({ systemLocale: 'en-US', flags: 'brew-install-fail-download' });
    button('安装 Homebrew')?.click();
    await vi.waitFor(() => expect(text()).toContain('安装脚本下载失败'), { timeout: 3000 });
    expect(button('改用清华 TUNA')).toBeUndefined();
    expect(radios()).toHaveLength(1);
    expect(useSettingsStore().value?.mirror.key).toBe('official');
  });

  it('Official left, fastest mirror right; select/recommend only lowest latency', async () => {
    await open();
    expect(radios().map(item => item.textContent)).toEqual([
      expect.stringContaining('官方源安装'),
      expect.stringContaining('其他镜像安装')
    ]);
    // Simulated probes: official 324 ms; mirrors 27 and 31 ms by position; Tsinghua TUNA is fastest.
    expect(radio('官方源安装')?.textContent).toContain('formulae.brew.sh');
    expect(radio('官方源安装')?.textContent).toContain(formatMilliseconds(324, { locale: 'zh-CN' }));
    expect(radio('其他镜像安装')?.textContent).toContain('清华 TUNA');
    expect(radio('其他镜像安装')?.textContent).toContain(formatMilliseconds(27, { locale: 'zh-CN' }));
    expect(checked()).toContain('其他镜像安装');
    expect(radio('其他镜像安装')?.textContent).toContain('推荐');
    expect(radio('官方源安装')?.textContent).not.toContain('推荐');
    expect(text()).not.toContain('国内镜像');
    expect(text()).toContain('Xcode 命令行工具');
    expect(text()).toContain('未找到，/opt/homebrew 为空');
  });

  it('Faster official source is selected and solely recommended', async () => {
    await open({ flags: 'slow-mirrors' });
    expect(checked()).toContain('官方源安装');
    expect(radio('官方源安装')?.textContent).toContain('推荐');
    expect(radio('其他镜像安装')?.textContent).not.toContain('推荐');
  });

  it('Persist complete selected mirror URLs before enqueuing installation', async () => {
    await open();
    const save = vi.spyOn(commands, 'settingsSet');
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    expect(save).not.toHaveBeenCalled();
    button('安装 Homebrew')?.click();
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0]?.[0].mirror).toEqual({
      key: 'tuna',
      apiDomain: `${TUNA}/homebrew-bottles/api`,
      bottleDomain: `${TUNA}/homebrew-bottles`,
      brewGitRemote: `${TUNA}/git/homebrew/brew.git`,
      coreGitRemote: `${TUNA}/git/homebrew/homebrew-core.git`
    });
    expect(enqueue).toHaveBeenCalledWith('install_homebrew', null, {}, 'manual');
    expect(save.mock.invocationCallOrder[0]).toBeLessThan(enqueue.mock.invocationCallOrder[0] ?? 0);
  });

  it('Selecting already-configured official source avoids settings writes', async () => {
    await open();
    radio('官方源安装')?.click();
    await flushPromises();
    expect(checked()).toContain('官方源安装');
    const save = vi.spyOn(commands, 'settingsSet');
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    button('安装 Homebrew')?.click();
    await flushPromises();
    expect(save).not.toHaveBeenCalled();
    expect(enqueue).toHaveBeenCalledWith('install_homebrew', null, {}, 'manual');
  });

  it('Failed settings saves do not enqueue', async () => {
    await open();
    vi.spyOn(commands, 'settingsSet').mockResolvedValueOnce({
      status: 'error',
      error: { code: 'E_UNKNOWN', message: 'failed', detail: null }
    });
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    button('安装 Homebrew')?.click();
    await flushPromises();
    expect(enqueue).not.toHaveBeenCalled();
  });

  it('Unavailable mirrors disable the right card; install from official without silent switching', async () => {
    remote.config = null;
    await open();
    expect(radio('其他镜像安装')?.disabled).toBe(true);
    expect(checked()).toContain('官方源安装');
    expect(text()).toContain('暂时拿不到镜像列表');
    const save = vi.spyOn(commands, 'settingsSet');
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    button('安装 Homebrew')?.click();
    await flushPromises();
    // Settings already use the official source, so no write is needed.
    expect(save).not.toHaveBeenCalled();
    expect(enqueue).toHaveBeenCalledWith('install_homebrew', null, {}, 'manual');
    expect(useSettingsStore().value?.mirror.key).toBe('official');
  });

  it('Failures show reason/logs; reinstall clears the failure panel', async () => {
    await open({ flags: 'brew-install-fail-sudo' });
    button('安装 Homebrew')?.click();
    await vi.waitFor(() => expect(text()).toContain('没有输入管理员密码，安装已取消。'), {
      timeout: 3000
    });
    expect(text()).toContain('Homebrew 没有装好');

    button('查看日志')?.click();
    await flushPromises();
    expect(text()).toContain('sudo: a password is required');
    button('关闭')?.click();
    await flushPromises();

    button('安装 Homebrew')?.click();
    await flushPromises();
    expect(text()).not.toContain('Homebrew 没有装好');
  });

  it('Official download failures offer switching to reachable mirrors', async () => {
    await open({ flags: 'slow-mirrors,brew-install-fail-download' });
    expect(checked()).toContain('官方源安装');
    button('安装 Homebrew')?.click();
    await vi.waitFor(() => expect(text()).toContain('安装脚本下载失败'), { timeout: 3000 });
    button('改用清华 TUNA')?.click();
    await flushPromises();
    expect(checked()).toContain('其他镜像安装');
  });

  it('Permissions can be enabled before install; success shows Get Started', async () => {
    await open();
    expect(text()).toContain('允许 OpenNavo 管理 App');
    expect(text()).toContain('完全磁盘访问权限');
    expect(button('开启 App 管理')).toBeDefined();
    button('安装 Homebrew')?.click();
    await vi.waitFor(() => expect(start()).toBeDefined(), { timeout: 3000 });
    expect(button('安装 Homebrew')).toBeUndefined();
  });

  it('Without Homebrew offer neither Skip nor app entry', async () => {
    const { router } = await open();
    expect(button('跳过，先随便看看')).toBeUndefined();
    expect(start()).toBeUndefined();
    expect(button('安装 Homebrew')).toBeDefined();
    expect(useSettingsStore().value?.onboardingCompleted).toBe(false);
    expect(router.currentRoute.value.path).toBe('/welcome');
  });
});

describe('Welcome with Homebrew', () => {
  it('Require App Management and Full Disk Access, open guidance, save source/onboarding, then enter Discover', async () => {
    const { router } = await open({ brew: true, flags: 'first-run' });
    const replace = vi.spyOn(router, 'replace');
    expect(text()).toContain('不读取终端里设置的镜像变量');
    expect(button('安装 Homebrew')).toBeUndefined();
    expect(start()?.disabled).toBe(true);
    expect(text()).toContain('开启「App 管理」和「完全磁盘访问权限」后即可开始使用。');

    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    await grantAppManagement();
    expect(guide).toHaveBeenCalledWith('app_management');
    // App Management alone is no longer enough.
    expect(start()?.disabled).toBe(true);
    await grantFullDiskAccess();
    expect(guide).toHaveBeenCalledWith('full_disk_access');
    expect(start()?.disabled).toBe(false);
    expect(text()).not.toContain('后即可开始使用。');

    start()?.click();
    await flushPromises();
    const settings = useSettingsStore().value;
    expect(settings?.mirror).toMatchObject({
      key: 'tuna',
      brewGitRemote: `${TUNA}/git/homebrew/brew.git`
    });
    expect(settings?.onboardingCompleted).toBe(true);
    expect(router.currentRoute.value.path).toBe('/discover');
    expect(replace).toHaveBeenCalledWith('/discover');
  });

  it('Full Disk Access is required as well and shows Enabled after granting', async () => {
    await open({ brew: true });
    expect(usePermissionsStore().appManagement).toBe('granted');
    expect(usePermissionsStore().fullDiskAccess).toBe('denied');
    expect(text().match(/必需/g)?.length).toBe(2);
    expect(text()).not.toContain('可选');
    expect(start()?.disabled).toBe(true);
    const guide = vi.spyOn(commands, 'permissionGuideOpen');
    await grantFullDiskAccess();
    expect(guide).toHaveBeenCalledWith('full_disk_access');
    expect(button('开启完全磁盘访问权限')).toBeUndefined();
    expect(text().match(/已开启/g)?.length).toBe(2);
    expect(start()?.disabled).toBe(false);
  });

  it('Failed onboarding saves stay on Welcome with retryable errors', async () => {
    const { router } = await open({ brew: true, flags: 'first-run' });
    await grantAppManagement();
    await grantFullDiskAccess();
    const update = vi.spyOn(useSettingsStore(), 'update').mockRejectedValueOnce(new Error('save_failed'));
    start()?.click();
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/welcome');
    expect(useSettingsStore().value?.onboardingCompleted).toBe(false);
    expect(document.body.querySelector('[role="alert"]')).not.toBeNull();
    update.mockRestore();
    start()?.click();
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/discover');
  });

  it('Unknown system detection allows explicit I enabled it confirmation, covering both permissions', async () => {
    await open({ brew: true, flags: 'first-run,no-permission-check' });
    expect(start()?.disabled).toBe(true);
    button('我已开启')?.click();
    await flushPromises();
    expect(start()?.disabled).toBe(false);
    // The confirmation also lets Installed start without asking again.
    expect(usePermissionsStore().fullDiskReady).toBe(true);
  });

  it('Permission requiring relaunch shows notice and Reopen', async () => {
    await open({ brew: true, flags: 'first-run,permission-relaunch' });
    button('开启 App 管理')?.click();
    await vi.waitFor(() => expect(text()).toContain('重新打开 OpenNavo 后生效'), { timeout: 3000 });
    expect(button('重新打开')).toBeDefined();
  });
});
