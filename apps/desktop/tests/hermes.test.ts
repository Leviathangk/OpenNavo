import { nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useHermesStore } from '@/stores/hermes';
import { HERMES_PROVIDER_MAP, hermesProviderSlug, pickOfficialProvider } from '@/hermes/providers';
import { useToasts } from '@/composables/useToasts';
import { IpcError, commands } from '@/ipc/client';

const eventMock = vi.hoisted(() => {
  type EventPayload = Record<string, unknown>;
  const listeners: { event: string; handler: (event: { payload: EventPayload }) => void }[] = [];
  return {
    listeners,
    listen: vi.fn(async (event: string, handler: (event: { payload: EventPayload }) => void) => {
      listeners.push({ event, handler });
      return () => undefined;
    })
  };
});

vi.mock('@tauri-apps/api/event', () => ({ listen: eventMock.listen }));

const STORAGE_KEY = 'ONV_HERMES_STATE_V2';
const PROFILES_KEY = 'ONV_HERMES_PROFILES_V1';

const PANEL_CATALOG = {
  providers: [
    {
      slug: 'custom',
      name: 'Custom',
      is_current: true,
      authenticated: false,
      models: ['grok-4.7'],
      total_models: 1
    },
    {
      slug: 'custom:newapi',
      name: 'newapi',
      is_current: true,
      authenticated: true,
      models: ['grok-4.6', 'grok-4.3'],
      total_models: 2
    }
  ],
  model: 'grok-4.7',
  provider: 'custom'
};

function seedPanelProfile() {
  localStorage.setItem(
    PROFILES_KEY,
    JSON.stringify({
      version: 1,
      activeProfileId: 'panel-1',
      profiles: [
        {
          id: 'local',
          name: 'Local Mac',
          kind: 'local',
          endpoint: '',
          status: 'connected',
          version: '0.23.0',
          latency: null,
          models: 0
        },
        {
          id: 'panel-1',
          name: 'Panel',
          kind: 'panel',
          endpoint: 'http://panel.tail1234.ts.net:9119',
          status: 'connected',
          version: '0.23.0',
          latency: 12,
          models: 2
        }
      ],
      states: {}
    })
  );
}

beforeEach(() => {
  localStorage.clear();
  setActivePinia(createPinia());
  eventMock.listeners.length = 0;
});

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
  Reflect.deleteProperty(window, '__TAURI_INTERNALS__');
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

  it('Restores per-profile history and disables Mac controls on a remote profile', () => {
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: { providers: [], model: null, provider: null }
    });
    const store = useHermesStore();
    store.conversations.push({
      id: 'local-conversation',
      messages: [{ id: 'local-message', role: 'user', text: 'local', at: 1 }],
      updatedAt: 1
    });
    store.activeId = 'local-conversation';
    store.profiles.push({
      id: 'remote-test',
      name: 'Remote test',
      kind: 'remote',
      endpoint: 'wss://remote.test/api/ws',
      status: 'connected',
      version: '0.23.0',
      latency: 12,
      models: 2
    });
    store.switchProfile('remote-test');
    expect(store.remoteActive).toBe(true);
    expect(store.messages).toHaveLength(0);
    expect(store.mac.enabled).toBe(false);
    store.switchProfile('local');
    expect(store.remoteActive).toBe(false);
    expect(store.messages[0]?.text).toBe('local');
  });

  it('Persists profile color and default route', () => {
    const store = useHermesStore();
    store.setAgentProfileColor('work', '#123456');
    store.setAgentProfileDefault('work');
    const raw = JSON.parse(localStorage.getItem('ONV_HERMES_PROFILES_V1') ?? '{}') as {
      profileColors?: Record<string, string>;
      defaultAgentProfiles?: Record<string, string>;
    };
    expect(raw.profileColors?.work).toBe('#123456');
    expect(raw.defaultAgentProfiles?.local).toBe('work');
  });

  it('Interrupts a remote turn with the id and route it had before clearing them', () => {
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: { providers: [], model: null, provider: null }
    });
    const store = useHermesStore();
    store.profiles.push({
      id: 'remote-test',
      name: 'Remote test',
      kind: 'remote',
      endpoint: 'wss://remote.test/api/ws',
      status: 'connected',
      version: '0.23.0',
      latency: 12,
      models: 2
    });
    store.switchProfile('remote-test');
    store.conversations.push({
      id: 'remote-chat',
      messages: [{ id: 'remote-message', role: 'user', text: 'remote', at: 1 }],
      updatedAt: 1,
      liveSessionId: 'live-1'
    });
    store.activeId = 'remote-chat';
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);

    store.stopThinking();

    expect(interrupt).toHaveBeenCalledWith('live-1', 'wss://remote.test/api/ws', 'remote-test');
    expect(store.active?.liveSessionId).toBeUndefined();
  });

  it('Selects a notification conversation without stopping the active turn', async () => {
    const store = useHermesStore();
    store.conversations.push(
      {
        id: 'finished-chat',
        messages: [{ id: 'finished-message', role: 'user', text: 'finished', at: 1 }],
        updatedAt: 1
      },
      {
        id: 'running-chat',
        messages: [{ id: 'running-message', role: 'user', text: 'running', at: 2 }],
        updatedAt: 2,
        liveSessionId: 'live-2'
      }
    );
    store.activeId = 'running-chat';
    const interrupt = vi.spyOn(commands, 'hermesInterrupt');
    const stop = vi.spyOn(commands, 'hermesStop');

    await expect(store.revealConversation('finished-chat')).resolves.toBe(true);

    expect(store.activeId).toBe('finished-chat');
    expect(interrupt).not.toHaveBeenCalled();
    expect(stop).not.toHaveBeenCalled();
  });

  it('Ignores a history read that finishes after another profile took over', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    const deferred = new Map<string, (value: unknown) => void>();
    vi.spyOn(commands, 'hermesHistoryLoad').mockImplementation(
      profile =>
        new Promise(resolve => {
          deferred.set(profile ?? 'default', resolve);
        }) as never
    );
    vi.spyOn(commands, 'hermesHistorySave').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();

    const first = store.loadHistory('default');
    // Let the first read reach the backend before the switch supersedes it.
    await Promise.resolve();
    await Promise.resolve();
    const second = store.loadHistory('work');
    await Promise.resolve();
    await Promise.resolve();
    deferred.get('work')!({
      status: 'ok',
      data: JSON.stringify({
        conversations: [{ id: 'work-chat', messages: [], updatedAt: 2 }],
        activeId: 'work-chat'
      })
    });
    await second;
    expect(store.conversations.map(item => item.id)).toEqual(['work-chat']);

    // The older request returns last and must not overwrite the profile that owns the store.
    deferred.get('default')!({
      status: 'ok',
      data: JSON.stringify({
        conversations: [{ id: 'default-chat', messages: [], updatedAt: 1 }],
        activeId: 'default-chat'
      })
    });
    await first;
    expect(store.conversations.map(item => item.id)).toEqual(['work-chat']);
    expect(store.activeId).toBe('work-chat');
    Reflect.deleteProperty(window, '__TAURI_INTERNALS__');
  });

  it('Keeps conversations isolated per Hermes profile', () => {
    const store = useHermesStore();
    store.conversations.push({
      id: 'default-chat',
      messages: [{ id: 'default-message', role: 'user', text: 'default', at: 1 }],
      updatedAt: 1
    });
    store.activeId = 'default-chat';
    store.switchAgentProfile('work');
    expect(store.activeAgentProfileName).toBe('work');
    expect(store.messages).toHaveLength(0);
    store.conversations.push({
      id: 'work-chat',
      messages: [{ id: 'work-message', role: 'user', text: 'work', at: 2 }],
      updatedAt: 2
    });
    store.activeId = 'work-chat';
    store.switchAgentProfile('default');
    expect(store.activeAgentProfileName).toBe('default');
    expect(store.messages[0]?.text).toBe('default');
  });

  it('Drops a saved API key when the agent profile changes', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: { providers: [], model: null, provider: null }
    });
    const chat = vi.spyOn(commands, 'hermesChat').mockResolvedValue({
      status: 'ok',
      data: { text: 'reply', sessionId: 'session-1', liveSessionId: null, usage: null }
    });
    const store = useHermesStore();
    await store.loadRuntime();
    // A key the sheet saved while profile `default` was active. Keeping it in the store made
    // the next turn in another profile write it into that profile's `.env`.
    store.setApiKey('sk-profile-default');
    await store.switchAgentProfile('work');
    expect(store.activeAgentProfileName).toBe('work');
    expect(store.apiKey).toBe('');
    store.newChat();
    await store.send('hello');
    expect(chat).toHaveBeenCalledWith(expect.objectContaining({ apiKey: null, profile: 'work' }));
  });

  it('Switches to a profile on another connection in one step', () => {
    const store = useHermesStore();
    store.profiles.push({
      id: 'fleet-remote',
      name: 'Fleet remote',
      kind: 'remote',
      endpoint: 'wss://fleet.test/api/ws',
      status: 'connected',
      version: '0.23.0',
      latency: 10,
      models: 2
    });
    store.switchToAgentProfile('fleet-remote', 'research');
    expect(store.activeProfileId).toBe('fleet-remote');
    expect(store.activeAgentProfileName).toBe('research');
    expect(store.remoteActive).toBe(true);
  });

  it('Sends the remote gateway and profile with a remote chat request', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: false,
        installed: false,
        version: null,
        expectedVersion: '0.19.0',
        path: null,
        dataPath: '/tmp/OpenNavoData',
        computerUse: false,
        outdated: false,
        message: null
      }
    });
    const chat = vi.spyOn(commands, 'hermesChat').mockResolvedValue({
      status: 'ok',
      data: { text: 'Remote reply', sessionId: 'remote-session', liveSessionId: null, usage: null }
    });
    localStorage.setItem(
      'ONV_HERMES_PROFILES_V1',
      JSON.stringify({
        version: 1,
        activeProfileId: 'remote-test',
        profiles: [
          {
            id: 'local',
            name: 'Local Mac',
            kind: 'local',
            endpoint: '',
            status: 'connected',
            version: '0.23.0',
            latency: null,
            models: 0
          },
          {
            id: 'remote-test',
            name: 'Panel test',
            kind: 'panel',
            endpoint: 'wss://remote.test/api/ws',
            status: 'connected',
            version: '0.23.0',
            latency: 12,
            models: 2
          }
        ],
        states: {}
      })
    );
    const store = useHermesStore();
    await store.send('Use the remote gateway');
    expect(chat).toHaveBeenCalledWith(
      expect.objectContaining({
        gatewayUrl: 'wss://remote.test/api/ws',
        profileId: 'remote-test',
        panelAuth: true,
        macEnabled: false,
        macTerminal: false,
        macScreen: false,
        macFiles: false,
        macCron: false
      })
    );
    expect(store.messages.at(-1)?.text).toBe('Remote reply');
  });

  it('Aligns a panel profile to the authenticated provider row', async () => {
    seedPanelProfile();
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: PANEL_CATALOG
    });
    const store = useHermesStore();

    await store.loadModelOptions(false);

    // `custom` is a stale, unauthenticated row with one saved model. The endpoint that
    // actually answers is the scoped `custom:newapi` row, so a new profile must not inherit
    // the stale model or card-level provider id.
    expect(store.model.provider).toBe('custom');
    expect(store.model.model).toBe('grok-4.6');
  });

  it('Sends the scoped panel provider slug instead of the provider card id', async () => {
    seedPanelProfile();
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: PANEL_CATALOG
    });
    const chat = vi.spyOn(commands, 'hermesChat').mockResolvedValue({
      status: 'ok',
      data: { text: 'Panel reply', sessionId: 'panel-session', liveSessionId: null, usage: null }
    });
    const store = useHermesStore();
    await store.loadModelOptions(false);

    await store.send('Use the working endpoint');

    expect(chat).toHaveBeenCalledWith(
      expect.objectContaining({
        provider: 'custom:newapi',
        model: 'grok-4.6'
      })
    );
  });

  it('Repairs a stale saved model before the next panel turn', async () => {
    seedPanelProfile();
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: PANEL_CATALOG
    });
    const chat = vi.spyOn(commands, 'hermesChat').mockResolvedValue({
      status: 'ok',
      data: { text: 'Panel reply', sessionId: 'panel-session', liveSessionId: null, usage: null }
    });
    const store = useHermesStore();
    await store.loadModelOptions(false);
    store.saveModel({ ...store.model, provider: 'custom', model: 'grok-4.7' });

    await store.send('Use the repaired endpoint');

    expect(store.model.model).toBe('grok-4.6');
    expect(chat).toHaveBeenCalledWith(expect.objectContaining({ provider: 'custom:newapi', model: 'grok-4.6' }));
  });

  it('Does not treat a model provider 401 as an expired panel session', async () => {
    seedPanelProfile();
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: PANEL_CATALOG
    });
    vi.spyOn(commands, 'hermesChat').mockRejectedValue(
      new IpcError({
        code: 'E_HERMES_GATEWAY_STREAM',
        message: 'custom rejected your API key',
        detail: 'Provider said: HTTP 401: Invalid token'
      })
    );
    const store = useHermesStore();
    await store.loadModelOptions(false);

    await store.send('Trigger the provider error');

    expect(store.panelAuthExpired).toBeNull();
  });

  it('Still asks for a panel login when the panel session itself expired', async () => {
    seedPanelProfile();
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: PANEL_CATALOG
    });
    vi.spyOn(commands, 'hermesChat').mockRejectedValue(
      new IpcError({
        code: 'E_HERMES_PANEL_AUTH',
        message: 'Hermes Panel session is missing. Sign in again.',
        detail: null
      })
    );
    const store = useHermesStore();
    await store.loadModelOptions(false);

    await store.send('Trigger the panel auth error');

    expect(store.panelAuthExpired).toBe('panel-1');
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
    expect(store.conversations.find((item: { id: string }) => item.id === id)?.title).toBe('Renamed chat');
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
        expectedVersion: '0.19.0',
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
        expectedVersion: '0.19.0',
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

  it('Shows one card at a time and records what the runtime did with it', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    const respond = vi.spyOn(commands, 'hermesAskRespond').mockResolvedValue({ status: 'ok', data: null });
    let turnRequestId = '';
    vi.spyOn(commands, 'hermesChat').mockImplementation(input => {
      turnRequestId = input.requestId;
      return new Promise(() => undefined) as never;
    });
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    eventMock.listeners.length = 0;
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('Run the migration');
    await vi.waitFor(() => expect(turnRequestId).not.toBe(''));
    await vi.waitFor(() =>
      expect(eventMock.listeners.some(listener => listener.event === 'hermes-ask-settled')).toBe(true)
    );
    const ask = eventMock.listeners.find(listener => listener.event === 'hermes-ask')!.handler;
    const settled = eventMock.listeners.find(listener => listener.event === 'hermes-ask-settled')!.handler;

    // Two requests can be open together (a clarify behind an approval); the card shows the first.
    ask({
      payload: {
        requestId: 'srq-1',
        turnRequestId,
        kind: 'approval',
        deadline: null,
        params: { command: 'rm -rf build', choices: ['once', 'deny'] }
      }
    });
    ask({
      payload: {
        requestId: 'srq-2',
        turnRequestId,
        kind: 'clarify',
        deadline: null,
        params: { questions: [{ qid: 'q1', question: 'Which database?' }] }
      }
    });
    await nextTick();
    expect(store.approvalRequest?.command).toBe('rm -rf build');
    expect(store.clarifyRequest).toBeNull();

    await store.respondApproval('once');
    expect(respond).toHaveBeenCalledWith('srq-1', { kind: 'approval', choice: 'once' });
    await nextTick();
    // Answering discloses the next card instead of leaving the queue stuck.
    expect(store.approvalRequest).toBeNull();
    expect(store.clarifyRequest?.questions[0]?.question).toBe('Which database?');

    // The settled event is what puts the decision in the transcript.
    settled({
      payload: {
        requestId: 'srq-1',
        turnRequestId,
        kind: 'approval',
        outcome: 'allowed',
        scope: 'once',
        summary: 'rm -rf build',
        count: null
      }
    });
    await nextTick();
    expect(store.messages.at(-1)?.decisions?.[0]).toMatchObject({
      outcome: 'allowed',
      scope: 'once',
      summary: 'rm -rf build'
    });

    // A withdrawn question tears the card down without inventing a decision from the user.
    settled({
      payload: {
        requestId: 'srq-2',
        turnRequestId,
        kind: 'clarify',
        outcome: 'cancelled',
        scope: null,
        summary: 'Which database?',
        count: null
      }
    });
    await nextTick();
    expect(store.clarifyRequest).toBeNull();
    expect(store.messages.at(-1)?.decisions?.at(-1)?.outcome).toBe('cancelled');
  });

  it('Does not let an earlier stop fallback kill the next turn', async () => {
    vi.useFakeTimers();
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
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
        expectedVersion: '0.19.0',
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

  it('Sends a steer with the running execution generation', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesChat').mockImplementation(() => new Promise(() => undefined) as never);
    const steer = vi.spyOn(commands, 'hermesSteer').mockResolvedValue({
      status: 'ok',
      data: { status: 'queued', delivered: null }
    } as never);
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    expect(store.thinking).toBe(true);
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;

    await store.send('steer this');

    expect(steer).toHaveBeenCalledWith('live-1', 7, 'steer this', null, null);
    expect(interrupt).not.toHaveBeenCalled();
    expect(store.stopping).toBe(false);
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(true);
  });

  it('Keeps a rejected steer that may have been delivered', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesChat').mockImplementation(() => new Promise(() => undefined) as never);
    vi.spyOn(commands, 'hermesSteer').mockResolvedValue({
      status: 'ok',
      data: { status: 'rejected', delivered: null }
    } as never);
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;

    await store.send('steer this');

    expect(interrupt).not.toHaveBeenCalled();
    expect(store.stopping).toBe(false);
    expect(store.thinking).toBe(true);
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(true);
  });

  it('Queues an undelivered steer for the next turn without interrupting', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let finishTurn!: (value: unknown) => void;
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishTurn = resolve;
          }) as never
      )
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    vi.spyOn(commands, 'hermesSteer').mockResolvedValue({
      status: 'ok',
      data: { status: 'rejected', delivered: false }
    } as never);
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;

    await store.send('steer this');

    expect(interrupt).not.toHaveBeenCalled();
    expect(store.thinking).toBe(true);
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(false);

    finishTurn({ status: 'ok', data: { text: 'done', sessionId: null, liveSessionId: null, usage: null } });
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
    expect(store.messages.some(entry => entry.text === 'steer this' && !entry.steering)).toBe(true);
  });

  it('Waits for the running generation before steering', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let requestId = '';
    vi.spyOn(commands, 'hermesChat').mockImplementation(input => {
      requestId = input.requestId;
      return new Promise(() => undefined) as never;
    });
    const steer = vi.spyOn(commands, 'hermesSteer').mockResolvedValue({
      status: 'ok',
      data: { status: 'queued', delivered: null }
    } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(requestId).not.toBe(''));
    store.active!.liveSessionId = 'live-1';

    await store.send('steer this');
    expect(steer).not.toHaveBeenCalled();

    const listener = eventMock.listeners.filter(entry => entry.event === 'hermes-session').at(-1);
    expect(listener).toBeDefined();
    listener!.handler({
      payload: {
        requestId,
        liveSessionId: 'live-1',
        sessionId: 'stored-1',
        executionGeneration: 7
      }
    });

    await vi.waitFor(() => expect(steer).toHaveBeenCalledWith('live-1', 7, 'steer this', null, null));
  });

  it('Keeps a steer that is still waiting for a generation across a session reset', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let finishInitial!: (value: unknown) => void;
    const settled = {
      status: 'ok',
      data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
    };
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishInitial = resolve;
          }) as never
      )
      .mockResolvedValue(settled as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));

    // The turn has not claimed a generation yet, so the steer waits for one.
    await store.send('steer across reset');
    expect(store.messages.some(entry => entry.text === 'steer across reset' && entry.steering)).toBe(true);

    store.setWorkingDir('/tmp/hermes-review');
    finishInitial(settled);

    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({
      prompt: 'steer across reset',
      cwd: '/tmp/hermes-review'
    });
  });

  it('Removes an undelivered steer and sends it when the turn already ended', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let requestId = '';
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(input => {
        requestId = input.requestId;
        return new Promise(() => undefined) as never;
      })
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    let failSteer!: (reason: unknown) => void;
    vi.spyOn(commands, 'hermesSteer').mockImplementation(
      () =>
        new Promise((_, reject) => {
          failSteer = reject;
        }) as never
    );
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(requestId).not.toBe(''));
    store.active!.liveSessionId = 'live-1';

    await store.send('steer this');
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(true);

    const listener = eventMock.listeners.filter(entry => entry.event === 'hermes-session').at(-1);
    listener!.handler({
      payload: {
        requestId,
        liveSessionId: 'live-1',
        sessionId: 'stored-1',
        executionGeneration: 7
      }
    });
    store.stopThinking();
    failSteer(new Error('gateway unavailable'));

    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(interrupt).not.toHaveBeenCalled();
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(false);
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
  });

  it('Keeps a failed steer in its own conversation after switching away', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let requestId = '';
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(input => {
        requestId = input.requestId;
        return new Promise(() => undefined) as never;
      })
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    let failSteer!: (reason: unknown) => void;
    vi.spyOn(commands, 'hermesSteer').mockImplementation(
      () =>
        new Promise((_, reject) => {
          failSteer = reject;
        }) as never
    );
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(requestId).not.toBe(''));
    const original = store.active!;
    original.liveSessionId = 'live-1';
    original.executionGeneration = 7;

    const steering = store.send('steer this');
    await vi.waitFor(() => expect(commands.hermesSteer).toHaveBeenCalled());
    store.stopThinking();
    store.newChat();
    failSteer(new Error('gateway unavailable'));
    await steering;

    expect(chat).toHaveBeenCalledTimes(1);
    expect(interrupt).not.toHaveBeenCalled();

    store.select(original.id);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
    expect(store.activeId).toBe(original.id);
    expect(store.messages.some(entry => entry.text === 'steer this' && !entry.steering)).toBe(true);
  });

  it('Keeps a queued steer across a profile switch and drains it on return', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesHistoryLoad').mockResolvedValue({ status: 'ok', data: null } as never);
    vi.spyOn(commands, 'hermesHistorySave').mockResolvedValue({ status: 'ok', data: null } as never);
    vi.spyOn(commands, 'hermesModelOptions').mockResolvedValue({
      status: 'ok',
      data: { providers: [], model: null, provider: null }
    } as never);
    let requestId = '';
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(input => {
        requestId = input.requestId;
        return new Promise(() => undefined) as never;
      })
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    let failSteer!: (reason: unknown) => void;
    vi.spyOn(commands, 'hermesSteer').mockImplementation(
      () =>
        new Promise((_, reject) => {
          failSteer = reject;
        }) as never
    );
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(requestId).not.toBe(''));
    const original = store.active!;
    original.liveSessionId = 'live-1';
    original.executionGeneration = 7;

    const steering = store.send('steer this');
    await vi.waitFor(() => expect(commands.hermesSteer).toHaveBeenCalled());
    store.stopThinking();
    store.newChat();
    failSteer(new Error('gateway unavailable'));
    await steering;
    expect(chat).toHaveBeenCalledTimes(1);

    const beforeQueue = original.messages.length;
    let releaseListen!: () => void;
    const listen = eventMock.listen.getMockImplementation();
    expect(listen).toBeDefined();
    eventMock.listen.mockImplementationOnce(
      (event, handler) =>
        new Promise(resolve => {
          releaseListen = () => resolve(listen!(event, handler));
        })
    );

    store.select(original.id);
    await vi.waitFor(() => expect(original.messages.length).toBe(beforeQueue + 2));
    await vi.waitFor(() => expect(releaseListen).toBeDefined());
    await store.switchAgentProfile('work');
    releaseListen();
    await vi.waitFor(() => expect(original.messages.length).toBe(beforeQueue));

    await store.switchAgentProfile('default');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
    expect(store.activeId).toBe(original.id);
    expect(store.messages.some(entry => entry.text === 'steer this' && !entry.steering)).toBe(true);
  });

  it('Requeues a queued steer when the profile has no credentials yet', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesSecretStatus')
      .mockResolvedValueOnce({ status: 'ok', data: false } as never)
      .mockResolvedValue({ status: 'ok', data: true } as never);
    const toasts = useToasts();
    toasts.clear();
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(() => new Promise(() => undefined) as never)
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    let failSteer!: (reason: unknown) => void;
    vi.spyOn(commands, 'hermesSteer').mockImplementation(
      () =>
        new Promise((_, reject) => {
          failSteer = reject;
        }) as never
    );
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    const original = store.active!;
    original.liveSessionId = 'live-1';
    original.executionGeneration = 7;

    const steering = store.send('steer this');
    await vi.waitFor(() => expect(commands.hermesSteer).toHaveBeenCalled());
    store.stopThinking();
    store.newChat();
    failSteer(new Error('gateway unavailable'));
    await steering;
    expect(chat).toHaveBeenCalledTimes(1);

    store.saveModel({ ...store.model, provider: 'openai', model: 'GPT-5.6', endpoint: '' });
    store.select(original.id);
    await vi.waitFor(() => expect(toasts.items.value.some(item => item.title === 'Hermes profile')).toBe(true));
    expect(chat).toHaveBeenCalledTimes(1);
    expect(original.messages.some(entry => entry.text === 'steer this')).toBe(false);

    store.select(original.id);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
    expect(store.messages.some(entry => entry.text === 'steer this' && !entry.steering)).toBe(true);
  });

  it('Arms stop fallbacks when steering is rejected', async () => {
    vi.useFakeTimers();
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesChat').mockImplementation(() => new Promise(() => undefined) as never);
    const steer = vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('does not support steer'));
    vi.spyOn(commands, 'hermesInterrupt').mockImplementation(() => new Promise(() => undefined) as never);
    vi.spyOn(commands, 'hermesStop').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    expect(store.thinking).toBe(true);
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;
    void store.send('steer this');
    for (let attempt = 0; attempt < 8 && !store.stopping; attempt += 1) await Promise.resolve();
    expect(steer).toHaveBeenCalledWith('live-1', 7, 'steer this', null, null);
    expect(store.stopping).toBe(true);
    vi.advanceTimersByTime(10_000);
    expect(store.thinking).toBe(false);
    expect(store.stopping).toBe(false);
  });

  it('Retries a stale steer once with the newer generation and leaves the turn running', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesChat').mockImplementation(() => new Promise(() => undefined) as never);
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    const steer = vi.spyOn(commands, 'hermesSteer').mockImplementation(((_live: string, generation: number) => {
      if (generation === 7) {
        store.active!.executionGeneration = 8;
        return Promise.reject(new Error('stale_generation'));
      }
      return Promise.resolve({ status: 'ok', data: { status: 'queued', delivered: null } });
    }) as never);
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    expect(store.thinking).toBe(true);
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;

    await store.send('steer this');

    expect(steer).toHaveBeenNthCalledWith(1, 'live-1', 7, 'steer this', null, null);
    expect(steer).toHaveBeenNthCalledWith(2, 'live-1', 8, 'steer this', null, null);
    expect(interrupt).not.toHaveBeenCalled();
    expect(store.stopping).toBe(false);
    expect(store.thinking).toBe(true);
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(true);
  });

  it('Queues a stale steer without interrupting the running turn', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let finishTurn!: (value: unknown) => void;
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishTurn = resolve;
          }) as never
      )
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('stale_generation'));
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;

    await store.send('steer this');

    expect(interrupt).not.toHaveBeenCalled();
    expect(store.stopping).toBe(false);
    expect(store.thinking).toBe(true);
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(false);

    finishTurn({ status: 'ok', data: { text: 'done', sessionId: null, liveSessionId: null, usage: null } });
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
  });

  it('Leaves the running turn alone when a steer fails for another reason', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesChat').mockImplementation(() => new Promise(() => undefined) as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('gateway unavailable'));
    const interrupt = vi.spyOn(commands, 'hermesInterrupt').mockResolvedValue({ status: 'ok', data: null } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;

    await store.send('steer this');

    expect(interrupt).not.toHaveBeenCalled();
    expect(store.stopping).toBe(false);
    expect(store.thinking).toBe(true);
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(false);
  });

  it('Starts one queued chat at a time while a credential check is in flight', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let releaseKey!: (value: { status: 'ok'; data: boolean }) => void;
    let keyCalls = 0;
    vi.spyOn(commands, 'hermesSecretStatus').mockImplementation(() => {
      keyCalls += 1;
      if (keyCalls === 1) return Promise.resolve({ status: 'ok', data: true });
      return new Promise(resolve => {
        releaseKey = resolve;
      });
    });
    let finishTurn!: (value: unknown) => void;
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishTurn = resolve;
          }) as never
      )
      .mockResolvedValue({
        status: 'ok',
        data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
      } as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('stale_generation'));
    const store = useHermesStore();
    await store.loadRuntime();
    store.saveModel({ ...store.model, provider: 'openai', model: 'GPT-5.6', endpoint: '' });
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;
    await store.send('first queued');
    await store.send('second queued');

    finishTurn({ status: 'ok', data: { text: 'done', sessionId: null, liveSessionId: null, usage: null } });
    await vi.waitFor(() => expect(keyCalls).toBe(2));
    store.select(store.activeId!);
    expect(keyCalls).toBe(2);
    expect(chat).toHaveBeenCalledTimes(1);

    releaseKey({ status: 'ok', data: true });
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'first queued' });
    expect(chat).toHaveBeenCalledTimes(2);
  });

  it('Writes the assistant reply onto the conversation restored while send waited', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    let releaseHistory!: (value: unknown) => void;
    vi.spyOn(commands, 'hermesHistoryLoad').mockImplementation(
      () =>
        new Promise(resolve => {
          releaseHistory = resolve;
        }) as never
    );
    vi.spyOn(commands, 'hermesHistorySave').mockResolvedValue({ status: 'ok', data: null } as never);
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    const chat = vi.spyOn(commands, 'hermesChat').mockResolvedValue({
      status: 'ok',
      data: { text: 'assistant reply', sessionId: 'sess-1', liveSessionId: 'live-9', usage: null }
    } as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.conversations.push({
      id: 'chat-1',
      messages: [{ id: 'earlier', role: 'user', text: 'earlier', at: 1 }],
      updatedAt: 1
    });
    store.activeId = 'chat-1';

    const loading = store.loadHistory();
    await vi.waitFor(() => expect(releaseHistory).toBeTypeOf('function'));
    const sending = store.send('hello from user');
    releaseHistory({
      status: 'ok',
      data: JSON.stringify({
        conversations: [
          {
            id: 'chat-1',
            messages: [{ id: 'earlier', role: 'user', text: 'earlier', at: 1 }],
            updatedAt: 9_999_999_999_999,
            sessionId: 'old-session'
          }
        ],
        activeId: 'chat-1'
      })
    });
    await loading;
    await sending;

    expect(chat).toHaveBeenCalledWith(expect.objectContaining({ sessionId: 'old-session' }));
    expect(store.active?.id).toBe('chat-1');
    expect(store.active?.sessionId).toBe('sess-1');
    expect(store.active?.liveSessionId).toBe('live-9');
    expect(store.messages.map(entry => entry.text)).toEqual(['earlier', 'hello from user', 'assistant reply']);
    expect(store.conversations.filter(item => item.id === 'chat-1')).toHaveLength(1);
  });

  it('Continues the queue after stopThinking once the superseded chat finishes', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let finishInitial!: (value: unknown) => void;
    let finishQueued!: (value: unknown) => void;
    const settled = {
      status: 'ok',
      data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
    };
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishInitial = resolve;
          }) as never
      )
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishQueued = resolve;
          }) as never
      )
      .mockResolvedValue(settled as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('stale_generation'));
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;
    await store.send('first queued');
    await store.send('second queued');

    finishInitial(settled);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'first queued' });

    store.stopThinking();
    expect(chat).toHaveBeenCalledTimes(2);
    finishQueued(settled);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(3));
    expect(chat.mock.calls[2]?.[0]).toMatchObject({ prompt: 'second queued' });
  });

  it('Lets select start a queued chat while the superseded request stays hung', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let finishInitial!: (value: unknown) => void;
    const settled = {
      status: 'ok',
      data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
    };
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishInitial = resolve;
          }) as never
      )
      .mockImplementationOnce(() => new Promise(() => undefined) as never)
      .mockResolvedValue(settled as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('stale_generation'));
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;
    await store.send('first queued');
    await store.send('second queued');

    finishInitial(settled);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    store.stopThinking();
    await Promise.resolve();
    expect(chat).toHaveBeenCalledTimes(2);

    store.select(store.activeId!);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(3));
    expect(chat.mock.calls[2]?.[0]).toMatchObject({ prompt: 'second queued' });
  });

  it('Sends a steer that was still waiting for a generation after stop and select', async () => {
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    const settled = {
      status: 'ok',
      data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
    };
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(() => new Promise(() => undefined) as never)
      .mockResolvedValue(settled as never);
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    await store.send('steer this');
    expect(store.messages.some(entry => entry.text === 'steer this' && entry.steering)).toBe(true);

    store.stopThinking();
    await Promise.resolve();
    expect(chat).toHaveBeenCalledTimes(1);
    expect(store.messages.some(entry => entry.steering)).toBe(false);

    store.select(store.activeId!);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'steer this' });
    expect(store.messages.some(entry => entry.text === 'steer this' && !entry.steering)).toBe(true);
  });

  it('Restores a queued message when the runtime is not ready and sends it on the next select', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    let finishInitial!: (value: unknown) => void;
    const settled = {
      status: 'ok',
      data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
    };
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishInitial = resolve;
          }) as never
      )
      .mockResolvedValue(settled as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('stale_generation'));
    const store = useHermesStore();
    await store.loadRuntime();
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;
    await store.send('queued text');

    store.runtime = { ...store.runtime!, ready: false };
    finishInitial(settled);
    await vi.waitFor(() => expect(store.thinking).toBe(false));
    expect(chat).toHaveBeenCalledTimes(1);
    expect(store.messages.some(entry => entry.text === 'queued text')).toBe(false);

    store.runtime = { ...store.runtime!, ready: true };
    store.select(store.activeId!);
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'queued text' });
  });

  it('Continues a credential-blocked queue when the model becomes usable', async () => {
    Object.defineProperty(window, '__TAURI_INTERNALS__', { value: {}, configurable: true });
    vi.spyOn(commands, 'hermesRuntimeStatus').mockResolvedValue({
      status: 'ok',
      data: {
        ready: true,
        installed: true,
        version: 'Hermes Agent v0.19.0',
        expectedVersion: '0.19.0',
        path: '/tmp/hermes',
        dataPath: '/tmp/OpenNavoData',
        computerUse: true,
        outdated: false,
        message: null
      }
    });
    vi.spyOn(commands, 'hermesSecretStatus')
      .mockResolvedValueOnce({ status: 'ok', data: true } as never)
      .mockResolvedValueOnce({ status: 'ok', data: false } as never)
      .mockResolvedValue({ status: 'ok', data: true } as never);
    const toasts = useToasts();
    toasts.clear();
    let finishInitial!: (value: unknown) => void;
    const settled = {
      status: 'ok',
      data: { text: 'done', sessionId: null, liveSessionId: null, usage: null }
    };
    const chat = vi
      .spyOn(commands, 'hermesChat')
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finishInitial = resolve;
          }) as never
      )
      .mockResolvedValue(settled as never);
    vi.spyOn(commands, 'hermesSteer').mockRejectedValue(new Error('stale_generation'));
    const store = useHermesStore();
    await store.loadRuntime();
    store.saveModel({ ...store.model, provider: 'openai', model: 'GPT-5.6', endpoint: '' });
    store.newChat();
    void store.send('initial turn');
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(1));
    store.active!.liveSessionId = 'live-1';
    store.active!.executionGeneration = 7;
    await store.send('after key');

    finishInitial(settled);
    await vi.waitFor(() => expect(toasts.items.value.some(item => item.title === 'Hermes profile')).toBe(true));
    expect(chat).toHaveBeenCalledTimes(1);
    expect(store.messages.some(entry => entry.text === 'after key')).toBe(false);

    // The settings sheet resets the session as soon as a key is typed, then saves the model.
    store.resetSession();
    store.saveModel({ ...store.model, keySet: true });
    await vi.waitFor(() => expect(chat).toHaveBeenCalledTimes(2));
    expect(chat.mock.calls[1]?.[0]).toMatchObject({ prompt: 'after key' });
    expect(store.activeId).toBeTruthy();
  });

  it('Stops Mac control on panic', () => {
    const store = useHermesStore();
    store.stopMac();
    expect(store.mac.enabled).toBe(false);
    expect(store.macStatus).toBe('paused');
  });
});

describe('Hermes credential state', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('Re-reads the stored key instead of trusting a persisted keySet', async () => {
    const status = vi.spyOn(commands, 'hermesSecretStatus').mockResolvedValue({ status: 'ok', data: false });
    const store = useHermesStore();
    store.useProvider('openai');
    store.model = { ...store.model, keySet: true };

    await expect(store.refreshCredentialState()).resolves.toBe(false);
    expect(status).toHaveBeenCalledWith('openai', null);
    expect(store.model.keySet).toBe(false);
  });

  it('Records a key that the runtime reports as stored', async () => {
    vi.spyOn(commands, 'hermesSecretStatus').mockResolvedValue({ status: 'ok', data: true });
    const store = useHermesStore();
    store.useProvider('anthropic');

    await expect(store.refreshCredentialState()).resolves.toBe(true);
    expect(store.model.keySet).toBe(true);
  });

  it('Never asks for a key on an OAuth provider', async () => {
    const status = vi.spyOn(commands, 'hermesSecretStatus');
    const store = useHermesStore();
    store.useProvider('nous');

    await expect(store.refreshCredentialState()).resolves.toBe(false);
    expect(status).not.toHaveBeenCalled();
    expect(store.model.keySet).toBe(false);
  });

  it('Only requires a stored key for providers that need one', () => {
    const store = useHermesStore();
    const needs = (provider: 'openai' | 'nous' | 'codex' | 'local' | 'custom', endpoint = '') =>
      store.requiresStoredKey({ ...store.model, provider, endpoint }, HERMES_PROVIDER_MAP[provider]);

    expect(needs('openai')).toBe(true);
    // Account logins and local endpoints authenticate without a stored key.
    expect(needs('nous')).toBe(false);
    expect(needs('codex')).toBe(false);
    expect(needs('local', 'http://127.0.0.1:11434')).toBe(false);
    // A custom endpoint needs one only until its URL is set.
    expect(needs('custom')).toBe(true);
    expect(needs('custom', 'https://example.test/v1')).toBe(false);
  });
});

describe('Hermes provider slugs', () => {
  it('Maps the local and custom cards to the provider OpenNavo actually configures', () => {
    // `write_config()` registers one user-defined provider, `providers.openavoo-custom`.
    // Mapping these cards to the canonical `custom` slug matched an always-present empty
    // skeleton row, which is why their model list stayed empty.
    expect(hermesProviderSlug('custom')).toBe('openavoo-custom');
    expect(hermesProviderSlug('local')).toBe('openavoo-custom');
  });

  it('Keeps the canonical slugs for the vendor cards', () => {
    expect(hermesProviderSlug('openai')).toBe('openai-api');
    expect(hermesProviderSlug('google')).toBe('gemini');
    expect(hermesProviderSlug('codex')).toBe('openai-codex');
    expect(hermesProviderSlug('claude-code')).toBe('anthropic');
    expect(hermesProviderSlug('opencode')).toBe('opencode-zen');
    expect(hermesProviderSlug('nous')).toBe('nous');
  });
});

describe('Hermes official provider picker', () => {
  it('Picks the user-defined row on this Mac instead of the canonical skeleton', () => {
    // Shape observed locally: the canonical `custom` row is always present and always empty.
    const rows = [
      { slug: 'custom', is_current: false, authenticated: false, models: [] },
      { slug: 'openavoo-custom', is_current: true, authenticated: true, models: ['grok-4.7'] }
    ];
    expect(pickOfficialProvider(rows, 'custom')?.slug).toBe('openavoo-custom');
    expect(pickOfficialProvider(rows, 'local')?.slug).toBe('openavoo-custom');
  });

  it('Picks the scoped endpoint row on a Hermes Panel host', () => {
    // Shape observed on a panel host: `custom` is the configured-but-unauthenticated
    // provider (one saved model plus the "not authenticated" warning), while the real
    // endpoint is registered as `custom:newapi` with 14 models.
    const rows = [
      {
        slug: 'custom',
        is_current: true,
        authenticated: false,
        models: ['grok-4.7'],
        warning:
          'Configured provider is not authenticated; run `hermes model` to reactivate. Showing the saved model only.'
      },
      {
        slug: 'custom:newapi',
        is_current: true,
        authenticated: true,
        models: ['grok-4.6', 'grok-4.3', 'gpt-6-sol']
      }
    ];
    const picked = pickOfficialProvider(rows, 'custom');
    expect(picked?.slug).toBe('custom:newapi');
    expect(picked?.models).toHaveLength(3);
  });

  it('Falls back to the current row when nothing can list models', () => {
    const rows = [
      { slug: 'custom', is_current: true, authenticated: false, models: ['grok-4.7'] },
      { slug: 'custom:other', is_current: false, authenticated: false, models: [] }
    ];
    expect(pickOfficialProvider(rows, 'custom')?.slug).toBe('custom');
  });

  it('Returns undefined when the payload has no row for the card', () => {
    expect(pickOfficialProvider([{ slug: 'nous', models: ['x'] }], 'custom')).toBeUndefined();
  });
});
