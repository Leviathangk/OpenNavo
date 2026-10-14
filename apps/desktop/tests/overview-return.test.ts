import { flushPromises, mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import { i18n } from '@/i18n';
import PackageOverview from '@/pages/package/PackageOverview.vue';
import { setup } from './helpers';

vi.mock('@/composables/useRemoteLoader', () => ({ useRemoteLoader: () => ({ data: { value: undefined } }) }));
vi.mock('@/composables/usePackageDetail', async () => {
  const { ref } = await import('vue');
  return {
    usePackage: () => ({
      kind: ref('cask'),
      token: ref('ghostty'),
      local: ref(null),
      detail: ref({
        artifacts: { binaries: [] },
        supports: { status: 'known', arm64: true, x86_64: true },
        releaseCount: 10,
        latestRelease: {
          version: '1.0',
          source: 'github',
          sections: [],
          translation: { status: 'original' },
          isLatest: true
        }
      })
    })
  };
});

describe('Overview release history navigation', () => {
  it('Keeps the third recent-app page when opening all versions', async () => {
    const returnTo = '/recent?page=3&size=28';
    const { pinia, router } = await setup(`/package/cask/ghostty?returnTo=${encodeURIComponent(returnTo)}`);
    const wrapper = mount(PackageOverview, {
      global: {
        plugins: [pinia, router, i18n],
        stubs: { OnPackageOverview: { template: '<div><slot name="main" /></div>' } }
      }
    });
    try {
      const link = wrapper.findAll('a').find(node => node.attributes('href')?.includes('/versions'))!;
      await link.trigger('click');
      await flushPromises();
      expect(router.currentRoute.value.path).toBe('/package/cask/ghostty/versions');
      expect(router.currentRoute.value.query.returnTo).toBe(returnTo);
    } finally {
      wrapper.unmount();
    }
  });
});
