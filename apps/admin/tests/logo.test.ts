import { mount } from '@vue/test-utils';
import { expect, it, vi } from 'vitest';
import { LOGO, logoInlineShift } from '@opennavo/shared';
import SystemLogo from '../src/components/common/system-logo.vue';
import GlobalLogo from '../src/layouts/modules/global-logo/index.vue';
// Reuse the Vue component test environment to cover share-image entry points that bypass OnLogo.
import OgFrame from '../../web/app/components/og/OgFrame.vue';

vi.mock('@/store/modules/theme', () => ({ useThemeStore: () => ({ darkMode: true }) }));
vi.mock('@/locales', () => ({ $t: (key: string) => key }));

function mountLogo(showTitle: boolean) {
  return mount(GlobalLogo, {
    props: { showTitle },
    global: { components: { SystemLogo }, stubs: { RouterLink: { template: '<a><slot /></a>' } } }
  });
}

it('raises the mark beside the title so the N centres on the text, but not when the sider is collapsed', () => {
  const shift = `transform: ${logoInlineShift(LOGO.small.nCenterOffset)}`;
  expect(mountLogo(true).get('svg').attributes('style')).toContain(shift);
  expect(mountLogo(false).get('svg').attributes('style')).toBeUndefined();
});

it.each([16, 32, 40])('selects geometry for a %i px admin logo', size => {
  const geometry = size <= 32 ? LOGO.small : LOGO;
  const svg = mount(SystemLogo, { props: { size, inline: true } }).get('svg');
  expect(svg.attributes('viewBox')).toBe(geometry.viewBox);
  expect(svg.findAll('path').map(path => path.attributes('d'))).toEqual([geometry.outerPath, geometry.innerPath]);
  expect(svg.attributes('style')).toContain(logoInlineShift(geometry.nCenterOffset));
});

it('uses full brand geometry and gradients in the 64px OG header', () => {
  const frame = mount(OgFrame, {
    props: { tagline: 'The Homebrew App Store', platform: 'macOS app', site: 'opennavo.com' }
  });
  const svg = frame.get('svg');
  expect(svg.attributes('viewBox')).toBe(LOGO.markViewBox);
  expect(svg.attributes('width')).toBe(String(Math.round(64 * LOGO.markAspect)));
  expect(svg.attributes('height')).toBe('64');
  expect(svg.findAll('path').map(path => path.attributes('d'))).toEqual([LOGO.outerPath, LOGO.innerPath]);
  expect(svg.findAll('linearGradient')).toHaveLength(2);
  expect(frame.text()).toContain('The Homebrew App Store');
  expect(frame.text()).toContain('- macOS app');
  expect(frame.text()).toContain('opennavo.com');
});
