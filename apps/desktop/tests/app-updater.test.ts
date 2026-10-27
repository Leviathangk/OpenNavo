import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent } from 'vue';
import { flushPromises, mount } from '@vue/test-utils';
import { check } from '@tauri-apps/plugin-updater';
import type { DownloadEvent, Update } from '@tauri-apps/plugin-updater';
import { relaunch } from '@tauri-apps/plugin-process';
import { useAppUpdaterStore } from '@/stores/appUpdater';
import { useSettingsStore } from '@/stores/settings';
import { useTasksStore } from '@/stores/tasks';
import { useAppUpdateChecks } from '@/composables/useAppUpdateChecks';
import AppUpdateDialog from '@/components/shell/AppUpdateDialog.vue';
import { i18n } from '@/i18n';
import { api } from '@/api';
import { commands } from '@/ipc/client';
import AppUpdater from '@/components/settings/AppUpdater.vue';
import { setup } from './helpers';

vi.mock('@tauri-apps/plugin-updater', () => ({ check: vi.fn() }));
vi.mock('@tauri-apps/plugin-process', () => ({ relaunch: vi.fn() }));

function release(version = '0.1.4') {
  return {
    version,
    currentVersion: '0.1.3',
    body: '- Improvements',
    close: vi.fn().mockResolvedValue(undefined),
    downloadAndInstall: vi.fn().mockResolvedValue(undefined)
  } as unknown as Update;
}

beforeEach(async () => {
  vi.clearAllMocks();
  localStorage.clear();
  await setup();
  await useSettingsStore().load();
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 9, 9, 12));
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
  vi.mocked(check).mockResolvedValue(null);
});
afterEach(() => {
  vi.unstubAllEnvs();
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('OpenNavo automatic self-update checks', () => {
  it('Shows checking immediately while the initial native app-info request is pending', async () => {
    let finish!: (info: Awaited<ReturnType<typeof commands.appInfo>>) => void;
    vi.spyOn(commands, 'appInfo').mockReturnValueOnce(
      new Promise(resolve => {
        finish = resolve;
      })
    );
    const updater = useAppUpdaterStore();
    const pending = updater.checkNow();
    expect(updater.state).toBe('checking');
    expect(updater.working).toBe(true);
    finish({ status: 'ok', data: { version: '0.1.3', os: 'macos', arch: 'aarch64' } });
    await pending;
    expect(updater.state).toBe('latest');
  });

  it('A manual check joining an automatic check still opens a previously dismissed update', async () => {
    const updater = useAppUpdaterStore();
    vi.mocked(check).mockResolvedValue(release());
    await updater.checkNow(true);
    updater.dismiss();
    await vi.advanceTimersByTimeAsync(3_600_000);
    let finish!: (update: Update | null) => void;
    vi.mocked(check).mockReturnValueOnce(
      new Promise(resolve => {
        finish = resolve;
      })
    );
    const automatic = updater.checkNow(true);
    await flushPromises();
    const manual = updater.checkNow();
    finish(release());
    await Promise.all([automatic, manual]);
    expect(check).toHaveBeenCalledTimes(2);
    expect(updater.dialogOpen).toBe(true);
  });

  it.each(['hidden-window', 'hidden-dialog', 'hidden-parent'])(
    'Opens a cached update without a network wait when there is a %s',
    async scenario => {
      const updater = useAppUpdaterStore();
      updater.update = release();
      updater.state = 'available';
      const blocker = document.createElement('div');
      blocker.setAttribute('role', 'dialog');
      const parent = document.createElement('div');
      parent.append(blocker);
      if (scenario === 'hidden-dialog') blocker.hidden = true;
      if (scenario === 'hidden-parent') parent.style.display = 'none';
      if (scenario === 'hidden-window') vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
      else document.body.append(parent);
      const get = vi.spyOn(api, 'GET').mockReturnValue(new Promise(() => {}));
      const dialog = mount(AppUpdateDialog, { attachTo: document.body, global: { plugins: [i18n] } });
      const row = mount(AppUpdater, { props: { currentVersion: '0.1.3' }, global: { plugins: [i18n] } });
      try {
        await row.get('button').trigger('click');
        await flushPromises();
        expect(document.querySelector('.on-app-update-dialog')).not.toBeNull();
        expect(get).toHaveBeenCalledTimes(1);
        expect(check).not.toHaveBeenCalled();
      } finally {
        row.unmount();
        dialog.unmount();
        parent.remove();
        document.body.innerHTML = '';
      }
    }
  );

  it('Releases a postponed prompt as soon as another dialog becomes hidden without being removed', async () => {
    const updater = useAppUpdaterStore();
    updater.update = release();
    updater.state = 'available';
    updater.show();
    const blocker = document.createElement('div');
    blocker.setAttribute('role', 'alertdialog');
    document.body.append(blocker);
    vi.spyOn(api, 'GET').mockReturnValue(new Promise(() => {}));
    const dialog = mount(AppUpdateDialog, { attachTo: document.body, global: { plugins: [i18n] } });
    try {
      await flushPromises();
      expect(document.querySelector('.on-app-update-dialog')).toBeNull();
      blocker.hidden = true;
      await flushPromises();
      expect(document.querySelector('.on-app-update-dialog')).not.toBeNull();
    } finally {
      dialog.unmount();
      blocker.remove();
      document.body.innerHTML = '';
    }
  });

  it('The Settings restart action starts native restart immediately and shows pending feedback', async () => {
    const updater = useAppUpdaterStore();
    updater.update = release();
    updater.state = 'installed';
    let finish!: () => void;
    vi.mocked(relaunch).mockReturnValueOnce(
      new Promise(resolve => {
        finish = resolve;
      })
    );
    const row = mount(AppUpdater, { props: { currentVersion: '0.1.3' }, global: { plugins: [i18n] } });
    try {
      await row.get('button').trigger('click');
      expect(relaunch).toHaveBeenCalledTimes(1);
      expect(row.get('button').attributes('aria-busy')).toBe('true');
      expect(row.get('button').attributes('disabled')).toBeDefined();
      expect(updater.dialogOpen).toBe(false);
      await row.get('button').trigger('click');
      expect(relaunch).toHaveBeenCalledTimes(1);
      finish();
      await flushPromises();
      expect(row.get('button').attributes('aria-busy')).toBeUndefined();
    } finally {
      row.unmount();
    }
  });

  it('Deduplicates restart requests and allows retry after the native process rejects one', async () => {
    const updater = useAppUpdaterStore();
    updater.update = release();
    updater.state = 'installed';
    let fail!: (error: Error) => void;
    vi.mocked(relaunch).mockReturnValueOnce(
      new Promise((_resolve, reject) => {
        fail = reject;
      })
    );
    const first = updater.restart();
    const rejected = expect(first).rejects.toThrow('active_tasks');
    const duplicate = updater.restart();
    expect(relaunch).toHaveBeenCalledTimes(1);
    fail(new Error('active_tasks'));
    await rejected;
    await duplicate;
    expect(updater.state).toBe('installed');
    await updater.restart();
    expect(relaunch).toHaveBeenCalledTimes(2);
  });

  it('Simulates a failed download and allows a successful retry without native updates', async () => {
    vi.stubEnv('VITE_OPENNAVO_UPDATE_PREVIEW', '1');
    const updater = useAppUpdaterStore();
    const update = release();
    updater.update = update;
    updater.state = 'available';
    updater.show();
    await updater.install(true);
    await vi.advanceTimersByTimeAsync(4000);
    expect(updater.state).toBe('available');
    expect(updater.installFailed).toBe(true);
    expect(updater.dialogOpen).toBe(true);
    await updater.install();
    expect(updater.installFailed).toBe(false);
    await vi.advanceTimersByTimeAsync(8000);
    expect(updater.state).toBe('installed');
    expect(update.downloadAndInstall).not.toHaveBeenCalled();
    updater.$dispose();
  });

  it('Simulates preview progress without native installation or restart', async () => {
    vi.stubEnv('VITE_OPENNAVO_UPDATE_PREVIEW', '1');
    const updater = useAppUpdaterStore();
    const update = release();
    updater.update = update;
    updater.state = 'available';
    updater.show();
    await updater.install();
    expect(updater.state).toBe('downloading');
    updater.dismiss();
    expect(updater.dialogOpen).toBe(true);
    await vi.advanceTimersByTimeAsync(4000);
    expect(updater.percent).toBe(50);
    await vi.advanceTimersByTimeAsync(4000);
    expect(updater.state).toBe('installed');
    expect(updater.percent).toBe(100);
    expect(update.downloadAndInstall).not.toHaveBeenCalled();
    await updater.restart();
    expect(relaunch).not.toHaveBeenCalled();
    expect(updater.dialogOpen).toBe(false);
    expect(updater.state).toBe('available');
    updater.$dispose();
  });

  it('Defaults on, delays startup for 30 seconds, and removes timers and listeners on unmount', async () => {
    expect(useSettingsStore().value?.autoCheckAppUpdates).toBe(true);
    const Host = defineComponent({
      setup: () => {
        useAppUpdateChecks();
        return () => null;
      }
    });
    const wrapper = mount(Host);
    await vi.advanceTimersByTimeAsync(29_999);
    expect(check).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(check).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(3_599_999);
    expect(check).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(check).toHaveBeenCalledTimes(2);
    wrapper.unmount();
    vi.setSystemTime(new Date(2026, 9, 10, 12));
    window.dispatchEvent(new Event('focus'));
    await vi.advanceTimersByTimeAsync(3_600_000);
    expect(check).toHaveBeenCalledTimes(2);
  });

  it('Persists hourly checks across launches and checks again after one hour on the same date', async () => {
    const updater = useAppUpdaterStore();
    await updater.checkNow(true);
    await updater.checkNow(true);
    expect(check).toHaveBeenCalledTimes(1);
    setActivePinia(createPinia());
    await useSettingsStore().load();
    await useAppUpdaterStore().checkNow(true);
    expect(check).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(3_599_999);
    await useAppUpdaterStore().checkNow(true);
    expect(check).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await useAppUpdaterStore().checkNow(true);
    expect(check).toHaveBeenCalledTimes(2);
  });

  it('Measures the hourly interval from request start rather than response completion', async () => {
    const updater = useAppUpdaterStore();
    vi.mocked(check).mockImplementationOnce(() => new Promise(resolve => setTimeout(() => resolve(null), 5000)));
    const pending = updater.checkNow(true);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(5000);
    await pending;
    await vi.advanceTimersByTimeAsync(3_595_000);
    await updater.checkNow(true);
    expect(check).toHaveBeenCalledTimes(2);
  });

  it('Prompts for a newly published version at the next hourly check without repeating a dismissed version', async () => {
    const updater = useAppUpdaterStore();
    await updater.checkNow(true);
    expect(updater.dialogOpen).toBe(false);
    vi.mocked(check).mockResolvedValue(release('0.1.4'));
    await vi.advanceTimersByTimeAsync(3_600_000);
    await updater.checkNow(true);
    expect(updater.dialogOpen).toBe(true);
    updater.dismiss();
    await vi.advanceTimersByTimeAsync(3_600_000);
    await updater.checkNow(true);
    expect(check).toHaveBeenCalledTimes(3);
    expect(updater.dialogOpen).toBe(false);
    vi.mocked(check).mockResolvedValue(release('0.1.5'));
    await vi.advanceTimersByTimeAsync(3_600_000);
    await updater.checkNow(true);
    expect(updater.update?.version).toBe('0.1.5');
    expect(updater.dialogOpen).toBe(true);
  });

  it('Honors opt-out while allowing manual checks and deduplicates in-flight requests', async () => {
    const updater = useAppUpdaterStore();
    useSettingsStore().value!.autoCheckAppUpdates = false;
    await updater.checkNow(true);
    expect(check).not.toHaveBeenCalled();
    let finish!: (update: Update | null) => void;
    vi.mocked(check).mockReturnValue(
      new Promise(resolve => {
        finish = resolve;
      })
    );
    const first = updater.checkNow();
    const second = updater.checkNow();
    await flushPromises();
    expect(check).toHaveBeenCalledTimes(1);
    finish(release());
    await Promise.all([first, second]);
    expect(updater.dialogOpen).toBe(true);
  });

  it('Retries automatic failures after one hour and keeps them silent', async () => {
    const updater = useAppUpdaterStore();
    vi.mocked(check).mockRejectedValueOnce(new Error('offline'));
    await updater.checkNow(true);
    expect(updater.state).toBe('idle');
    expect(updater.dialogOpen).toBe(false);
    await vi.advanceTimersByTimeAsync(3_599_999);
    await updater.checkNow(true);
    expect(check).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    await updater.checkNow(true);
    expect(check).toHaveBeenCalledTimes(2);
  });

  it('Does not prompt twice for a dismissed version that day but allows manual reopening', async () => {
    const updater = useAppUpdaterStore();
    vi.mocked(check).mockResolvedValue(release());
    await updater.checkNow(true);
    expect(updater.dialogOpen).toBe(true);
    updater.dismiss();
    await updater.checkNow(true);
    expect(updater.dialogOpen).toBe(false);
    updater.show();
    expect(updater.dialogOpen).toBe(true);
    updater.dismiss();
    vi.setSystemTime(new Date(2026, 9, 10, 12));
    await updater.checkNow(true);
    expect(updater.dialogOpen).toBe(true);
  });

  it.each([true, false])(
    'Closes an open dialog when a subsequent check finds no update (automatic=%s)',
    async automatic => {
      const updater = useAppUpdaterStore();
      const update = release();
      vi.mocked(check).mockResolvedValueOnce(update).mockResolvedValueOnce(null);
      await updater.checkNow(true);
      expect(updater.dialogOpen).toBe(true);
      expect(updater.update).toBe(update);

      vi.setSystemTime(new Date(2026, 9, 10, 12));
      await updater.checkNow(automatic);
      expect(check).toHaveBeenCalledTimes(2);
      expect(updater.state).toBe('latest');
      expect(updater.update).toBeNull();
      expect(updater.dialogOpen).toBe(false);
      expect(update.close).toHaveBeenCalledTimes(1);
      updater.show();
      expect(updater.dialogOpen).toBe(false);
    }
  );

  it('Suppresses prompts if disabled during a request and waits until the app is visible to check', async () => {
    const updater = useAppUpdaterStore();
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
    await updater.checkNow(true);
    expect(check).not.toHaveBeenCalled();
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible');
    vi.mocked(check).mockImplementationOnce(async () => {
      useSettingsStore().value!.autoCheckAppUpdates = false;
      return release();
    });
    await updater.checkNow(true);
    expect(updater.state).toBe('available');
    expect(updater.dialogOpen).toBe(false);
  });

  it('Downloads only on explicit install, tracks progress, retries failures, and protects restart with active tasks', async () => {
    const updater = useAppUpdaterStore();
    const update = release();
    vi.mocked(check).mockResolvedValue(update);
    await updater.checkNow(true);
    expect(update.downloadAndInstall).not.toHaveBeenCalled();
    vi.mocked(update.downloadAndInstall).mockRejectedValueOnce(new Error('download failed'));
    await updater.install();
    expect(updater.state).toBe('available');
    expect(updater.installFailed).toBe(true);
    vi.mocked(update.downloadAndInstall).mockImplementationOnce(async callback => {
      callback?.({ event: 'Started', data: { contentLength: 100 } } as DownloadEvent);
      callback?.({ event: 'Progress', data: { chunkLength: 40 } } as DownloadEvent);
      expect(updater.percent).toBe(40);
      updater.dismiss();
      expect(updater.dialogOpen).toBe(true);
      callback?.({ event: 'Finished' });
    });
    await updater.install();
    expect(updater.state).toBe('installed');
    expect(updater.percent).toBe(100);
    expect(relaunch).not.toHaveBeenCalled();
    useTasksStore().active = [{}] as ReturnType<typeof useTasksStore>['active'];
    await updater.restart();
    expect(relaunch).not.toHaveBeenCalled();
    useTasksStore().active = [];
    await updater.restart();
    expect(relaunch).toHaveBeenCalledTimes(1);
  });

  it.each([
    ['0.1.4', '本次更新修复了启动问题', '本次更新修复了启动问题'],
    ['0.1.5', '另一版本的更新记录', 'Improvements']
  ])('Displays localized notes only for the offered version (%s)', async (version, translated, expected) => {
    const get = vi.spyOn(api, 'GET').mockResolvedValue({
      data: { code: '0000', data: { version, notes: translated } },
      response: new Response()
    } as never);
    const updater = useAppUpdaterStore();
    vi.mocked(check).mockResolvedValue(release());
    await updater.checkNow();
    const wrapper = mount(AppUpdateDialog, {
      attachTo: document.body,
      global: {
        plugins: [i18n],
        stubs: { OnMarkdown: { props: ['source'], template: '<div class="on-app-update-notes">{{ source }}</div>' } }
      }
    });
    try {
      await flushPromises();
      expect(get).toHaveBeenCalledWith('/desktop/releases/latest', {
        params: { query: { channel: 'stable' } },
        headers: { 'Accept-Language': 'zh-CN' }
      });
      expect(document.querySelector('.on-app-update-notes')?.textContent).toContain(expected);
    } finally {
      wrapper.unmount();
      document.body.innerHTML = '';
    }
  });

  it('Shows an accessible update dialog with Later focused and postpones it behind other dialogs', async () => {
    const updater = useAppUpdaterStore();
    vi.mocked(check).mockResolvedValue(release());
    await updater.checkNow();
    const blocker = document.createElement('div');
    blocker.setAttribute('role', 'alertdialog');
    document.body.append(blocker);
    const wrapper = mount(AppUpdateDialog, {
      attachTo: document.body,
      global: { plugins: [i18n] }
    });
    try {
      await flushPromises();
      expect(document.querySelector('.on-app-update-dialog')).toBeNull();
      blocker.remove();
      await flushPromises();
      const dialog = document.querySelector<HTMLElement>('.on-app-update-dialog')!;
      expect(dialog.getAttribute('aria-modal')).toBe('true');
      expect(document.getElementById(dialog.getAttribute('aria-labelledby')!)?.textContent).toBe('发现新版本');
      expect(document.activeElement?.textContent).toBe('稍后');
      dialog.querySelector<HTMLButtonElement>('[data-update-later]')!.click();
      await flushPromises();
      expect(updater.dialogOpen).toBe(false);
      expect(updater.update?.downloadAndInstall).not.toHaveBeenCalled();
    } finally {
      wrapper.unmount();
      blocker.remove();
      document.body.innerHTML = '';
    }
  });
});
