import { shallowMount } from '@vue/test-utils';
import { expect, it, vi } from 'vitest';
import AssetTab from '../src/views/catalog/package-detail/modules/asset-tab.vue';
import { updatePackageScreenshot } from '../src/service/api';

vi.mock('@/locales', () => ({ $t: (key: string) => key }));
vi.mock('@/hooks/business/auth', () => ({ useAuth: () => ({ hasAuth: () => true }) }));
vi.mock('@/hooks/business/localized', () => ({ useLocalizedPackage: () => ({ nameOf: () => 'Example' }) }));
vi.mock('@/service/api', () => ({
  addPackageScreenshot: vi.fn(),
  deletePackageIcon: vi.fn(),
  deletePackageScreenshot: vi.fn(),
  reorderPackageScreenshots: vi.fn(),
  setPackageIcon: vi.fn(),
  uploadAsset: vi.fn(),
  updatePackageScreenshot: vi.fn().mockResolvedValue({ error: null })
}));

it('saves only the screenshot theme while previewing derived Traditional text', async () => {
  const wrapper = shallowMount(AssetTab, {
    props: { detail: { id: 42, meta: {}, screenshots: [] } as any },
    global: { stubs: ['NText', 'NUpload', 'NButton', 'NEmpty', 'NCard', 'NForm', 'NFormItem', 'NSelect', 'NInput'] }
  });
  const vm = wrapper.vm as any;
  const shot = {
    id: 7,
    theme: 'dark',
    editLocale: 'zh-CN',
    caption: '软件',
    original: { 'zh-CN': { caption: '软件' }, 'zh-Hant': { caption: '軟體' } },
    dirty: true
  };
  vm.switchLocale(shot, 'zh-Hant');
  await vm.saveShot(shot);
  expect(updatePackageScreenshot).toHaveBeenCalledWith(42, 7, { theme: 'dark' });
  expect(shot.dirty).toBe(false);
  wrapper.unmount();
});
