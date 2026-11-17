// @vitest-environment jsdom
import { effectScope, nextTick, reactive, ref, watch } from 'vue';
import type { Ref } from 'vue';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { launchDeepLink } = vi.hoisted(() => ({ launchDeepLink: vi.fn() }));

vi.mock('../app/utils/deepLink', () => ({ launchDeepLink }));

import { useClientHandoff } from '../app/composables/useClientHandoff';

const states = new Map<string, Ref<unknown>>();
const route = reactive({ fullPath: '/monitor' });
const copy = vi.fn();

beforeEach(() => {
  states.clear();
  route.fullPath = '/monitor';
  launchDeepLink.mockImplementation(() => {});
  vi.useFakeTimers();
  vi.stubGlobal('useState', <T>(key: string, init: () => T): Ref<T> => {
    if (!states.has(key)) states.set(key, ref(init()));
    return states.get(key) as Ref<T>;
  });
  vi.stubGlobal('useRoute', () => route);
  vi.stubGlobal('watch', watch);
  vi.stubGlobal('useCopyCommand', () => ({ copy }));
});

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  launchDeepLink.mockReset();
  copy.mockReset();
});

describe('client handoff state', () => {
  it('treats a synchronous launch failure as a missing client instead of leaving opening stuck', () => {
    launchDeepLink.mockImplementation(() => {
      throw new Error('unregistered protocol');
    });
    const scope = effectScope();
    const handoff = scope.run(() => useClientHandoff())!;

    handoff.open({ url: 'opennavo://monitor', kind: 'route', feature: 'monitor' });

    expect(handoff.opening.value).toBe(false);
    expect(handoff.prompt.value?.url).toBe('opennavo://monitor');
    scope.stop();
  });

  it('cancels a pending handoff when the route changes', async () => {
    const scope = effectScope();
    const handoff = scope.run(() => useClientHandoff())!;

    handoff.open({ url: 'opennavo://monitor', kind: 'route', feature: 'monitor' });
    expect(handoff.opening.value).toBe(true);

    route.fullPath = '/download';
    await nextTick();
    expect(handoff.opening.value).toBe(false);

    vi.advanceTimersByTime(3000);
    expect(handoff.prompt.value).toBeNull();
    scope.stop();
  });
});
