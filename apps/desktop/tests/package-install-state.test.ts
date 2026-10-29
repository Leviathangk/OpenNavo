import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import { OnMenu } from '@opennavo/ui';
import { i18n } from '@/i18n';
import PackageOverview from '@/pages/package/PackageOverview.vue';
import DetailHeader from '@/components/package/DetailHeader.vue';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import { setup } from './helpers';

const state = vi.hoisted(() => ({ detail: {} as PackageDetail }));
vi.mock('@/composables/useRemoteLoader', () => ({
  useRemoteLoader: () => ({ data: { value: undefined } })
}));
vi.mock('@/composables/usePackageDetail', async () => {
  const { ref } = await import('vue');
  return {
    usePackage: () => ({
      kind: ref('cask'),
      token: ref('iterm2'),
      local: ref(null),
      detail: ref(state.detail)
    })
  };
});

describe('Desktop package install states', () => {
  it.each(['disabled', 'deprecated', 'paused'] as const)('%s follows the shared command policy', async status => {
    state.detail = {
      kind: 'cask',
      token: 'iterm2',
      installCommand: 'brew install --cask iterm2',
      artifacts: { apps: [], binaries: [], pkgs: [], entries: [] },
      supports: { status: 'known', arm64: true, x86_64: true },
      installs: { d30: 0, d90: 0, d365: 0 },
      homebrewStatus: { state: status === 'paused' ? 'active' : status },
      disabled: status !== 'deprecated',
      ...(status === 'deprecated'
        ? { deprecation: { reason: 'unmaintained' } }
        : { disable: { reason: 'discontinued' } })
    } as unknown as PackageDetail;
    const { pinia, router } = await setup('/package/cask/iterm2');
    const wrapper = mount(PackageOverview, {
      global: {
        plugins: [pinia, router, i18n],
        stubs: { MonthlyInstallsCard: true, MasAlsoPanel: true }
      }
    });
    try {
      const card = wrapper.get('[data-testid="package-install-command"]');
      const warning = card.get('[role="note"]');
      expect(warning.text()).not.toBe('');
      expect(card.find('code').exists()).toBe(status !== 'disabled');
      expect(card.find('button').exists()).toBe(status !== 'disabled');
      if (status !== 'disabled')
        expect(
          warning.element.compareDocumentPosition(card.get('pre').element) & Node.DOCUMENT_POSITION_FOLLOWING
        ).toBeTruthy();
    } finally {
      wrapper.unmount();
    }
    const header = mount(DetailHeader, {
      props: { kind: 'cask', token: 'iterm2', detail: state.detail },
      global: { plugins: [pinia, router, i18n] }
    });
    try {
      expect(header.findComponent(PackageGetButton).props('disabled')).toBe(status === 'disabled');
      expect(
        header
          .findComponent(OnMenu)
          .props('items')
          .some(item => item.key === 'copy')
      ).toBe(status !== 'disabled');
    } finally {
      header.unmount();
    }
  });
});
