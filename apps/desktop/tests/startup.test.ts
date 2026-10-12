import { flushPromises } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { startupRedirect } from '@/composables/startupRoute';
import { deepLinkOpened, startDeepLinks, resumeDeepLink, useDeepLinkStore } from '@/composables/useDeepLink';
import { events } from '@/ipc/bindings';
import { startLocalState, useSettingsStore } from '@/stores';
import { setup } from './helpers';

// Startup destination (first-launch-onboarding §3.1, D1).
describe('Startup route', () => {
  it('Incomplete onboarding opens Welcome; completed settings stay on default page with/without Homebrew; missing settings do not redirect', () => {
    expect(startupRedirect({ deepLinkOpened: false, onboardingCompleted: false })).toBe('/welcome');
    expect(startupRedirect({ deepLinkOpened: false, onboardingCompleted: true })).toBeNull();
    expect(startupRedirect({ deepLinkOpened: false })).toBeNull();
    expect(startupRedirect({ deepLinkOpened: true, onboardingCompleted: false })).toBe('/welcome');
  });

  it('Cold-start links arriving before state wait for onboarding before opening targets', async () => {
    const context = await setup('/discover', { brew: false, tickMs: 1000 });
    expect(deepLinkOpened()).toBe(false);
    const ready = startLocalState();
    await startDeepLinks(context.router, ready);
    await events.deeplinkReceived.emit({
      url: 'opennavo://package/cask/zed',
      route: '/package/cask/zed',
      action: null
    });
    await ready;
    await flushPromises();
    const settings = useSettingsStore().value;
    // Fresh settings: without a deep link, startup would enter the welcome page.
    expect(settings?.onboardingCompleted).toBe(false);
    expect(
      startupRedirect({
        deepLinkOpened: deepLinkOpened(),
        onboardingCompleted: settings?.onboardingCompleted
      })
    ).toBe('/welcome');
    expect(context.router.currentRoute.value.path).toBe('/welcome');
    expect(useDeepLinkStore().pending?.route).toBe('/package/cask/zed');
    await useSettingsStore().update('onboardingCompleted', true);
    expect(await resumeDeepLink(context.router)).toBe(true);
    expect(context.router.currentRoute.value.path).toBe('/package/cask/zed');
    expect(useDeepLinkStore().pending).toBeNull();
  });
});
