import { mount } from '@vue/test-utils';
import { expect, it } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import { DESKTOP_LOCALES, formatDate } from '@opennavo/shared';
import { OnPackageHeader } from '@opennavo/ui';
import lifecycleCases from '../../../packages/shared/tests/fixtures/packageInstallStates.json';
import DetailHeader from '@/components/package/DetailHeader.vue';
import { i18n } from '@/i18n';
import { useLibraryStore } from '@/stores';
import { setup } from './helpers';

const detail = () =>
  ({
    kind: 'cask',
    token: 'source-example',
    displayName: 'Source Example',
    names: [],
    version: '1.0',
    tap: 'homebrew/cask',
    sourceUrl: 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/s/source-example.rb',
    facts: {
      overview: { version: '1.0' },
      requirements: [
        {
          architectures: ['arm64', 'x86_64'],
          versions: ['1.0'],
          downloadUrl: 'https://downloads.example.com/source-example.dmg',
          sha256: 'a'.repeat(64),
          evidence: {
            sourceUrl: 'https://github.com/Homebrew/homebrew-cask/blob/HEAD/Casks/s/source-example.rb',
            checkedAt: '2026-10-30T00:00:00Z',
            status: 'verified'
          }
        }
      ]
    }
  }) as unknown as PackageDetail;

it.each(DESKTOP_LOCALES)('renders source evidence in the desktop tag row using %s messages', async locale => {
  const { pinia, router } = await setup('/package/cask/source-example');
  i18n.global.locale.value = locale;
  const wrapper = mount(DetailHeader, {
    props: { kind: 'cask', token: 'source-example', detail: detail() },
    global: { plugins: [pinia, router, i18n] },
    attachTo: document.body
  });
  try {
    const badge = wrapper.get('[data-testid="package-source-badge"]');
    expect(badge.element.tagName).toBe('BUTTON');
    expect(badge.attributes('type')).toBe('button');
    expect(badge.attributes('aria-label')).toBe(i18n.global.messages.value[locale].package.sourceBadge.verified);
    expect(badge.element.closest('[data-testid="package-header-tags"]')).not.toBeNull();
    expect(wrapper.findComponent(OnPackageHeader).props('meta')).toBe('');
    expect(wrapper.text()).not.toContain('homebrew/cask');
    const tooltip = document.getElementById(badge.attributes('aria-describedby')!)!;
    expect(tooltip.getAttribute('role')).toBe('tooltip');
    expect(tooltip.textContent).toContain('downloads.example.com');
    expect(tooltip.textContent).toContain('a'.repeat(64));
    expect(tooltip.textContent).toContain('2026');
    expect(tooltip.textContent).not.toMatch(/package\.sourceBadge|\{date\}|\{domain\}/);
  } finally {
    wrapper.unmount();
    i18n.global.locale.value = 'zh-CN';
  }
});

it.each(lifecycleCases)('desktop source warnings follow lifecycle priority: $name', async ({ fields, expected }) => {
  const { pinia, router } = await setup('/package/cask/source-example');
  const pkg = Object.assign(detail(), fields);
  const wrapper = mount(DetailHeader, {
    props: { kind: 'cask', token: 'source-example', detail: pkg },
    global: { plugins: [pinia, router, i18n] }
  });
  try {
    const badge = wrapper.get('[data-testid="package-source-badge"]');
    expect(badge.attributes('data-state')).toBe(expected);
    expect(badge.text()).toBe(i18n.global.t(`package.sourceBadge.${expected === 'active' ? 'verified' : expected}`));
    expect(
      wrapper
        .findComponent(OnPackageHeader)
        .props('chips')
        ?.some(chip => chip.key === expected)
    ).toBe(false);
  } finally {
    wrapper.unmount();
  }
});

it('preserves the desktop installed date after removing tap metadata', async () => {
  const { pinia, router } = await setup('/package/cask/source-example');
  useLibraryStore().items = [
    {
      kind: 'cask',
      token: 'source-example',
      name: 'Source Example',
      installedVersion: '1.0',
      actualVersion: null,
      latestVersion: null,
      status: 'up_to_date',
      onRequest: true,
      requiredBy: [],
      installedAt: Date.parse('2026-10-01T00:00:00Z'),
      sizeBytes: null,
      appPaths: [],
      iconPath: null,
      autoUpdates: false,
      pinned: false
    }
  ];
  const wrapper = mount(DetailHeader, {
    props: { kind: 'cask', token: 'source-example', detail: detail() },
    global: { plugins: [pinia, router, i18n] }
  });
  try {
    expect(wrapper.findComponent(OnPackageHeader).props('meta')).toBe(
      i18n.global.t('package.installedAt', {
        date: formatDate('2026-10-01T00:00:00Z', { locale: 'zh-CN' })
      })
    );
    expect(wrapper.findComponent(OnPackageHeader).props('meta')).not.toContain('homebrew/cask');
  } finally {
    wrapper.unmount();
  }
});
