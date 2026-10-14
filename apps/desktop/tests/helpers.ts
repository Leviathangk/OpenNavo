import { createPinia, setActivePinia } from 'pinia';
import type { Pinia } from 'pinia';
import { createRouter, createMemoryHistory } from 'vue-router';
import type { RouteRecordRaw, Router } from 'vue-router';
import { defineComponent, h } from 'vue';
import { i18n } from '@/i18n';
import { commands, unwrap } from '@/ipc/client';
import { installMockIpc } from '@/ipc/mock';
import type { MockState } from '@/ipc/mock-data/runner';

/** Placeholder pages and rails: shell tests only cover layout and navigation. */
export const stub = (text: string) => defineComponent({ name: `Stub${text}`, setup: () => () => h('p', text) });

/** Use the real routes' paths, names, and metadata, with placeholder page components. */
export const stubRoutes: RouteRecordRaw[] = [
  { path: '/', redirect: '/discover' },
  {
    path: '/discover',
    name: 'discover',
    components: { default: stub('发现页'), rail: stub('本机 Homebrew 状态') },
    meta: { nav: 'discover' }
  },
  {
    path: '/categories',
    name: 'categories',
    component: stub('分类页'),
    meta: { nav: 'categories' }
  },
  {
    path: '/rankings',
    name: 'rankings',
    component: stub('排行榜页'),
    meta: { nav: 'rankings' }
  },
  {
    path: '/recent',
    name: 'recent',
    component: stub('最近更新页'),
    meta: { nav: 'recent' }
  },
  {
    path: '/search',
    name: 'search',
    component: stub('搜索页'),
    meta: { nav: 'discover' }
  },
  {
    path: '/package/:kind/:token',
    name: 'package',
    component: stub('详情页'),
    meta: { nav: 'discover' }
  },
  {
    path: '/collections/:slug',
    name: 'collection',
    component: stub('合集页'),
    meta: { nav: 'discover' }
  },
  { path: '/brewfile', name: 'brewfile', component: stub('我的清单'), meta: { nav: 'brewfile' } },
  {
    path: '/installed',
    name: 'installed',
    components: { default: stub('已安装页'), rail: stub('存储') },
    meta: { nav: 'installed' }
  },
  {
    path: '/updates',
    name: 'updates',
    components: { default: stub('更新页'), rail: stub('本机 Homebrew 状态') },
    meta: { nav: 'updates' }
  },
  {
    path: '/cleanup',
    name: 'cleanup',
    components: { default: stub('清理电脑页'), rail: stub('磁盘') },
    meta: { nav: 'cleanup' }
  },
  {
    path: '/monitor',
    name: 'monitor',
    components: { default: stub('电脑监控页'), rail: stub('菜单栏') },
    meta: { nav: 'monitor' }
  },
  {
    path: '/history',
    name: 'history',
    redirect: '/updates'
  },
  {
    path: '/settings/:section?',
    name: 'settings',
    component: stub('设置页'),
    meta: { nav: 'settings' }
  },
  {
    path: '/welcome',
    name: 'welcome',
    component: stub('欢迎页'),
    meta: { bare: true }
  },
  {
    path: '/permission-guide/:pane(app_management|full_disk_access)',
    name: 'permission-guide',
    component: stub('权限引导'),
    meta: { bare: true }
  }
];

export interface TestContext {
  pinia: Pinia;
  router: Router;
  state: MockState;
}

/** Install simulated IPC, Pinia, and memory-history routing; tickMs controls simulated task progression. */
export async function setup(
  path = '/discover',
  options: { tickMs?: number; brew?: boolean } = {}
): Promise<TestContext> {
  const state = installMockIpc(options);
  // Existing interaction cases explicitly use Chinese so the product's English default does not affect them.
  const settings = await unwrap(commands.settingsGet());
  await unwrap(
    commands.settingsSet({
      ...settings,
      locale: 'zh-CN',
      localeMode: 'manual'
    })
  );
  const pinia = createPinia();
  setActivePinia(pinia);
  i18n.global.locale.value = 'zh-CN';
  const router = createRouter({
    history: createMemoryHistory(),
    routes: stubRoutes
  });
  await router.push(path);
  await router.isReady();
  return { pinia, router, state };
}

/** jsdom lacks matchMedia: answer shell media queries using the supplied width. */
export function mockMatchMedia(wide: () => boolean) {
  window.matchMedia = (query: string) =>
    ({
      matches: query.includes('min-width: 1240px') ? wide() : false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {}
    }) as unknown as MediaQueryList;
}
