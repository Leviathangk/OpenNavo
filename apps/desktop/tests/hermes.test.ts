import { nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useHermesStore } from '@/stores/hermes';
import { useToasts } from '@/composables/useToasts';
import { commands } from '@/ipc/client';

const eventMock = vi.hoisted(() => {
  const listeners: { event: string; handler: (event: { payload: { requestId: string; delta: string } }) => void }[] =
    [];
  return {
    listeners,
    listen: vi.fn(
      async (event: string, handler: (event: { payload: { requestId: string; delta: string } }) => void) => {
        listeners.push({ event, handler });
        return () => undefined;
      }
    )
  };
});

vi.mock('@tauri-apps/api/event', () => ({ listen: eventMock.listen }));

const STORAGE_KEY = 'ONV_HERMES_STATE_V2';

beforeEach(() => {
  localStorage.clear();
  setActivePinia(createPinia());
});

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

describe('Hermes conversation store', () => {
  it('Migrates old full-control state to safe Mac defaults', () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({
        mac: {
          enabled: true,
          level: 'full',
          terminal: true,
          screen: true,
          files: true,
          install: true,
          cron: true,
          confirmWrites: false
        },
        conversations: [],
        activeId: null
      })
    );
    const store = useHermesStore();
    expect(store.mac.enabled).toBe(false);
    expect(store.mac.level).toBe('open');
    expect(store.mac.confirmWrites).toBe(true);
    const saved = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '{}') as {
      version: number;
      mac: { enabled: boolean };
    };
    expect(saved.version).toBe(3);
    expect(saved.mac.enabled).toBe(false);
  });

  it('Starts with an empty desktop history', () => {
    const store = useHermesStore();
    expect(store.conversations).toHaveLength(0);
    expect(store.activeId).toBeNull();
    expect(store.messages).toHaveLength(0);
    expect(store.history).toHaveLength(0);
  });

  it('Creates a new conversation from a typed prompt and appends a deterministic reply', async () => {
    vi.useFakeTimers();
    const store = useHermesStore();
    store.newChat();
    expect(store.activeId).toBeNull();
    store.send('磁盘快满了，看看能清什么。');
    expect(store.thinking).toBe(true);
    expect(store.activeId).not.toBeNull();
    expect(store.messages).toHaveLength(1);
    vi.advanceTimersByTime(600);
    await nextTick();
    expect(store.thinking).toBe(false);
    expect(store.messages).toHaveLength(2);
    expect(store.messages[1]?.card).toBe('cleanup');
    expect(store.history[0]?.id).toBe(store.activeId);
  });

  it('Renames, deletes and clears local conversations', async () => {
    vi.useFakeTimers();
    const store = useHermesStore();
    store.send('hello');
    vi.advanceTimersByTime(600);
    await nextTick();
    const id = store.activeId;
    expect(id).toBeTruthy();
    store.renameConversation(id!, 'Renamed chat');
    expect(store.conversations.find(item => item.id === id)?.title).toBe('Renamed chat');
    store.deleteConversation(id!);
    expect(store.conversations).toHaveLength(0);
    expect(store.activeId).toBeNull();
    store.send('another');
    vi.advanceTimersByTime(600);
    await nextTick();
    store.clearHistory();
    expect(store.conversations).toHaveLength(0);
    expect(store.activeId).toBeNull();
  });

  it('Persists model, effort and Mac control settings across store instances', async () => {
    const store = useHermesStore();
    store.saveModel({ ...store.model, provider: 'openai', model: 'GPT-5.6', effort: 'high' });
    store.saveMac({ ...store.mac, enabled: false, level: 'guarded', confirmWrites: true });
    await nextTick();
    const raw = localStorage.getItem(STORAGE_KEY);
    expect(raw).toBeTruthy();
    const saved = JSON.parse(raw ?? '{}') as { model: { provider: string; effort: string }; mac: { enabled: boolean } };
    expect(saved.model).toMatchObject({ provider: 'openai', effort: 'high' });
    expect(saved.mac.enabled).toBe(false);

    setActivePinia(createPinia());
    const restored = useHermesStore();
    expect(restored.model.model).toBe('GPT-5.6');
    expect(restored.model.effort).toBe('high');
    expect(restored.mac.enabled).toBe(false);
    expect(restored.mac.confirmWrites).toBe(true);
  });

  it('Uses the bundled runtime when it reports ready and resumes its session', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    const chat = vi.spyOn(commands, 'hermesChat').mockResolvedValue({
      status: 'ok',
      data: { text: 'Real Hermes reply', sessionId: 'session-1', liveSessionId: null, usage: null }
    });
    const store = useHermesStore();
    await store.loadRuntime();
    expect(store.runtimeReady).toBe(true);
    store.newChat();
    await store.send('Do something on this Mac');
    expect(chat).toHaveBeenCalledWith(
      expect.objectContaining({
        prompt: 'Do something on this Mac',
        sessionId: null,
        provider: 'nous',
        model: 'nousresearch/hermes-4-405b',
        controlLevel: 'open',
        macEnabled: false,
        macTerminal: false,
        macScreen: false,
        macFiles: false,
        macCron: false
      })
    );
    expect(store.messages.at(-1)?.text).toBe('Real Hermes reply');
    expect(store.active?.sessionId).toBe('session-1');
    expect(store.thinking).toBe(false);
  });

  it('Renders gateway deltas into the assistant message before the final reply', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let requestId = '';
    let finish: (value: unknown) => void = () => undefined;
    vi.spyOn(commands, 'hermesChat').mockImplementation(input => {
      requestId = input.requestId;
      return new Promise(resolve => {
        finish = resolve;
      }) as never;
    });
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    eventMock.listeners.length = 0;
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    const sending = store.send('Stream this answer');
    for (
      let attempt = 0;
      attempt < 20 && !eventMock.listeners.some(listener => listener.event === 'hermes-chunk');
      attempt++
    ) {
      await new Promise(resolve => setTimeout(resolve, 0));
    }
    expect(eventMock.listeners.some(listener => listener.event === 'hermes-chunk')).toBe(true);
    const handler = eventMock.listeners.find(listener => listener.event === 'hermes-chunk')!.handler;
    handler({ payload: { requestId, delta: 'Hello' } });
    handler({ payload: { requestId, delta: ' world' } });
    await nextTick();
    expect(store.messages.at(-1)?.text).toBe('Hello world');
    expect(store.messages.at(-1)?.streaming).toBe(true);
    finish({ status: 'ok', data: { text: 'Hello world', sessionId: 'stored-1', liveSessionId: null, usage: null } });
    await sending;
    expect(store.messages.at(-1)?.text).toBe('Hello world');
    expect(store.messages.at(-1)?.streaming).toBe(false);
    expect(store.active?.sessionId).toBe('stored-1');
    Reflect.deleteProperty(window, '__TAURI_INTERNALS__');
  });

  it('Does not let an earlier stop fallback kill the next turn', async () => {
    vi.useFakeTimers();
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    const pending: Array<(value: unknown) => void> = [];
    vi.spyOn(commands, 'hermesChat').mockImplementation(
      () =>
        new Promise(resolve => {
          pending.push(resolve);
        }) as never
    );
    const stop = vi.spyOn(commands, 'hermesStop').mockResolvedValue({ status: 'ok', data: null } as never);
    vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('first turn');
    expect(pending).toHaveLength(1);
    await store.stop();
    store.stopThinking();
    const stopsBeforeNewTurn = stop.mock.calls.length;
    void store.send('second turn');
    expect(pending).toHaveLength(2);
    vi.advanceTimersByTime(1_200);
    expect(stop.mock.calls.length).toBe(stopsBeforeNewTurn);
    expect(store.thinking).toBe(true);
    pending[1]!({
      status: 'ok',
      data: { text: 'second reply', sessionId: 'session-2', liveSessionId: null, usage: null }
    });
    await Promise.resolve();
    await Promise.resolve();
    expect(store.thinking).toBe(false);
    expect(store.messages.at(-1)?.text).toBe('second reply');
  });

  it('Finalizes a hung stop without reporting a runtime failure', async () => {
    vi.useFakeTimers();
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    const pending: Array<(value: unknown) => void> = [];
    vi.spyOn(commands, 'hermesChat').mockImplementation(
      () =>
        new Promise(resolve => {
          pending.push(resolve);
        }) as never
    );
    vi.spyOn(commands, 'hermesStop').mockResolvedValue({ status: 'ok', data: null } as never);
    vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const toasts = useToasts();
    toasts.clear();
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('hang');
    await store.stop();
    vi.advanceTimersByTime(10_000);
    expect(store.thinking).toBe(false);
    expect(store.stopping).toBe(false);
    expect(store.messages.at(-1)?.text).toBe('已停止。');
    pending[0]!({ status: 'error', error: { code: 'E_HERMES_STOPPED', message: 'stopped', detail: null } });
    await Promise.resolve();
    await Promise.resolve();
    expect(store.messages.at(-1)?.text).toBe('已停止。');
    expect(toasts.items.value.some(item => item.tone === 'danger')).toBe(false);
  });

  it('Re-enables the composer after stopping a local demo reply', async () => {
    vi.useFakeTimers();
    const store = useHermesStore();
    store.newChat();
    void store.send('demo reply');
    expect(store.thinking).toBe(true);
    await store.stop();
    expect(store.stopping).toBe(true);
    vi.advanceTimersByTime(600);
    expect(store.thinking).toBe(false);
    expect(store.stopping).toBe(false);
  });

  it('Arms stop fallbacks when steering is rejected', async () => {
    vi.useFakeTimers();
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesChat').mockImplementation(() => new Promise(() => undefined) as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('does not support steer'));
    vi.spyOn(commands, 'hermesInterrupt').mockImplementation(() => new Promise(() => undefined) as never);
    vi.spyOn(commands, 'hermesStop').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    expect(store.thinking).toBe(true);
    store.active!.liveSessionId = 'live-1';
    void store.send('steer this');
    await Promise.resolve();
    await Promise.resolve();
    expect(store.stopping).toBe(true);
    vi.advanceTimersByTime(10_000);
    expect(store.thinking).toBe(false);
    expect(store.stopping).toBe(false);
  });

  it('Stops Mac control on panic', () => {
    const store = useHermesStore();
    store.stopMac();
    expect(store.mac.enabled).toBe(false);
    expect(store.macStatus).toBe('paused');
  });
});
