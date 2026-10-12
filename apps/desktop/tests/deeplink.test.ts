import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it } from 'vitest';
import DeepLinkConfirm from '@/components/shell/DeepLinkConfirm.vue';
import { openDeepLink, resumeDeepLink, startDeepLinks, useDeepLinkStore } from '@/composables/useDeepLink';
import { useToasts } from '@/composables/useToasts';
import { i18n } from '@/i18n';
import { commands, events } from '@/ipc/bindings';
import { unwrap } from '@/ipc/client';
import { startLocalState, useSettingsStore } from '@/stores';
import { setup } from './helpers';

// M3-09: deep-link writes require user confirmation (AGENTS §3, 06 §10).
afterEach(() => {
  document.body.innerHTML = '';
  localStorage.removeItem('opennavo.pendingDeepLink');
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

  it('Restore deferred links after authorization restart; replay after onboarding and retain install confirmation', async () => {
    const first = await setup('/discover');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', false);
    const event = {
      url: 'opennavo://package/cask/zed?action=install',
      route: '/package/cask/zed',
      action: 'install' as const
    };
    await openDeepLink(first.router, event);
    expect(first.router.currentRoute.value.path).toBe('/welcome');
    expect(JSON.parse(localStorage.getItem('opennavo.pendingDeepLink')!)).toEqual(event);
    useDeepLinkStore().$dispose();

    const restarted = await setup('/welcome');
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', false);
    expect(useDeepLinkStore().pending).toEqual(event);
    expect(await resumeDeepLink(restarted.router)).toBe(false);
    expect(useDeepLinkStore().pending).toEqual(event);
    await useSettingsStore().update('onboardingCompleted', true);
    expect(await resumeDeepLink(restarted.router)).toBe(true);
    expect(restarted.router.currentRoute.value.path).toBe('/package/cask/zed');
    expect(useDeepLinkStore().install).toEqual({ kind: 'cask', token: 'zed' });
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);
    expect(localStorage.getItem('opennavo.pendingDeepLink')).toBeNull();
    expect(await resumeDeepLink(restarted.router)).toBe(false);
  });

  it('Install links open details/confirmation only; enqueue with deeplink trigger after consent', async () => {
    const context = await setup('/discover', { tickMs: 1000 });
    const ready = startLocalState();
    await startDeepLinks(context.router, ready);
    await ready;
    await useSettingsStore().update('onboardingCompleted', true);
    const wrapper = mount(DeepLinkConfirm, {
      global: { plugins: [context.pinia, context.router, i18n] },
      attachTo: document.body
    });

    await events.deeplinkReceived.emit({
      url: 'opennavo://package/cask/zed?action=install',
      route: '/package/cask/zed',
      action: 'install'
    });
    await flushPromises();
    expect(context.router.currentRoute.value.path).toBe('/package/cask/zed');
    expect(document.body.textContent).toContain('brew install --cask zed');
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);

    const confirm = [...document.body.querySelectorAll('button')].find(button => button.textContent?.trim() === '安装');
    confirm?.click();
    await flushPromises();
    const [task] = await unwrap(commands.taskListActive());
    expect(task).toMatchObject({
      op: 'install',
      trigger: 'deeplink',
      target: { kind: 'cask', token: 'zed' }
    });
    expect(useDeepLinkStore().install).toBeNull();
    wrapper.unmount();
  });

  it('Canceled links do not enqueue; installed packages open details; collections open batch confirmation', async () => {
    const context = await setup('/discover', { tickMs: 1000 });
    await startLocalState();
    await useSettingsStore().update('onboardingCompleted', true);
    const deeplink = useDeepLinkStore();

    await openDeepLink(context.router, { url: '', route: '/package/cask/zed', action: 'install' });
    expect(deeplink.install).toEqual({ kind: 'cask', token: 'zed' });
    deeplink.install = null;
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);

    // Cask-only catalog (ADR-018): command-line tool deep links return to Discover with a notice, without install confirmation.
    await openDeepLink(context.router, {
      url: '',
      route: '/package/formula/fzf',
      action: 'install'
    });
    expect(context.router.currentRoute.value.path).toBe('/discover');
    expect(deeplink.install).toBeNull();
    expect(useToasts().items.value.at(-1)?.title).toContain('只收录 App');
    expect(await unwrap(commands.taskListActive())).toHaveLength(0);

    await openDeepLink(context.router, {
      url: '',
      route: '/package/cask/obsidian',
      action: 'install'
    });
    expect(context.router.currentRoute.value.path).toBe('/package/cask/obsidian');
    expect(deeplink.install).toBeNull();

    await openDeepLink(context.router, {
      url: '',
      route: '/collection/dev-setup',
      action: 'install'
    });
    expect(context.router.currentRoute.value.fullPath).toBe('/collections/dev-setup?install=1');
    await openDeepLink(context.router, {
      url: '',
      route: '/search?q=%E5%BE%AE%E4%BF%A1',
      action: null
    });
    expect(context.router.currentRoute.value.query.q).toBe('微信');
  });
});
