import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import TapConfirm from '@/components/shell/TapConfirm.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { useTasksStore } from '@/stores/tasks';
import { useTapConfirmStore } from '@/stores/tapConfirm';
import { setup } from './helpers';
import type { TestContext } from './helpers';

const target = { kind: 'cask' as const, token: 'notion' };
let activeContext: TestContext | undefined;
async function init() {
  const context = await setup('/discover', { tickMs: 1 });
  activeContext = context;
  context.state.installed = context.state.installed.filter(item => item.token !== target.token);
  // The real preflight reports the tap the local catalog lists for this cask.
  vi.spyOn(commands, 'installPreflight').mockResolvedValue({
    status: 'ok',
    data: { name: 'Notion', paths: [], status: 'ready', tap: 'example-dev/tap' }
  });
  return context;
}
afterEach(async () => {
  try {
    // The simulated runner emits IPC events until the queued installation finishes.
    // Keep the mocks and jsdom alive until those callbacks have drained.
    await vi.waitFor(() => expect(activeContext?.state.active ?? []).toHaveLength(0));
  } finally {
    activeContext = undefined;
    vi.restoreAllMocks();
  }
});

describe('Installing from a third-party tap', () => {
  it('asks before every install and queues nothing without consent', async () => {
    await init();
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const tasks = useTasksStore();
    const declined = tasks.enqueue('install', target);
    await flushPromises();
    expect(useTapConfirmStore().pending).toEqual({ name: 'Notion', tap: 'example-dev/tap' });
    useTapConfirmStore().choose(false);
    expect(await declined).toBeNull();
    expect(enqueue).not.toHaveBeenCalled();

    const accepted = tasks.enqueue('install', target);
    await flushPromises();
    expect(useTapConfirmStore().pending?.tap).toBe('example-dev/tap');
    useTapConfirmStore().choose(true);
    expect(await accepted).not.toBeNull();
    // The tap never travels with the request; Rust reads it from the synced catalog.
    expect(enqueue).toHaveBeenCalledWith('install', target, { adopt: false }, 'manual');
  });

  it('shows the tap and treats closing the dialog as declining', async () => {
    const context = await init();
    const wrapper = mount(TapConfirm, {
      global: { plugins: [context.pinia, i18n, context.router] },
      attachTo: document.body
    });
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const result = useTasksStore().enqueue('install', target);
    await flushPromises();
    expect(document.body.textContent).toContain('example-dev/tap');
    expect(document.body.textContent).toContain(i18n.global.t('tapConfirm.title', { name: 'Notion' }));
    wrapper.unmount();
    expect(await result).toBeNull();
    expect(enqueue).not.toHaveBeenCalled();
  });

  it('never asks for official casks', async () => {
    await init();
    vi.spyOn(commands, 'installPreflight').mockResolvedValue({
      status: 'ok',
      data: { name: 'Notion', paths: [], status: 'ready', tap: null }
    });
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    await useTasksStore().enqueue('install', target);
    expect(useTapConfirmStore().pending).toBeNull();
    expect(enqueue).toHaveBeenCalledTimes(1);
  });
});
