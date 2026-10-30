import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { getAllWindows } from '@tauri-apps/api/window';
import { commands, events } from '@/ipc/bindings';
import type { Task } from '@/ipc/bindings';
import { unwrap } from '@/ipc/client';
import { useSettingsStore, useUpdatesStore } from '@/stores';
import { setup } from './helpers';

const search = (q: string) =>
  unwrap(
    commands.catalogSearch({
      q,
      kind: null,
      category: null,
      includeDisabled: false,
      limit: 10,
      offset: 0
    })
  );

describe('Local catalog simulation (06 §7.1)', () => {
  it('Exact names rank first; short terms, commands, and spaceless names match', async () => {
    await setup();
    // Cask-only catalog (ADR-018): only apps appear; command names come from bundled commands (visual-studio-code's code).
    expect((await search('ghostty')).items[0]?.item.token).toBe('ghostty');
    expect((await search('code')).items.some(hit => hit.item.token === 'visual-studio-code')).toBe(true);
    expect((await search('微信')).items[0]?.item.token).toBe('wechat');
    expect((await search('vscode')).items[0]?.item.token).toBe('visual-studio-code');
    expect((await search('ripgrep')).items).toHaveLength(0);
  });

  it('Categories follow sort order and counts include descendants', async () => {
    await setup();
    const categories = await unwrap(commands.catalogCategories());
    expect(categories[0]?.slug).toBe('ai');
    expect(categories.find(category => category.slug === 'developer-tools')?.packageCount).toBeGreaterThan(0);
  });
});

describe('Settings and check state (06 §11, §6.8)', () => {
  it('Tray Open/Settings can query and show the simulated main window', async () => {
    await setup('/tray');
    const windows = await getAllWindows();
    const main = windows.find(window => window.label === 'main');
    expect(main).toBeDefined();
    await main?.unminimize();
    await main?.show();
    await main?.setFocus();
  });

  it('Disabling automatic checks clears next check; save full settings objects', async () => {
    await setup();
    const settings = useSettingsStore();
    const updates = useUpdatesStore();
    await Promise.all([settings.load(), updates.load()]);
    expect(updates.nextCheckAt).not.toBeNull();
    // Select a nondefault download source first; changing other settings must preserve it instead of reverting to the official source.
    const tuna = 'https://mirrors.tuna.tsinghua.edu.cn';
    await settings.update('mirror', {
      key: 'tuna',
      apiDomain: `${tuna}/homebrew-bottles/api`,
      bottleDomain: `${tuna}/homebrew-bottles`,
      brewGitRemote: `${tuna}/git/homebrew/brew.git`,
      coreGitRemote: `${tuna}/git/homebrew/homebrew-core.git`
    });
    await settings.update('autoCheck', false);
    await updates.loadStatus();
    expect(updates.nextCheckAt).toBeNull();
    expect(settings.value?.mirror.key).toBe('tuna');
  });
});

describe('Task queue simulation (06 §9)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    window.history.replaceState(null, '', '/');
  });

  it.each(['upgrade', 'uninstall'] as const)(
    '%s remains blocked when App Management is granted but pending relaunch',
    async op => {
      // Working Full Disk Access also lets OpenNavo change apps, so this case turns it off too.
      window.history.replaceState(null, '', '/?mock=no-app-management,no-full-disk-access,permission-relaunch');
      await setup('/updates', { tickMs: 10 });
      const updates: Task[] = [];
      await events.taskUpdated.listen(event => updates.push(event.payload));
      const enqueue = () => unwrap(commands.taskEnqueue(op, { kind: 'cask', token: 'ghostty' }, {}, 'manual'));
      await enqueue();
      await vi.advanceTimersByTimeAsync(2000);
      expect(updates.at(-1)?.error?.code).toBe('E_PERMISSION');

      await unwrap(commands.permissionGuideOpen('app_management'));
      await vi.advanceTimersByTimeAsync(100);
      expect(await unwrap(commands.permissionsGet())).toMatchObject({
        appManagement: 'granted',
        appManagementPending: true,
        relaunchRequired: true
      });

      const retry = await enqueue();
      await vi.advanceTimersByTimeAsync(2000);
      expect(updates.at(-1)).toMatchObject({
        id: retry.id,
        state: 'failed',
        error: { code: 'E_PERMISSION' }
      });
    }
  );

  it('Updates emit staged events, leave available updates, and enter history on completion', async () => {
    await setup('/updates', { tickMs: 10 });
    const updates: Task[] = [];
    await events.taskUpdated.listen(event => updates.push(event.payload));
    const task = await unwrap(commands.taskEnqueue('upgrade', { kind: 'cask', token: 'ghostty' }, {}, 'manual'));
    expect(task.state).toBe('queued');
    // Enqueuing the same package again returns the existing task.
    expect((await unwrap(commands.taskEnqueue('upgrade', { kind: 'cask', token: 'ghostty' }, {}, 'manual'))).id).toBe(
      task.id
    );
    await vi.advanceTimersByTimeAsync(2000);
    expect(updates.some(item => item.state === 'running' && item.phase === 'downloading')).toBe(true);
    expect(updates.at(-1)?.state).toBe('succeeded');
    const outdated = await unwrap(commands.updatesList());
    expect(outdated.some(item => item.token === 'ghostty')).toBe(false);
    const history = await unwrap(
      commands.historyList({
        q: 'ghostty',
        ops: [],
        states: [],
        target: null,
        includeScheduledUpdates: false,
        from: null,
        to: null,
        limit: 10,
        offset: 0
      })
    );
    expect(history.items[0]?.toVersion).toBeTruthy();
  });

  it('Firefox checksum failure E_CHECKSUM includes error logs', async () => {
    await setup('/discover', { tickMs: 10 });
    const task = await unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'firefox' }, {}, 'manual'));
    await vi.advanceTimersByTimeAsync(2000);
    const history = await unwrap(
      commands.historyList({
        q: 'firefox',
        ops: ['install'],
        states: ['failed'],
        target: null,
        includeScheduledUpdates: false,
        from: null,
        to: null,
        limit: 10,
        offset: 0
      })
    );
    expect(history.items[0]?.error?.code).toBe('E_CHECKSUM');
    expect(await unwrap(commands.taskLog(task.id, null))).toContain('Error: SHA256 mismatch');
  });

  it('Canceled running tasks report canceled with E_INTERRUPTED', async () => {
    await setup('/discover', { tickMs: 10 });
    const task = await unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'zed' }, {}, 'manual'));
    await vi.advanceTimersByTimeAsync(60);
    await unwrap(commands.taskCancel(task.id));
    const active = await unwrap(commands.taskListActive());
    expect(active.some(item => item.id === task.id)).toBe(false);
    const history = await unwrap(
      commands.historyList({
        q: 'zed',
        ops: [],
        states: ['canceled'],
        target: null,
        includeScheduledUpdates: false,
        from: null,
        to: null,
        limit: 10,
        offset: 0
      })
    );
    expect(history.items[0]?.error?.code).toBe('E_INTERRUPTED');
  });
});

describe('Debug-only deep-link mock gate', () => {
  it('approves the guarded command only with the development switch', async () => {
    window.history.replaceState(null, '', '/?mock=deeplink-approve');
    await setup('/discover');
    const result = await unwrap(commands.deeplinkInstallConfirm('mock-pending'));
    expect(result.tasks).toEqual([]);
  });
});
