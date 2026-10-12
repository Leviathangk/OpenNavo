import { disposePinia } from 'pinia';
import { mount } from '@vue/test-utils';
import { defineComponent, h } from 'vue';
import { expect, it, vi } from 'vitest';
import { commands, unwrap } from '@/ipc/client';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useNamesStore } from '@/stores/names';
import { setup } from './helpers';
import { i18n } from '@/i18n';

vi.mock('@tauri-apps/api/core', async importOriginal => ({
  ...(await importOriginal<typeof import('@tauri-apps/api/core')>()),
  isTauri: () => true,
  convertFileSrc: (path: string) => `asset://${path}`
}));

it('Installed apps prefer normalized native icons and keep catalog names; catalog icons remain a fallback', async () => {
  const context = await setup('/installed');
  try {
    const sample = (await unwrap(commands.localAppsList()))[0]!;
    useNamesStore().entries['cask/visual-studio-code'] = {
      name: 'Visual Studio Code',
      sourceLocale: 'en-US',
      displayName: {},
      iconUrl: 'https://cdn.test/catalog-icon.png',
      accentColor: null
    };
    let identity!: ReturnType<typeof usePackageIdentity>;
    const wrapper = mount(
      defineComponent({
        setup() {
          identity = usePackageIdentity();
          return () => h('div');
        }
      }),
      { global: { plugins: [context.pinia, i18n] } }
    );
    const app = {
      ...sample,
      cask: 'visual-studio-code',
      iconPath: '/icons/native-normalized-v1.png'
    };
    expect(identity.localApp(app).icon.src).toBe('asset:///icons/native-normalized-v1.png');
    expect(identity.localApp(app).name).toBe('Visual Studio Code');
    expect(identity.localApp({ ...app, iconPath: null }).icon.src).toBe('https://cdn.test/catalog-icon.png');
    expect(identity.localApp({ ...sample, cask: null, catalogToken: null, iconPath: null }).icon.src).toBeNull();
    wrapper.unmount();
  } finally {
    disposePinia(context.pinia);
  }
});
