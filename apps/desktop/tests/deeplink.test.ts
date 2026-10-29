import { flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { openDeepLink, resumeDeepLink, startDeepLinks, useDeepLinkStore } from '@/composables/useDeepLink';
import { useToasts } from '@/composables/useToasts';
import { commands, events, unwrap } from '@/ipc/client';
import { startLocalState, useSettingsStore } from '@/stores';
import { setup } from './helpers';

type ConfirmResult = Awaited<ReturnType<typeof commands.deeplinkInstallConfirm>>;
const confirmed = (remaining: number): ConfirmResult => ({
  status: 'ok',
  data: { task: {} as never, remaining }
});

afterEach(() => {
  document.body.innerHTML = '';
  localStorage.removeItem('opennavo.pendingDeepLink');
  vi.restoreAllMocks();
});

describe('Deep links', () => {
  it.each(['/updates', '/collection/dev-setup', '/search?q=zed'])(
    'Restore read-only target %s after restart',
    async route => {
      await setup('/welcome');
      const event = { url: '', route, action: null };
      localStorage.setItem('opennavo.pendingDeepLink', JSON.stringify(event));
      expect(useDeepLinkStore().pending).toEqual(event);
    }
  );

  it('replays a deferred install link through Rust after onboarding without a frontend target', async () => {
    const first = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', false);
    const event = {
      url: 'opennavo://package/cask/zed?action=install',
      route: '/package/cask/zed',
      action: 'install' as const,
      pendingId: 'pending-1'
    };
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockResolvedValue(confirmed(0));
    await openDeepLink(first.router, event);
    expect(first.router.currentRoute.value.path).toBe('/welcome');
    expect(JSON.parse(localStorage.getItem('opennavo.pendingDeepLink')!)).toEqual(event);
    useDeepLinkStore().$dispose();

    const restarted = await setup('/welcome');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', false);
    expect(useDeepLinkStore().pending).toEqual(event);
    expect(await resumeDeepLink(restarted.router)).toBe(false);
    await useSettingsStore().update('onboardingCompleted', true);
    expect(await resumeDeepLink(restarted.router)).toBe(true);
    expect(restarted.router.currentRoute.value.path).toBe('/package/cask/zed');
    expect(confirm).toHaveBeenCalledWith('pending-1');
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);
    expect(localStorage.getItem('opennavo.pendingDeepLink')).toBeNull();
  });

  it('install links pass only the Rust pending id and never call taskEnqueue', async () => {
    const context = await setup('/discover');
    const ready = startLocalState();
    await startDeepLinks(context.router, ready);
    await ready;
    await useSettingsStore().update('onboardingCompleted', true);
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockResolvedValue(confirmed(0));
    const enqueue = vi.spyOn(commands, 'taskEnqueue');

    await events.deeplinkReceived.emit({
      url: 'opennavo://package/cask/zed?action=install',
      route: '/package/cask/zed',
      action: 'install',
      pendingId: 'pending-single'
    });
    await flushPromises();
    expect(context.router.currentRoute.value.path).toBe('/package/cask/zed');
    expect(confirm).toHaveBeenCalledWith('pending-single');
    expect(enqueue).not.toHaveBeenCalled();
  });

  it('collection links confirm each Rust-owned item one at a time', async () => {
    const context = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    const confirm = vi
      .spyOn(commands, 'deeplinkInstallConfirm')
      .mockResolvedValueOnce(confirmed(1))
      .mockResolvedValueOnce(confirmed(0));
    const enqueue = vi.spyOn(commands, 'taskEnqueue');

    await openDeepLink(context.router, {
      url: 'opennavo://collection/dev-setup?action=install',
      route: '/collection/dev-setup',
      action: 'install',
      pendingId: 'pending-collection'
    });
    expect(context.router.currentRoute.value.path).toBe('/collections/dev-setup');
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(confirm).toHaveBeenNthCalledWith(1, 'pending-collection');
    expect(confirm).toHaveBeenNthCalledWith(2, 'pending-collection');
    expect(enqueue).not.toHaveBeenCalled();
  });

  it.each(['E_CONFIRMATION_DENIED', 'E_CONFIRMATION_TIMEOUT', 'E_BUSY', 'E_CONFIRMATION_REUSED', 'E_CONFIRMATION_MISMATCH'])(
    'does not enqueue when Rust denies a deep-link grant (%s)',
    async code => {
      const context = await setup('/discover');
      await startLocalState();
      await useSettingsStore().update('onboardingCompleted', true);
      const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockRejectedValue({
        code,
        message: code,
        detail: null
      });
      const enqueue = vi.spyOn(commands, 'taskEnqueue');
      await openDeepLink(context.router, {
        url: 'opennavo://package/cask/zed?action=install',
        route: '/package/cask/zed',
        action: 'install',
        pendingId: 'pending-denied'
      });
      expect(confirm).toHaveBeenCalledWith('pending-denied');
      expect(enqueue).not.toHaveBeenCalled();
      expect(useToasts().items.value.at(-1)?.tone).toBe('danger');
    }
  );

  it('read-only and legacy links never invoke the confirmation command', async () => {
    const context = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm');

    await openDeepLink(context.router, { url: '', route: '/package/cask/zed', action: 'install' });
    expect(confirm).not.toHaveBeenCalled();
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);

    await openDeepLink(context.router, {
      url: '',
      route: '/package/formula/fzf',
      action: 'install'
    });
    expect(context.router.currentRoute.value.path).toBe('/discover');
    expect(confirm).not.toHaveBeenCalled();
    expect(useToasts().items.value.at(-1)?.title).toContain('只收录 App');

    await openDeepLink(context.router, {
      url: '',
      route: '/collection/dev-setup',
      action: 'install'
    });
    expect(context.router.currentRoute.value.fullPath).toBe('/collections/dev-setup');
  });

  it('the generic task command rejects deep-link writes while the normal UI trigger remains valid', async () => {
    await setup('/discover');
    await expect(
      unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'zed' }, {}, 'deeplink'))
    ).rejects.toMatchObject({ code: 'E_CONFIRMATION_REQUIRED' });
    const task = await unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'zed' }, {}, 'manual'));
    expect(task.trigger).toBe('manual');
  });
});
