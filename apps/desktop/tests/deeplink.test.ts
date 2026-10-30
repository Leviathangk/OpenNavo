import { flushPromises, mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { openDeepLink, resumeDeepLink, startDeepLinks, useDeepLinkStore } from '@/composables/useDeepLink';
import { useToasts } from '@/composables/useToasts';
import { IpcError, commands, events, unwrap } from '@/ipc/client';
import { startLocalState, useSettingsStore } from '@/stores';
import { setup } from './helpers';
import { routes } from '@/router';
import { i18n } from '@/i18n';
import FormulaDetailPage from '@/pages/package/FormulaDetailPage.vue';

type ConfirmResult = Awaited<ReturnType<typeof commands.deeplinkInstallConfirm>>;
const confirmed = (): ConfirmResult => ({
  status: 'ok',
  data: { tasks: [] }
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
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockResolvedValue(confirmed());
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
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockResolvedValue(confirmed());
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

  it('collection links use one Rust-owned confirmation for the whole plan', async () => {
    const context = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockResolvedValue(confirmed());
    const enqueue = vi.spyOn(commands, 'taskEnqueue');

    await openDeepLink(context.router, {
      url: 'opennavo://collection/dev-setup?action=install',
      route: '/collection/dev-setup',
      action: 'install',
      pendingId: 'pending-collection'
    });
    expect(context.router.currentRoute.value.path).toBe('/collections/dev-setup');
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(confirm).toHaveBeenCalledWith('pending-collection');
    expect(enqueue).not.toHaveBeenCalled();
  });

  it.each([
    'E_CONFIRMATION_DENIED',
    'E_CONFIRMATION_TIMEOUT',
    'E_BUSY',
    'E_CONFIRMATION_REUSED',
    'E_CONFIRMATION_MISMATCH'
  ])('does not enqueue when Rust denies a deep-link grant (%s)', async code => {
    const context = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm').mockRejectedValue(
      new IpcError({ code, message: code, detail: null })
    );
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
  });

  it.each([
    ['E_BUSY', '另一个确认窗口还开着。请先关闭它，再重试。'],
    ['E_CONFIRMATION_TIMEOUT', '确认窗口已超时。请先关闭它，再重试。']
  ])('shows a localized message when a deep-link confirmation fails with %s', async (code, message) => {
    const context = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    vi.spyOn(commands, 'deeplinkInstallConfirm').mockRejectedValue(
      new IpcError({ code, message: code, detail: null })
    );
    await openDeepLink(context.router, {
      url: 'opennavo://package/cask/zed?action=install',
      route: '/package/cask/zed',
      action: 'install',
      pendingId: 'pending-confirmation-error'
    });
    const toast = useToasts().items.value.at(-1);
    expect(toast?.tone).toBe('danger');
    expect(toast?.title).toBe(i18n.global.t('errors.actionFailed'));
    expect(toast?.description).toBe(message);
    expect(toast?.description).not.toContain(code);
  });

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
      pendingId: 'forged-formula-intent',
      action: 'install'
    });
    expect(context.router.currentRoute.value.path).toBe('/package/formula/fzf');
    expect(confirm).not.toHaveBeenCalled();

    await openDeepLink(context.router, {
      url: '',
      route: '/collection/dev-setup',
      action: 'install'
    });
    expect(context.router.currentRoute.value.fullPath).toBe('/collections/dev-setup');
  });

  it.each(['manual', 'deeplink', 'mcp', 'schedule', 'bundle', 'retry'] as const)(
    'generic single and batch IPC preserve internal origins and reject external origins (%s)',
    async trigger => {
      await setup('/discover');
      const before = await unwrap(commands.taskListActive());
      if (trigger === 'deeplink' || trigger === 'mcp') {
        const error = { code: 'E_INVALID_ARG', message: 'external_trigger_requires_rust' };
        await expect(
          unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'zed' }, {}, trigger))
        ).rejects.toMatchObject(error);
        await expect(
          unwrap(commands.taskEnqueueMany('install', [{ kind: 'cask', token: 'notion' }], {}, trigger))
        ).rejects.toMatchObject(error);
        await expect(unwrap(commands.taskEnqueueMany('install', [], {}, trigger))).rejects.toMatchObject(error);
        expect(await unwrap(commands.taskListActive())).toEqual(before);
        return;
      }
      const task = await unwrap(commands.taskEnqueue('install', { kind: 'cask', token: 'zed' }, {}, trigger));
      const batch = await unwrap(commands.taskEnqueueMany('install', [{ kind: 'cask', token: 'notion' }], {}, trigger));
      const active = await unwrap(commands.taskListActive());
      for (const item of [task, ...batch]) {
        expect(item.trigger).toBe(trigger);
        expect(active.find(stored => stored.id === item.id)?.trigger).toBe(trigger);
      }
    }
  );

  it('the real formula detail route exposes no install or other write actions', async () => {
    const context = await setup('/package/formula/fzf');
    const router = createRouter({ history: createMemoryHistory(), routes });
    const route = router.resolve('/package/formula/fzf').matched.at(-1)!;
    const component = route.components!.default as () => Promise<{ default: unknown }>;
    expect((await component()).default).toBe(FormulaDetailPage);
    const confirm = vi.spyOn(commands, 'deeplinkInstallConfirm');
    const enqueue = vi.spyOn(commands, 'taskEnqueue');
    const wrapper = mount(FormulaDetailPage, {
      props: { token: 'fzf' },
      global: { plugins: [context.pinia, context.router, i18n] }
    });
    await flushPromises();
    expect(wrapper.text()).toContain('fzf');
    expect(wrapper.text()).toContain(i18n.global.t('deeplink.formulaUnsupported'));
    expect(wrapper.find('button').exists()).toBe(false);
    expect(confirm).not.toHaveBeenCalled();
    expect(enqueue).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});
