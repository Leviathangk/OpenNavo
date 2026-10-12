import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import RunningAppsConfirm from '@/components/shell/RunningAppsConfirm.vue';
import QuitConfirm from '@/components/shell/QuitConfirm.vue';
import RestorePrompt from '@/components/shell/RestorePrompt.vue';
import { i18n } from '@/i18n';
import type { Task } from '@/ipc/bindings';
import { commands, events, unwrap } from '@/ipc/client';
import TrayPage from '@/pages/TrayPage.vue';
import { startLocalState, startTrayState, useTasksStore } from '@/stores';
import { setup } from './helpers';

// M5-01 / M5-02 UI: menu-bar popover, quit confirmation, and queued-task recovery at startup (06 §4.2, §4.3).
afterEach(() => {
  document.body.innerHTML = '';
});

describe('Menu-bar popover', () => {
  it('List updates and enqueue individual/all updates', async () => {
    const context = await setup('/tray', { tickMs: 1000 });
    await startTrayState();
    const mainPrompt = mount(RunningAppsConfirm, {
      global: { plugins: [context.pinia, context.router, i18n] }
    });
    const wrapper = mount(TrayPage, { global: { plugins: [context.pinia, context.router, i18n] } });
    await flushPromises();
    // The simulated machine has 6 updates, including 2 command-line tools; the Cask-only catalog (ADR-018) shows only 4 apps.
    expect(wrapper.text()).toContain('4 个可用更新');
    expect(wrapper.findAll('li').length).toBeGreaterThanOrEqual(4);

    const update = wrapper.findAll('button').find(button => button.text() === '更新');
    await update?.trigger('click');
    await flushPromises();
    expect(await unwrap(commands.taskListActive())).toHaveLength(1);

    await wrapper
      .findAll('button')
      .find(button => button.text().startsWith('全部更新'))
      ?.trigger('click');
    await flushPromises();
    expect((await unwrap(commands.taskListActive())).length).toBe(4);
    mainPrompt.unmount();
    wrapper.unmount();
  });
});

describe('Quit and recovery', () => {
  it('Confirm app:quit-requested before calling app_quit', async () => {
    const context = await setup('/discover', { tickMs: 1000 });
    const wrapper = mount(QuitConfirm, {
      global: { plugins: [context.pinia, i18n] },
      attachTo: document.body
    });
    await flushPromises();
    await events.appQuitRequested.emit({ running: 1, queued: 2 });
    await flushPromises();
    expect(document.body.textContent).toContain('还有 3 个任务没有完成');
    const quit = [...document.body.querySelectorAll('button')].find(button => button.textContent?.includes('仍要退出'));
    quit?.click();
    await flushPromises();
    expect(document.body.textContent).not.toContain('还有 3 个任务没有完成');
    wrapper.unmount();
  });

  it('Resume prior queued tasks with original identities; discard cancels each', async () => {
    const context = await setup('/discover', { tickMs: 1000 });
    const before = performance.timeOrigin - 60_000;
    const task = (token: string): Task => ({
      id: `old-${token}`,
      op: 'upgrade',
      target: { kind: 'formula', token },
      targetName: null,
      outcome: null,
      options: {},
      trigger: 'manual',
      state: 'queued',
      phase: null,
      percent: null,
      bytesDone: null,
      bytesTotal: null,
      speedBps: null,
      stepIndex: null,
      stepCount: null,
      fromVersion: null,
      toVersion: null,
      error: null,
      exitCode: null,
      logPath: null,
      createdAt: before,
      startedAt: null,
      finishedAt: null
    });
    context.state.active.push(task('wget'), task('node'));
    await startLocalState();

    // Startup reads tasks in the background: the prompt mounted before they arrive still asks once they do.
    const wrapper = mount(RestorePrompt, {
      global: { plugins: [context.pinia, i18n] },
      attachTo: document.body
    });
    await vi.waitFor(() => expect(document.body.textContent).toContain('上次有 2 个任务没有完成'));
    expect(useTasksStore().queued).toHaveLength(2);
    [...document.body.querySelectorAll('button')].find(button => button.textContent?.trim() === '放弃')?.click();
    await flushPromises();
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);
    wrapper.unmount();
  });
});
