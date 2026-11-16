import { createPinia, setActivePinia } from 'pinia';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { i18n } from '@/i18n';
import {
  HERMES_NOTIFICATION_BODY_MAX,
  clipHermesNotificationBody,
  hermesWindowIsBackgrounded,
  notifyHermesReply
} from '@/hermes/notifications';
import { IpcError, commands } from '@/ipc/client';
import { useHermesStore } from '@/stores/hermes';
import { HERMES_NOTIFICATION_STORAGE, useHermesNotificationsStore } from '@/stores/hermesNotifications';

vi.mock('@tauri-apps/api/event', () => ({
  listen: vi.fn(async () => () => undefined)
}));

const PROFILES_KEY = 'ONV_HERMES_PROFILES_V1';
const INITIAL_LOCALE = i18n.global.locale.value;

function setWindowState(focused: boolean, hidden: boolean) {
  Object.defineProperty(document, 'hidden', { configurable: true, value: hidden });
  Object.defineProperty(document, 'hasFocus', { configurable: true, value: () => focused });
}

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

function mockTauri() {
  Object.defineProperty(window, '__TAURI_INTERNALS__', { configurable: true, value: {} });
}

function mockChatReply(text = 'Remote reply') {
  return vi.spyOn(commands, 'hermesChat').mockResolvedValue({
    status: 'ok',
    data: { text, sessionId: 'panel-session', liveSessionId: null, usage: null }
  });
}

beforeEach(() => {
  localStorage.clear();
  setActivePinia(createPinia());
  mockTauri();
  setWindowState(false, true);
  i18n.global.locale.value = 'en-US';
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  localStorage.clear();
  Reflect.deleteProperty(window, '__TAURI_INTERNALS__');
  i18n.global.locale.value = INITIAL_LOCALE;
});

describe('Hermes notification copy and focus gate', () => {
  it('prefers the reply and truncates it to 140 characters', () => {
    expect(clipHermesNotificationBody('Build is green.', 'Message complete.')).toBe('Build is green.');
    expect(clipHermesNotificationBody('a'.repeat(200), 'Message complete.')).toHaveLength(HERMES_NOTIFICATION_BODY_MAX);
    expect(clipHermesNotificationBody('   ', 'Message complete.')).toBe('Message complete.');
  });

  it('keeps an emoji that crosses the 140-character boundary intact', () => {
    const emoji = '😀';
    const clipped = clipHermesNotificationBody(
      `${'a'.repeat(HERMES_NOTIFICATION_BODY_MAX - 1)}${emoji}tail`,
      'Message complete.'
    );

    expect(clipped).toBe(`${'a'.repeat(HERMES_NOTIFICATION_BODY_MAX - 1)}${emoji}`);
    expect(Array.from(clipped)).toHaveLength(HERMES_NOTIFICATION_BODY_MAX);
  });

  it('treats a hidden or unfocused window as backgrounded', () => {
    setWindowState(true, false);
    expect(hermesWindowIsBackgrounded()).toBe(false);

    setWindowState(false, false);
    expect(hermesWindowIsBackgrounded()).toBe(true);

    setWindowState(true, true);
    expect(hermesWindowIsBackgrounded()).toBe(true);
  });

  it('does not call the native bridge while the window is focused', async () => {
    setWindowState(true, false);
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });

    await expect(notifyHermesReply('turnDone', 'focused-chat', 'reply')).resolves.toBe(false);
    expect(notify).not.toHaveBeenCalled();
  });

  it('uses the localized fallback for an empty reply', async () => {
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });

    await expect(notifyHermesReply('turnDone', 'empty-chat', '   ')).resolves.toBe(true);
    expect(notify).toHaveBeenCalledWith('empty-chat', 'Hermes finished', 'Message complete.');
  });

  it('throttles a repeated kind and conversation for one second', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-16T00:00:00Z'));
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });

    await expect(notifyHermesReply('turnDone', 'throttled-chat', 'first')).resolves.toBe(true);
    await expect(notifyHermesReply('turnDone', 'throttled-chat', 'second')).resolves.toBe(false);
    expect(notify).toHaveBeenCalledTimes(1);

    vi.advanceTimersByTime(1001);
    await expect(notifyHermesReply('turnDone', 'throttled-chat', 'third')).resolves.toBe(true);
    expect(notify).toHaveBeenCalledTimes(2);
  });
});

describe('Hermes notification preferences', () => {
  it('defaults on and persists each switch independently', () => {
    const prefs = useHermesNotificationsStore();
    expect(prefs.prefs).toEqual({ enabled: true, turnDone: true, turnError: true });

    prefs.setKind('turnDone', false);
    prefs.setEnabled(false);

    expect(JSON.parse(localStorage.getItem(HERMES_NOTIFICATION_STORAGE) ?? '{}')).toEqual({
      enabled: false,
      turnDone: false,
      turnError: true
    });
  });
});

describe('Hermes reply notifications', () => {
  beforeEach(() => {
    seedPanelProfile();
  });

  it('notifies the active conversation after a successful background turn', async () => {
    mockChatReply();
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });
    const store = useHermesStore();

    await store.send('finish this');

    await vi.waitFor(() => expect(notify).toHaveBeenCalledWith(store.activeId, 'Hermes finished', 'Remote reply'));
  });

  it('notifies the active conversation after a failed background turn', async () => {
    vi.spyOn(commands, 'hermesChat').mockRejectedValue(
      new IpcError({
        code: 'E_HERMES_GATEWAY_STREAM',
        message: 'Provider failed',
        detail: 'Provider returned an error'
      })
    );
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });
    const store = useHermesStore();

    await store.send('fail this');

    await vi.waitFor(() =>
      expect(notify).toHaveBeenCalledWith(store.activeId, 'Turn failed', 'Provider returned an error')
    );
  });

  it('does not notify when the reply belongs to a conversation no longer on screen', async () => {
    const pending: Array<(value: unknown) => void> = [];
    vi.spyOn(commands, 'hermesChat').mockImplementation(
      () =>
        new Promise(resolve => {
          pending.push(resolve);
        }) as never
    );
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });
    const store = useHermesStore();

    const turn = store.send('leave this conversation');
    await vi.waitFor(() => expect(pending).toHaveLength(1));
    store.newChat();
    pending[0]!({
      status: 'ok',
      data: { text: 'Late reply', sessionId: 'panel-session', liveSessionId: null, usage: null }
    });
    await turn;

    expect(notify).not.toHaveBeenCalled();
  });

  it('honors the per-kind switch', async () => {
    mockChatReply();
    const notify = vi.spyOn(commands, 'hermesNotifyReply').mockResolvedValue({ status: 'ok', data: null });
    const store = useHermesStore();
    useHermesNotificationsStore().setKind('turnDone', false);

    await store.send('do not notify');

    expect(notify).not.toHaveBeenCalled();
  });
});
