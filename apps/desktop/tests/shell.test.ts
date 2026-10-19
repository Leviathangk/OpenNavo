import { flushPromises, mount } from '@vue/test-utils';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { effectScope, ref } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import { routes } from '@/router';
import { refreshPage } from '@/composables/usePageRefresh';
import { useLoader } from '@/composables/useLoader';
import mainCapability from '../src-tauri/capabilities/main.json';
import AppShell from '@/layouts/AppShell.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/bindings';
import { startLocalState } from '@/stores';
import { mockMatchMedia, setup } from './helpers';

let wide = true;

beforeAll(() => {
  mockMatchMedia(() => wide);
});

async function mountShell(path: string) {
  const { pinia, router } = await setup(path);
  await startLocalState();
  const wrapper = mount(AppShell, {
    global: { plugins: [pinia, router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  return { wrapper, router };
}

describe('AppShell', () => {
  it('Sidebar links are focusable/current; installed counts and update badges display', async () => {
    const { wrapper } = await mountShell('/discover');
    const links = wrapper.findAll('aside nav a, aside > div > a');
    expect(links.map(link => link.attributes('href'))).toEqual([
      '/discover',
      '/categories',
      '/rankings',
      '/recent',
      '/articles',
      '/collections',
      '/brewfile',
      // Tools: Scan for Viruses, then the app firewall right below it, then Monitor.
      '/security',
      '/firewall',
      '/monitor',
      '/installed',
      '/updates',
      '/cleanup',
      '/settings'
    ]);
    expect(links[0]?.attributes('aria-current')).toBe('page');
    expect(links[1]?.attributes('aria-current')).toBeUndefined();
    // Installed counts every app on the Mac (uninstall decision 3): the 8 simulated casks plus apps from other sources;
    // command-line tools stay excluded (ADR-018).
    const local = await commands.localAppsList();
    const count = local.status === 'ok' ? local.data.length : -1;
    expect(count).toBeGreaterThan(8);
    expect(links.find(link => link.attributes('href') === '/installed')?.text()).toContain(String(count));
    expect(links.find(link => link.attributes('href') === '/updates')?.text()).toContain('4');
    wrapper.unmount();
  });

  it('Sidebar omits Homebrew source module', async () => {
    const { wrapper } = await mountShell('/discover');
    const sidebar = wrapper.get('aside');
    expect(sidebar.find('section').exists()).toBe(false);
    expect(sidebar.text()).not.toContain('HOMEBREW');
    expect(sidebar.text()).not.toContain('7.0.7 · arm64');
    expect(sidebar.text()).not.toContain('清华 TUNA 镜像');
    wrapper.unmount();
  });

  it('Sidebar top/toolbar drag with main-window permissions; buttons are not draggable', async () => {
    const { wrapper } = await mountShell('/discover');
    const regions = wrapper.findAll('[data-tauri-drag-region]');
    expect(regions.length).toBeGreaterThanOrEqual(2);
    expect(regions[0]?.classes()).toContain('h-52px');
    expect(wrapper.find('header[data-tauri-drag-region]').exists()).toBe(true);
    expect(wrapper.find('header [data-tauri-drag-region]').classes()).toContain('flex-1');
    expect(wrapper.findAll('header button[data-tauri-drag-region], header a[data-tauri-drag-region]')).toHaveLength(0);
    // DOM checks alone cannot detect native requests denied by ACLs.
    expect(mainCapability.windows).toEqual(['main']);
    expect(mainCapability.permissions).toContain('core:window:allow-start-dragging');
    wrapper.unmount();
  });

  it('Pages with rails use named views; Settings omits rail; browser mode draws traffic lights', async () => {
    const discover = await mountShell('/discover');
    expect(discover.wrapper.text()).toContain('本机 Homebrew 状态');
    expect(discover.wrapper.find('.bg-macos-close').exists()).toBe(true);
    discover.wrapper.unmount();
    const settings = await mountShell('/settings');
    expect(settings.wrapper.text()).not.toContain('本机 Homebrew 状态');
    settings.wrapper.unmount();
  });

  it('Narrow windows collapse rail into Overview', async () => {
    wide = false;
    const { wrapper } = await mountShell('/updates');
    expect(wrapper.text()).not.toContain('本机 Homebrew 状态');
    const toggle = wrapper.findAll('button').find(button => button.text() === '概览');
    expect(toggle).toBeDefined();
    await toggle?.trigger('click');
    expect(wrapper.text()).toContain('本机 Homebrew 状态');
    wide = true;
    wrapper.unmount();
  });

  it('Command-K opens palette', async () => {
    const { wrapper } = await mountShell('/discover');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', metaKey: true }));
    await flushPromises();
    expect(document.body.querySelector('[role="combobox"]')).not.toBeNull();
    wrapper.unmount();
  });
});

describe('IPC simulation', () => {
  it('Browser commands return fixture data', async () => {
    await setup();
    const result = await commands.appInfo();
    expect(result).toEqual({
      status: 'ok',
      data: { version: '0.0.0', os: 'macos', arch: 'aarch64' }
    });
  });
});

describe('Recovery events survive throttling', () => {
  it('Network restoration one second after failed offline activation schedules retry without leaving the window', async () => {
    const { wrapper } = await mountShell('/discover');
    const scope = effectScope();
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValue('recovered');
    const loader = scope.run(() => useLoader(fetcher))!;
    await flushPromises();
    vi.useFakeTimers();
    try {
      window.dispatchEvent(new Event('focus'));
      await flushPromises();
      expect(fetcher).toHaveBeenCalledTimes(2);
      await vi.advanceTimersByTimeAsync(1_000);
      window.dispatchEvent(new Event('online'));
      window.dispatchEvent(new Event('focus'));
      document.dispatchEvent(new Event('visibilitychange'));
      await vi.advanceTimersByTimeAsync(3_999);
      expect(fetcher).toHaveBeenCalledTimes(2);
      await vi.advanceTimersByTimeAsync(1);
      expect(fetcher).toHaveBeenCalledTimes(3);
      expect(loader.data.value).toBe('recovered');
      await vi.advanceTimersByTimeAsync(10_000);
      expect(fetcher).toHaveBeenCalledTimes(3);
    } finally {
      wrapper.unmount();
      scope.stop();
      vi.useRealTimers();
    }
  });

  it('Online during an old request triggers recovery after it completes', async () => {
    const { wrapper } = await mountShell('/discover');
    const scope = effectScope();
    let fail!: (error: Error) => void;
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementationOnce(
        () =>
          new Promise<string>((_resolve, reject) => {
            fail = reject;
          })
      )
      .mockResolvedValue('recovered');
    const loader = scope.run(() => useLoader(fetcher))!;
    await flushPromises();
    vi.useFakeTimers();
    try {
      window.dispatchEvent(new Event('focus'));
      await vi.advanceTimersByTimeAsync(1_000);
      window.dispatchEvent(new Event('online'));
      await vi.advanceTimersByTimeAsync(5_000);
      expect(fetcher).toHaveBeenCalledTimes(2);
      fail(new Error('old offline request'));
      await flushPromises();
      expect(fetcher).toHaveBeenCalledTimes(3);
      expect(loader.data.value).toBe('recovered');
    } finally {
      wrapper.unmount();
      scope.stop();
      vi.useRealTimers();
    }
  });

  it('Unmounting shell cancels pending recovery timers', async () => {
    const { wrapper } = await mountShell('/discover');
    const scope = effectScope();
    const fetcher = vi.fn().mockRejectedValue(new Error('offline'));
    scope.run(() => useLoader(fetcher));
    await flushPromises();
    vi.useFakeTimers();
    try {
      window.dispatchEvent(new Event('focus'));
      await flushPromises();
      await vi.advanceTimersByTimeAsync(1_000);
      window.dispatchEvent(new Event('online'));
      wrapper.unmount();
      await vi.advanceTimersByTimeAsync(10_000);
      expect(fetcher).toHaveBeenCalledTimes(2);
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      scope.stop();
      vi.useRealTimers();
    }
  });
});

it('Hidden-window recovery remains pending until visible', async () => {
  const { wrapper } = await mountShell('/discover');
  const scope = effectScope();
  let visible = true;
  const visibility = vi
    .spyOn(document, 'visibilityState', 'get')
    .mockImplementation(() => (visible ? 'visible' : 'hidden'));
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error('offline'))
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue('recovered');
  const loader = scope.run(() => useLoader(fetcher))!;
  await flushPromises();
  vi.useFakeTimers();
  try {
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    await vi.advanceTimersByTimeAsync(1_000);
    window.dispatchEvent(new Event('online'));
    visible = false;
    document.dispatchEvent(new Event('visibilitychange'));
    await vi.advanceTimersByTimeAsync(5_000);
    expect(fetcher).toHaveBeenCalledTimes(2);
    visible = true;
    document.dispatchEvent(new Event('visibilitychange'));
    await flushPromises();
    expect(loader.data.value).toBe('recovered');
    expect(fetcher).toHaveBeenCalledTimes(3);
  } finally {
    wrapper.unmount();
    scope.stop();
    visibility.mockRestore();
    vi.useRealTimers();
  }
});

it('Old-page recovery does not block new-page pending network recovery', async () => {
  const { wrapper, router } = await mountShell('/discover');
  // jsdom does not implement element scrolling; provide the browser method needed for route changes.
  Object.defineProperty(wrapper.get('main').element, 'scrollTo', {
    value: vi.fn(),
    configurable: true
  });
  const oldScope = effectScope();
  const newScope = effectScope();
  let fail!: (error: Error) => void;
  const oldFetch = vi
    .fn()
    .mockRejectedValueOnce(new Error('offline'))
    .mockImplementationOnce(
      () =>
        new Promise<string>((_resolve, reject) => {
          fail = reject;
        })
    );
  oldScope.run(() => useLoader(oldFetch));
  await flushPromises();
  vi.useFakeTimers();
  try {
    window.dispatchEvent(new Event('focus'));
    await vi.advanceTimersByTimeAsync(1_000);
    window.dispatchEvent(new Event('online'));
    oldScope.stop();
    const newFetch = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue('new page recovered');
    const loader = newScope.run(() => useLoader(newFetch))!;
    await router.push('/categories');
    await flushPromises();
    await vi.advanceTimersByTimeAsync(4_000);
    expect(loader.data.value).toBe('new page recovered');
    expect(newFetch).toHaveBeenCalledTimes(2);
    fail(new Error('late old failure'));
    await flushPromises();
    expect(loader.data.value).toBe('new page recovered');
  } finally {
    wrapper.unmount();
    oldScope.stop();
    newScope.stop();
    vi.useRealTimers();
  }
});

it.each(['initial', 'manual'] as const)(
  'First online event is not consumed by an in-flight %s request',
  async origin => {
    const { wrapper } = await mountShell('/discover');
    const scope = effectScope();
    let fail!: (error: Error) => void;
    const fetcher = vi.fn<() => Promise<string>>();
    if (origin === 'manual') fetcher.mockResolvedValueOnce('cached');
    fetcher
      .mockImplementationOnce(
        () =>
          new Promise((_resolve, reject) => {
            fail = reject;
          })
      )
      .mockResolvedValue('recovered');
    const loader = scope.run(() => useLoader(fetcher))!;
    await flushPromises();
    const manual = origin === 'manual' ? refreshPage() : undefined;
    vi.useFakeTimers();
    try {
      window.dispatchEvent(new Event('online'));
      await vi.advanceTimersByTimeAsync(6_000);
      fail(new Error('old request failed'));
      await flushPromises();
      await manual;
      expect(loader.data.value).toBe('recovered');
      expect(fetcher).toHaveBeenCalledTimes(origin === 'manual' ? 3 : 2);
      await vi.advanceTimersByTimeAsync(30_000);
      expect(fetcher).toHaveBeenCalledTimes(origin === 'manual' ? 3 : 2);
    } finally {
      wrapper.unmount();
      scope.stop();
      vi.useRealTimers();
    }
  }
);

it.each(['locale', 'filter'])(
  'Same-path %s switches let new resources recover without waiting for old requests',
  async () => {
    const { wrapper, router } = await mountShell('/discover');
    const scope = effectScope();
    const identity = ref('a');
    let failOld!: (error: Error) => void;
    const fetcher = vi
      .fn()
      .mockRejectedValueOnce(new Error('offline'))
      .mockImplementationOnce(
        () =>
          new Promise<string>((_resolve, reject) => {
            failOld = reject;
          })
      )
      .mockRejectedValueOnce(new Error('new resource offline'))
      .mockResolvedValue('new recovered');
    const loader = scope.run(() => useLoader(fetcher, [identity]))!;
    await flushPromises();
    vi.useFakeTimers();
    try {
      window.dispatchEvent(new Event('focus'));
      await vi.advanceTimersByTimeAsync(1_000);
      identity.value = 'b';
      await flushPromises();
      window.dispatchEvent(new Event('online'));
      await vi.advanceTimersByTimeAsync(5_000);
      expect(router.currentRoute.value.path).toBe('/discover');
      expect(loader.data.value).toBe('new recovered');
      failOld(new Error('late old failure'));
      await flushPromises();
      expect(loader.data.value).toBe('new recovered');
    } finally {
      wrapper.unmount();
      scope.stop();
      vi.useRealTimers();
    }
  }
);

it('One hung resource cannot block another from handling first online', async () => {
  const { wrapper } = await mountShell('/discover');
  const scope = effectScope();
  let finish!: (value: string) => void;
  scope.run(() =>
    useLoader(
      () =>
        new Promise<string>(resolve => {
          finish = resolve;
        })
    )
  );
  const fetcher = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValue('recovered');
  const loader = scope.run(() => useLoader(fetcher))!;
  await flushPromises();
  try {
    window.dispatchEvent(new Event('online'));
    await flushPromises();
    expect(loader.data.value).toBe('recovered');
    finish('slow success');
    await flushPromises();
  } finally {
    wrapper.unmount();
    scope.stop();
  }
});

it.each([true, false])(
  'After waiting for old requests, recover only necessary resources; old request success=%s',
  async success => {
    const { wrapper } = await mountShell('/discover');
    const scope = effectScope();
    let resolve!: (value: string) => void;
    let reject!: (error: Error) => void;
    const fetcher = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<string>((ok, fail) => {
            resolve = ok;
            reject = fail;
          })
      )
      .mockRejectedValue(new Error('still offline'));
    const loader = scope.run(() => useLoader(fetcher))!;
    vi.useFakeTimers();
    try {
      window.dispatchEvent(new Event('online'));
      if (success) resolve('fresh');
      else reject(new Error('old offline'));
      await flushPromises();
      await vi.advanceTimersByTimeAsync(30_000);
      expect(fetcher).toHaveBeenCalledTimes(success ? 1 : 2);
      if (success) expect(loader.data.value).toBe('fresh');
      else expect(loader.error.value).toBeInstanceOf(Error);
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      wrapper.unmount();
      scope.stop();
      vi.useRealTimers();
    }
  }
);

it('Failed resources handle later online events while others remain pending', async () => {
  const { wrapper } = await mountShell('/discover');
  const scope = effectScope();
  let finish!: (value: string) => void;
  scope.run(() =>
    useLoader(
      () =>
        new Promise<string>(resolve => {
          finish = resolve;
        })
    )
  );
  const fetcher = vi
    .fn()
    .mockRejectedValueOnce(new Error('offline'))
    .mockRejectedValueOnce(new Error('offline'))
    .mockResolvedValue('recovered');
  const loader = scope.run(() => useLoader(fetcher))!;
  await flushPromises();
  vi.useFakeTimers();
  try {
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    await vi.advanceTimersByTimeAsync(1_000);
    window.dispatchEvent(new Event('online'));
    await vi.advanceTimersByTimeAsync(4_000);
    expect(loader.data.value).toBe('recovered');
    expect(fetcher).toHaveBeenCalledTimes(3);
    finish('slow success');
    await flushPromises();
  } finally {
    wrapper.unmount();
    scope.stop();
    vi.useRealTimers();
  }
});

it('The removed history route redirects to updates', async () => {
  const router = createRouter({ history: createMemoryHistory(), routes });
  await router.push('/history');
  expect(router.currentRoute.value.path).toBe('/updates');
  expect(router.currentRoute.value.meta.nav).toBe('updates');
});
