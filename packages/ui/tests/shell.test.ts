import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import { defineComponent, h } from 'vue';
import { LOGO, logoInlineShift } from '@opennavo/shared';
import OnLogo from '../src/components/OnLogo.vue';
import OnNavItem from '../src/components/OnNavItem.vue';

describe('OnLogo', () => {
  it('Reads the brand name when standalone and uses a tight bounding box with proportional width', () => {
    const wrapper = mount(OnLogo, { props: { size: 32 } });
    const svg = wrapper.get('svg');
    expect(svg.attributes('role')).toBe('img');
    expect(svg.attributes('aria-label')).toBe('OpenNavo');
    expect(svg.attributes('viewBox')).toBe(LOGO.small.markViewBox);
    expect(svg.attributes('height')).toBe('32');
    expect(svg.attributes('width')).toBe(String(Math.round(32 * LOGO.small.markAspect * 100) / 100));
    expect(wrapper.findAll('path')).toHaveLength(2);
    expect(svg.attributes('style')).toBeUndefined();
  });

  it('Shifts upward beside text to center the N and automatically shifts when a wordmark is present', () => {
    const shift = `transform: ${logoInlineShift(LOGO.small.nCenterOffset)}`;
    const inline = mount(OnLogo, { props: { inline: true } }).get('svg');
    const lockup = mount(OnLogo, { props: { wordmark: true } }).get('svg');
    expect(inline.attributes('style')).toContain(shift);
    expect(lockup.attributes('style')).toContain(shift);
  });

  it('Renders the standalone North Star as a square decoration centered with text by its area centroid', () => {
    const svg = mount(OnLogo, { props: { star: true, size: 13 } }).get('svg');
    expect(svg.attributes('viewBox')).toBe(LOGO.starViewBox);
    expect([svg.attributes('width'), svg.attributes('height')]).toEqual(['13', '13']);
    expect(svg.attributes('aria-hidden')).toBe('true');
    expect(svg.attributes('role')).toBeUndefined();
    expect(svg.findAll('path')).toHaveLength(1);
    expect(svg.attributes('style')).toContain(`transform: ${logoInlineShift(LOGO.starCenterOffset)}`);
  });

  it.each([16, 18, 22, 32, 33, 40])('Uses matching geometry and offsets at size %i', size => {
    const geometry = size <= 32 ? LOGO.small : LOGO;
    const svg = mount(OnLogo, { props: { size, inline: true } }).get('svg');
    expect(svg.attributes('viewBox')).toBe(geometry.markViewBox);
    expect(svg.findAll('path').map(path => path.attributes('d'))).toEqual([geometry.outerPath, geometry.innerPath]);
    expect(svg.attributes('style')).toContain(logoInlineShift(geometry.nCenterOffset));
    expect(svg.attributes('width')).toBe(String(Math.round(size * geometry.markAspect * 100) / 100));
  });

  it('Updates geometry when size changes and uses the small variant at the default 22 pixels', async () => {
    const wrapper = mount(OnLogo);
    expect(wrapper.find('path').attributes('d')).toBe(LOGO.small.outerPath);
    await wrapper.setProps({ size: 40 });
    expect(wrapper.find('path').attributes('d')).toBe(LOGO.outerPath);
  });

  it('Treats the mark as decorative with a wordmark and keeps gradient IDs unique across logos', () => {
    const first = mount(OnLogo, { props: { wordmark: true } });
    expect(first.text()).toBe('OpenNavo');
    expect(first.get('svg').attributes('aria-hidden')).toBe('true');

    const page = mount(defineComponent({ setup: () => () => h('div', [h(OnLogo), h(OnLogo, { wordmark: true })]) }));
    const ids = page.findAll('linearGradient').map(gradient => gradient.attributes('id'));
    expect(new Set(ids).size).toBe(ids.length);
  });
});

describe('OnNavItem', () => {
  it('Marks the selected item with aria-current and a selected background', () => {
    const wrapper = mount(OnNavItem, {
      props: { icon: 'compass', active: true, href: '#/discover' },
      slots: { default: '发现' }
    });
    expect(wrapper.element.tagName).toBe('A');
    expect(wrapper.attributes('aria-current')).toBe('page');
    expect(wrapper.classes()).toContain('bg-component-nav-active');
    expect(wrapper.find('svg').exists()).toBe(true);
  });

  it('Displays counts and badges and renders a button without a link', () => {
    const counted = mount(OnNavItem, { props: { count: 218 }, slots: { default: '已安装' } });
    expect(counted.element.tagName).toBe('BUTTON');
    expect(counted.text()).toBe('已安装218');
    expect(counted.attributes('aria-current')).toBeUndefined();
    const badged = mount(OnNavItem, {
      props: { count: 3, badge: 6, badgeLabel: '6 个可用更新' },
      slots: { default: '更新' }
    });
    expect(badged.get('[aria-label="6 个可用更新"]').text()).toBe('6');
    const zero = mount(OnNavItem, { props: { badge: 0 }, slots: { default: '更新' } });
    expect(zero.find('[aria-label]').exists()).toBe(false);
  });

  it('Shows an attention dot named by badgeLabel, in place of count and meta', () => {
    const item = mount(OnNavItem, {
      props: { dot: true, meta: '34 GB', badgeLabel: '2 个失效的后台项目仍在加载' },
      slots: { default: '清理电脑' }
    });
    expect(item.text()).toBe('清理电脑');
    expect(item.get('[role="img"]').attributes('aria-label')).toBe('2 个失效的后台项目仍在加载');
    const unnamed = mount(OnNavItem, { props: { dot: true }, slots: { default: '清理电脑' } });
    expect(unnamed.find('[role="img"]').exists()).toBe(false);
    expect(unnamed.find('[aria-hidden="true"]').exists()).toBe(true);
  });

  it('Shows meta text only when there is no count or badge', () => {
    expect(mount(OnNavItem, { props: { meta: '34 GB' }, slots: { default: '清理电脑' } }).text()).toBe('清理电脑34 GB');
    expect(mount(OnNavItem, { props: { count: 2, meta: '34 GB' }, slots: { default: '清理电脑' } }).text()).toBe(
      '清理电脑2'
    );
  });

  it('Accepts a router link component', () => {
    // Like RouterLink, the root creates href itself; undefined fallthrough attributes would overwrite it.
    const RouterLinkStub = defineComponent({
      props: { to: { type: String, required: true } },
      setup:
        (props, { slots }) =>
        () =>
          h('a', { href: `#${props.to}` }, slots.default?.())
    });
    const wrapper = mount(OnNavItem, { props: { as: RouterLinkStub, to: '/updates' }, slots: { default: '更新' } });
    expect(wrapper.get('a').attributes('href')).toBe('#/updates');
  });
});
