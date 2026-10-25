import { defineComponent } from 'vue';
import { createPinia } from 'pinia';
import { mount } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { router } from '@/router';
import { useAuthStore } from '@/store/modules/auth';
import { localStg } from '@/utils/storage';

const mocks = vi.hoisted(() => ({ clearTabs: vi.fn() }));
vi.mock('@/router', async () => {
  const { createRouter, createMemoryHistory } = await import('vue-router');
  return {
    router: createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'root', component: { template: '<div />' } },
        { path: '/login', name: 'login', component: { template: '<div />' } },
        { path: '/oauth-consent', component: { template: '<div />' } },
        { path: '/dashboard', component: { template: '<div />' } }
      ]
    })
  };
});
vi.mock('@/service/api', () => ({
  fetchLogin: vi.fn(async () => ({ data: { token: 'test-token', refreshToken: 'test-refresh' } })),
  fetchGetUserInfo: vi.fn(async () => ({ data: { userId: 'current-user', userName: 'test', roles: [], buttons: [] } }))
}));
vi.mock('@/store/modules/route', () => ({ useRouteStore: () => ({ resetStore: vi.fn() }) }));
vi.mock('@/store/modules/tab', () => ({ useTabStore: () => ({ clearTabs: mocks.clearTabs }) }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));

const pendingOAuth =
  '/oauth-consent?client_id=test-client&redirect_uri=https%3A%2F%2Fclient.example%2Fcallback&state=a%2Bb%26c&code_challenge=challenge&code_challenge_method=S256&resource=https%3A%2F%2Fadmin.example%2Fmcp';
let wrapper: ReturnType<typeof mount> | undefined;

beforeEach(() => {
  localStorage.clear();
  vi.clearAllMocks();
  mocks.clearTabs.mockResolvedValue(undefined);
});
afterEach(() => wrapper?.unmount());

async function setup(previousUser: string | undefined, redirect: string | string[] = pendingOAuth) {
  if (previousUser) localStg.set('lastLoginUserId', previousUser);
  await router.push({ name: 'login', query: { redirect } });
  let auth!: ReturnType<typeof useAuthStore>;
  wrapper = mount(
    defineComponent({
      setup() {
        auth = useAuthStore();
        return () => null;
      }
    }),
    { global: { plugins: [createPinia(), router] } }
  );
  return auth;
}

it.each([undefined, 'other-user', 'current-user'])(
  'resumes OAuth after login with previous user %s',
  async previousUser => {
    const auth = await setup(previousUser);
    await auth.login('test', 'test-password');
    expect(router.currentRoute.value.fullPath).toBe(pendingOAuth);
    expect(mocks.clearTabs).toHaveBeenCalledTimes(previousUser === 'current-user' ? 0 : 1);
    expect(localStg.get('lastLoginUserId')).toBeNull();
  }
);

it('finishes tab cleanup navigation before restoring the captured authorization request', async () => {
  const auth = await setup('other-user');
  mocks.clearTabs.mockImplementation(async () => {
    await new Promise(resolve => setTimeout(resolve, 10));
    await router.push('/dashboard');
  });
  await auth.login('test', 'test-password');
  expect(router.currentRoute.value.fullPath).toBe(pendingOAuth);
});

it.each([undefined, 'other-user', 'current-user'])(
  'preserves ordinary login behavior for previous user %s',
  async previousUser => {
    const auth = await setup(previousUser, '/dashboard');
    await auth.login('test', 'test-password');
    expect(router.currentRoute.value.path).toBe(previousUser === 'current-user' ? '/dashboard' : '/');
  }
);

it('honors explicitly disabled redirects even for pending OAuth', async () => {
  const auth = await setup('current-user');
  await auth.login('test', 'test-password', false);
  expect(router.currentRoute.value.path).toBe('/');
});

it.each([
  'https://evil.example/oauth-consent',
  '//evil.example/oauth-consent',
  '/oauth-consent-other?state=test',
  '/oauth-consent/../dashboard',
  '/oauth-consent\\other',
  ['/oauth-consent', '/dashboard']
])('does not preserve a non-OAuth target during account switching: %s', async target => {
  const auth = await setup('other-user', target);
  await auth.login('test', 'test-password');
  expect(router.currentRoute.value.path).toBe('/');
});
