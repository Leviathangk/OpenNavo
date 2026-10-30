import { mount } from '@vue/test-utils';
import { expect, it } from 'vitest';
import type { PackageDetail } from '@opennavo/api';
import { DESKTOP_LOCALES, formatDate } from '@opennavo/shared';
import { OnMenu, OnPackageHeader } from '@opennavo/ui';
import lifecycleCases from '../../../packages/shared/tests/fixtures/packageInstallStates.json';
import DetailHeader from '@/components/package/DetailHeader.vue';
import { i18n } from '@/i18n';
import { useLibraryStore } from '@/stores';
import { setup } from './helpers';

const verifiedLabels: Record<(typeof DESKTOP_LOCALES)[number], string> = {
  'en-US': '✓ Official source · Verified',
  'zh-CN': '✓ 官方来源 · 已核查',
  'zh-Hant': '✓ 官方來源 · 已核查',
  'ja-JP': '✓ 公式配布元 · 確認済み',
  'de-DE': '✓ Offizielle Quelle · Geprüft',
  'es-ES': '✓ Fuente oficial · Verificada',
  'fr-FR': '✓ Source officielle · Vérifiée',
  'it-IT': '✓ Fonte ufficiale · Verificata',
  'pt-BR': '✓ Fonte oficial · Verificada',
  'ru-RU': '✓ Официальный источник · Проверен'
};

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
    expect(badge.find('svg').exists()).toBe(false);
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

it.each(DESKTOP_LOCALES)('desktop source badge uses the web verified label in %s', async locale => {
  const { pinia, router } = await setup('/package/cask/source-example');
  i18n.global.locale.value = locale;
  const wrapper = mount(DetailHeader, {
    props: { kind: 'cask', token: 'source-example', detail: detail() },
    global: { plugins: [pinia, router, i18n] }
  });
  try {
    const badge = wrapper.get('[data-testid="package-source-badge"]');
    expect(badge.text()).toBe(verifiedLabels[locale]);
    expect(badge.attributes('aria-label')).toBe(verifiedLabels[locale]);
    expect(badge.find('svg').exists()).toBe(false);
    expect(wrapper.text()).not.toMatch(/package\.sourceBadge|\{domain\}|\{sha256\}|\{date\}/);
  } finally {
    wrapper.unmount();
    i18n.global.locale.value = 'zh-CN';
  }
});

it('desktop link header pairs Get and Homepage on the right', async () => {
  const { pinia, router } = await setup('/package/link/lumen-notes');
  const pkg = {
    kind: 'link',
    token: 'lumen-notes',
    displayName: 'Lumen Notes',
    names: [],
    version: '1.0',
    homepage: 'https://lumen.example/',
    link: {
      homepage: 'https://lumen.example/',
      downloadUrl: null,
      github: null,
      teamId: null,
      pricing: 'free',
      notes: null,
      adoptedByCask: null
    }
  } as unknown as PackageDetail;
  const wrapper = mount(DetailHeader, {
    props: { kind: 'link', token: 'lumen-notes', detail: pkg },
    global: { plugins: [pinia, router, i18n] }
  });
  try {
    const header = wrapper.findComponent(OnPackageHeader);
    expect(header.props('stretchActions')).toBe(true);
    const actions = wrapper.get('[data-testid="package-header-actions"]');
    expect(actions.element.children).toHaveLength(2);
    expect(actions.element.children[0]!.tagName).toBe('BUTTON');
    expect(actions.element.children[1]!.textContent).toContain(i18n.global.t('package.link.homepage'));
    expect(actions.classes()).toEqual(expect.arrayContaining(['grid', 'grid-cols-2', 'md:flex', 'md:justify-end']));
  } finally {
    wrapper.unmount();
  }
});

it('desktop cask header keeps Brewfile, Get and More actions', async () => {
  const { pinia, router } = await setup('/package/cask/source-example');
  const wrapper = mount(DetailHeader, {
    props: { kind: 'cask', token: 'source-example', detail: detail() },
    global: { plugins: [pinia, router, i18n] }
  });
  try {
    const header = wrapper.findComponent(OnPackageHeader);
    expect(header.props('stretchActions')).toBe(false);
    const actions = wrapper.get('[data-testid="package-header-actions"]');
    expect(actions.element.children).toHaveLength(3);
    expect(actions.element.children[0]!.textContent).toContain(i18n.global.t('brewfile.add'));
    expect(actions.element.children[2]!.querySelector('[aria-haspopup="menu"]')).not.toBeNull();
    expect(wrapper.findComponent(OnMenu).exists()).toBe(true);
    expect(actions.classes()).toContain('flex');
    expect(actions.classes()).not.toContain('grid-cols-2');
  } finally {
    wrapper.unmount();
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
