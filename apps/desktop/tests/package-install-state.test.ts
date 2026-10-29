import { mount } from '@vue/test-utils';
import { describe, expect, it, vi } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import { OnMenu, OnPackageNotice } from '@opennavo/ui';
import lifecycleCasesData from '../../../packages/shared/tests/fixtures/packageInstallStates.json';
import type { LocalItem } from '@/ipc/client';
import PackageLayout from '@/pages/package/PackageLayout.vue';
import { i18n } from '@/i18n';
import PackageOverview from '@/pages/package/PackageOverview.vue';
import DetailHeader from '@/components/package/DetailHeader.vue';
import PackageGetButton from '@/components/package/PackageGetButton.vue';
import { setup } from './helpers';

const state = vi.hoisted(() => ({ detail: null as PackageDetail | null, local: null as LocalItem | null }));
vi.mock('@/composables/useRemoteLoader', () => ({
  useRemoteLoader: () => ({ data: { value: undefined } })
}));
vi.mock('@/composables/usePackageDetail', async () => {
  const { ref } = await import('vue');
  const context = () => ({
    detail: ref(state.detail),
    local: ref(state.local),
    releaseStats: ref(null),
    error: ref(null)
  });
  return {
    providePackage: context,
    usePackage: () => ({
      kind: ref(state.detail?.kind ?? state.local?.kind ?? 'cask'),
      token: ref(state.detail?.token ?? state.local?.token ?? 'iterm2'),
      local: ref(state.local),
      detail: ref(state.detail)
    })
  };
});

describe('Desktop package install states', () => {
  it.each(['disabled', 'deprecated', 'paused'] as const)('%s follows the shared command policy', async status => {
    state.local = null;
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

const lifecycleCases = lifecycleCasesData as {
  name: string;
  fields: Partial<PackageDetail>;
  expected: 'active' | 'disabled' | 'deprecated' | 'paused';
}[];

it.each(lifecycleCases)('desktop banner and command share lifecycle priority: $name', async ({ fields, expected }) => {
  state.local = null;
  state.detail = {
    kind: 'cask',
    token: 'iterm2',
    displayName: 'iTerm2',
    names: [],
    version: '1.0.0',
    installCommand: 'brew install --cask iterm2',
    artifacts: { apps: [], binaries: [], pkgs: [], entries: [] },
    supports: { status: 'known', arm64: true, x86_64: true },
    installs: { d30: 0, d90: 0, d365: 0 },
    ...fields
  } as PackageDetail;
  const { pinia, router } = await setup('/package/cask/iterm2');
  const global = {
    plugins: [pinia, router, i18n],
    stubs: { MonthlyInstallsCard: true, MasAlsoPanel: true, DetailHeader: true, DetailStats: true }
  };
  const layout = mount(PackageLayout, { props: { kind: 'cask', token: 'iterm2' }, global });
  const overview = mount(PackageOverview, { global });
  try {
    const banner = layout.findComponent(OnPackageNotice);
    const command = overview.get('[data-testid="package-install-command"]');
    expect(banner.exists()).toBe(expected !== 'active');
    expect(command.find('code').exists()).toBe(expected !== 'disabled');
    expect(command.find('button').exists()).toBe(expected !== 'disabled');
    if (expected !== 'active') {
      expect(banner.props('tone')).toBe(expected === 'disabled' ? 'danger' : 'warning');
      expect(command.get('[role="note"]').text()).toBe(banner.props('text'));
      expect(banner.text()).not.toBe('');
    }
  } finally {
    overview.unmount();
    layout.unmount();
  }
});

it.each(['cask', 'formula', 'link'] as const)('offline PackageOverview renders the local %s spec card', async kind => {
  state.detail = null;
  state.local = {
    kind,
    token: 'offline-example',
    name: 'Offline Example',
    names: ['Offline Example', 'Former Example'],
    version: '1.2.3',
    summary: { 'en-US': 'Offline summary' },
    displayName: { 'en-US': 'Offline Example' },
    sourceLocale: 'en-US',
    homepage: 'https://example.com/',
    autoUpdates: true,
    binaries: ['example'],
    bundleIds: ['org.example.offline'],
    link: kind === 'link' ? { pricing: 'free', teamId: 'EXAMPLE123', github: { repo: 'acme/example' } } : null
  } as unknown as LocalItem;
  const { pinia, router } = await setup(`/package/${kind}/offline-example`);
  const wrapper = mount(PackageOverview, {
    global: { plugins: [pinia, router, i18n], stubs: { MonthlyInstallsCard: true, MasAlsoPanel: true } }
  });
  try {
    const card = wrapper.get('[data-testid="package-spec-card"]');
    expect(card.text()).toContain('offline-example');
    expect(card.text()).toContain('Former Example');
    expect(card.get('a[href="https://example.com/"]').text()).toBe('example.com');
    expect(card.text()).toContain(kind === 'link' ? 'org.example.offline' : 'example');
    expect(wrapper.find('[data-testid="package-install-command"]').exists()).toBe(kind !== 'link');
    if (kind === 'link') {
      expect(card.text()).toContain('1.2.3');
      expect(card.text()).toContain('EXAMPLE123');
    }
  } finally {
    wrapper.unmount();
    state.local = null;
  }
});
