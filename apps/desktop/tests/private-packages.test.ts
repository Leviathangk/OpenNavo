import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { ApiError } from '@opennavo/api';
import { OnEmpty, OnSkeleton } from '@opennavo/ui';
import { api } from '@/api';
import RequestError from '@/components/common/RequestError.vue';
import OutdatedList from '@/components/updates/OutdatedList.vue';
import { providePackage } from '@/composables/usePackageDetail';
import { i18n } from '@/i18n';
import type { InstalledItem } from '@/ipc/bindings';
import PackageLayout from '@/pages/package/PackageLayout.vue';
import PackageDetails from '@/pages/package/PackageDetails.vue';
import PackageVersions from '@/pages/package/PackageVersions.vue';
import UpdatesPage from '@/pages/UpdatesPage.vue';
import { useLibraryStore } from '@/stores/library';
import { useUpdatesStore } from '@/stores/updates';
import { optionalPublicPackage } from '@/utils/publicCatalog';
import { setup } from './helpers';

const ok = (data: unknown) => ({ data: { code: '0000', msg: 'ok', data }, response: new Response() });
vi.mock('@/api', () => ({
  api: {
    GET: vi.fn(async () => ({
      error: { code: '1002', msg: 'Not found' },
      response: new Response(null, { status: 404 })
    })),
    POST: vi.fn(async () => ({ data: { code: '0000', msg: 'ok', data: [] }, response: new Response() }))
  }
}));

const installed: InstalledItem = {
  kind: 'cask',
  token: 'private-sample',
  name: 'Locally installed application',
  installedVersion: '1.0',
  actualVersion: '1.0',
  latestVersion: '2.0',
  status: 'outdated',
  onRequest: true,
  requiredBy: [],
  installedAt: null,
  sizeBytes: null,
  appPaths: [],
  iconPath: null,
  autoUpdates: false,
  pinned: false
};
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe('Installed packages absent from the public catalog', () => {
  it('detail 404 uses installed inventory after the catalog entry is missing without a request error', async () => {
    const context = await setup('/package/cask/private-sample');
    context.state.installed.push({ ...installed });
    await useLibraryStore().load();
    const wrapper = mount(PackageLayout, {
      props: { kind: 'cask', token: installed.token },
      global: { plugins: [context.pinia, context.router, i18n] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    expect(wrapper.text()).toContain(installed.name);
    expect(wrapper.text()).toContain('1.0');
    expect(wrapper.findComponent(RequestError).exists()).toBe(false);
  });

  it('detail 404 without a local entry renders unavailable instead of skeletons', async () => {
    const context = await setup('/package/cask/absent-private');
    const wrapper = mount(PackageLayout, {
      props: { kind: 'cask', token: 'absent-private' },
      global: { plugins: [context.pinia, context.router, i18n] }
    });
    wrappers.push(wrapper);
    await flushPromises();
    await flushPromises();
    expect(wrapper.find('[data-testid="package-unavailable"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('本地目录里没有这个包');
    expect(wrapper.findComponent(OnSkeleton).exists()).toBe(false);
    expect(wrapper.findComponent(RequestError).exists()).toBe(false);
  });

  it('release notes 404 shows the empty timeline without an error', async () => {
    const context = await setup('/package/cask/private-sample');
    context.state.installed.push({ ...installed });
    await useLibraryStore().load();
    const Host = defineComponent({
      setup() {
        providePackage({ kind: 'cask', token: installed.token });
        return () => h(PackageVersions);
      }
    });
    const wrapper = mount(Host, { global: { plugins: [context.pinia, context.router, i18n] } });
    wrappers.push(wrapper);
    await flushPromises();
    await flushPromises();
    expect(wrapper.findComponent(RequestError).exists()).toBe(false);
    expect(wrapper.findComponent(OnEmpty).exists()).toBe(true);
  });

  it('installation details retain local names and paths after a detail 404', async () => {
    const context = await setup('/package/cask/private-sample');
    context.state.installed.push({ ...installed, appPaths: ['/Applications/LocalFixture.app'] });
    await useLibraryStore().load();
    const Host = defineComponent({
      setup() {
        providePackage({ kind: 'cask', token: installed.token });
        return () => h(PackageDetails);
      }
    });
    const wrapper = mount(Host, { global: { plugins: [context.pinia, context.router, i18n] } });
    wrappers.push(wrapper);
    await flushPromises();
    expect(wrapper.text()).toContain(installed.name);
    expect(wrapper.text()).toContain('/Applications/LocalFixture.app');
    expect(wrapper.findComponent(RequestError).exists()).toBe(false);
  });

  it.each(['omitted', 'not-found'] as const)(
    'lookup %s items preserve the whole local update batch and checking',
    async mode => {
      const context = await setup('/updates');
      context.state.installed.push({ ...installed });
      const updates = useUpdatesStore();
      await updates.load();
      const before = updates.items.map(item => item.token);
      const visible = updates.items.find(item => item.token !== installed.token)!;
      const records = [
        {
          kind: visible.kind,
          token: visible.token,
          found: true,
          latestRelease: { version: visible.currentVersion, summary: 'Public release summary', publishedAt: null }
        }
      ];
      vi.mocked(api.POST).mockResolvedValueOnce(
        ok(
          mode === 'omitted' ? records : [...records, { kind: 'cask', token: installed.token, found: false }]
        ) as Awaited<ReturnType<typeof api.POST>>
      );
      vi.stubGlobal(
        'IntersectionObserver',
        class {
          observe() {}
          disconnect() {}
        }
      );
      const wrapper = mount(UpdatesPage, { global: { plugins: [context.pinia, context.router, i18n] } });
      wrappers.push(wrapper);
      await flushPromises();
      const list = wrapper.getComponent(OutdatedList);
      expect(list.props('items').map(item => item.token)).toEqual(before);
      expect(list.props('items').some(item => item.token === installed.token)).toBe(true);
      expect(wrapper.text()).toContain('Public release summary');
      await expect(updates.check()).resolves.toBeUndefined();
      expect(updates.items.map(item => item.token)).toEqual(before);
      expect(wrapper.findComponent(RequestError).exists()).toBe(false);
    }
  );

  it('keeps network and server errors retryable', async () => {
    const error = new ApiError({ code: '5000', msg: 'Unavailable', status: 503, requestId: null });
    await expect(optionalPublicPackage(Promise.reject(error))).rejects.toBe(error);
  });
});
