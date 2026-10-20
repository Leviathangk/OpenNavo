import { DOMWrapper, mount } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h, ref } from 'vue';
import OnSearchField from '../src/components/OnSearchField.vue';
import OnSegmented from '../src/components/OnSegmented.vue';
import OnTabs from '../src/components/OnTabs.vue';
import OnToggle from '../src/components/OnToggle.vue';
import OnTooltip from '../src/components/OnTooltip.vue';
import { nextRovingIndex } from '../src/composables/roving';
import { withLocale } from './helpers';

const views = [
  { value: 'all', label: '全部', count: 218 },
  { value: 'app', label: 'App', count: 38, dots: ['chart-series1', 'chart-series2'] },
  { value: 'cli', label: '命令行', count: 180 }
] as const;

describe('nextRovingIndex', () => {
  const all = [true, true, true];
  it('Cycles with arrow keys and moves to the ends with Home and End', () => {
    expect(nextRovingIndex('ArrowRight', 2, all)).toBe(0);
    expect(nextRovingIndex('ArrowLeft', 0, all)).toBe(2);
    expect(nextRovingIndex('ArrowDown', 0, all)).toBe(1);
    expect(nextRovingIndex('ArrowUp', 1, all)).toBe(0);
    expect(nextRovingIndex('Home', 2, [false, true, true])).toBe(1);
    expect(nextRovingIndex('End', 0, [true, true, false])).toBe(1);
  });

  it('Skips disabled items, starts at either end with no selection, and returns -1 when all are disabled', () => {
    expect(nextRovingIndex('ArrowRight', 0, [true, false, true])).toBe(2);
    expect(nextRovingIndex('ArrowRight', -1, all)).toBe(0);
    expect(nextRovingIndex('ArrowLeft', -1, all)).toBe(2);
    expect(nextRovingIndex('ArrowRight', 0, [false, false])).toBe(-1);
  });
});

describe('OnSegmented', () => {
  it('Uses radio-group semantics with only the selected item in the tab order', () => {
    const wrapper = mount(OnSegmented, { props: { modelValue: 'app', options: [...views], ariaLabel: '筛选' } });
    expect(wrapper.attributes('role')).toBe('radiogroup');
    expect(wrapper.attributes('aria-label')).toBe('筛选');
    const radios = wrapper.findAll('[role="radio"]');
    expect(radios.map(radio => radio.attributes('aria-checked'))).toEqual(['false', 'true', 'false']);
    expect(radios.map(radio => radio.attributes('tabindex'))).toEqual(['-1', '0', '-1']);
    expect(radios[1]?.classes()).toContain('bg-surface-raised');
    expect(radios[1]?.text()).toBe('App 38');
    expect(radios[1]?.findAll('[style]').map(dot => dot.attributes('style'))).toEqual([
      'background-color: var(--on-chart-series1);',
      'background-color: var(--on-chart-series2);'
    ]);
  });

  it('Changes selection with clicks and arrow keys', async () => {
    const wrapper = mount(OnSegmented, {
      props: { modelValue: 'all', options: [...views], size: 'sm' },
      attachTo: document.body
    });
    await wrapper.findAll('[role="radio"]')[2]?.trigger('click');
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['cli']);
    await wrapper.findAll('[role="radio"]')[0]?.trigger('click');
    expect(wrapper.emitted('update:modelValue')).toHaveLength(1);

    await wrapper.trigger('keydown', { key: 'ArrowLeft' });
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual(['cli']);
    await wrapper.setProps({ modelValue: 'cli' });
    await wrapper.trigger('keydown', { key: 'ArrowRight' });
    expect(wrapper.emitted('update:modelValue')?.[2]).toEqual(['all']);
    await wrapper.trigger('keydown', { key: 'Tab' });
    expect(wrapper.emitted('update:modelValue')).toHaveLength(3);
    wrapper.unmount();
  });
});

describe('OnToggle', () => {
  it('Provides switch semantics and toggling', async () => {
    const wrapper = mount(OnToggle, { props: { modelValue: false, ariaLabel: '自动更新' } });
    expect(wrapper.attributes('role')).toBe('switch');
    expect(wrapper.attributes('aria-checked')).toBe('false');
    expect(wrapper.classes()).toContain('bg-component-toggle-track');
    await wrapper.trigger('click');
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual([true]);
    await wrapper.setProps({ modelValue: true });
    expect(wrapper.attributes('aria-checked')).toBe('true');
    expect(wrapper.classes()).toContain('bg-brand-coral');
  });

  it('Does not toggle when disabled', async () => {
    const wrapper = mount(OnToggle, { props: { modelValue: true, disabled: true, ariaLabelledby: 'label-1' } });
    expect(wrapper.attributes('aria-labelledby')).toBe('label-1');
    await wrapper.trigger('click');
    expect(wrapper.emitted('update:modelValue')).toBeUndefined();
  });
});

describe('OnSearchField', () => {
  it('Supports input, Enter submission, and shortcut hints', async () => {
    const wrapper = mount(OnSearchField, { props: { modelValue: '', placeholder: '搜索 App', shortcut: '⌘K' } });
    const input = wrapper.get('input');
    expect(input.attributes('aria-label')).toBe('搜索 App');
    expect(wrapper.get('kbd').text()).toBe('⌘K');
    await input.setValue('vscode');
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['vscode']);
    await wrapper.setProps({ modelValue: 'vscode' });
    await input.trigger('keydown', { key: 'Enter' });
    expect(wrapper.emitted('submit')?.[0]).toEqual(['vscode']);
    expect(wrapper.find('kbd').exists()).toBe(false);
  });

  it('Clears with the clear button or Escape and does not submit Enter during IME composition', async () => {
    const wrapper = mount(OnSearchField, {
      props: { modelValue: 'rg', ariaLabel: '搜索' },
      global: withLocale('en-US')
    });
    const clear = wrapper.get('button');
    expect(clear.attributes('aria-label')).toBe('Clear');
    await clear.trigger('click');
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['']);
    expect(wrapper.emitted('clear')).toHaveLength(1);
    await wrapper.get('input').trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('clear')).toHaveLength(2);
    await wrapper.get('input').trigger('keydown', { key: 'Enter', isComposing: true });
    expect(wrapper.emitted('submit')).toBeUndefined();
  });

  it('Passes Escape outward when empty, supports external focus, and hides clear when disabled', async () => {
    const wrapper = mount(OnSearchField, { props: { modelValue: '' }, attachTo: document.body });
    await wrapper.get('input').trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('clear')).toBeUndefined();
    (wrapper.vm as unknown as { focus: () => void }).focus();
    expect(document.activeElement).toBe(wrapper.get('input').element);
    (wrapper.vm as unknown as { blur: () => void }).blur();
    expect(document.activeElement).not.toBe(wrapper.get('input').element);
    wrapper.unmount();

    const disabled = mount(OnSearchField, { props: { modelValue: 'x', disabled: true } });
    expect(disabled.find('button').exists()).toBe(false);
    expect(disabled.classes()).toContain('opacity-50');
  });
});

const tabItems = [
  { value: 'overview', label: '概览' },
  { value: 'releases', label: '版本记录', count: 42 },
  { value: 'deps', label: '依赖', disabled: true },
  { value: 'details', label: '安装细节' }
] as const;

describe('OnTabs', () => {
  it('Provides tab semantics, a selection indicator, and counts', () => {
    const wrapper = mount(OnTabs, { props: { modelValue: 'releases', items: [...tabItems], ariaLabel: '详情' } });
    expect(wrapper.attributes('role')).toBe('tablist');
    const tabs = wrapper.findAll('[role="tab"]');
    expect(tabs.map(tab => tab.attributes('aria-selected'))).toEqual(['false', 'true', 'false', 'false']);
    expect(tabs[1]?.text()).toBe('版本记录 42');
    expect(tabs[1]?.find('.bg-component-tab-indicator').exists()).toBe(true);
    expect(tabs[2]?.attributes('disabled')).toBeDefined();
  });

  it('Changes selection with clicks and arrow keys while skipping disabled items', async () => {
    const wrapper = mount(OnTabs, { props: { modelValue: 'releases', items: [...tabItems] }, attachTo: document.body });
    await wrapper.findAll('[role="tab"]')[0]?.trigger('click');
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['overview']);
    await wrapper.trigger('keydown', { key: 'ArrowRight' });
    expect(wrapper.emitted('update:modelValue')?.[1]).toEqual(['details']);
    await wrapper.trigger('keydown', { key: 'Enter' });
    expect(wrapper.emitted('update:modelValue')).toHaveLength(2);
    wrapper.unmount();
  });

  it('Renders href tabs as navigation links with aria-current on the active item', async () => {
    const items = tabItems.map(item => ({ ...item, href: `/apps/ghostty/${item.value}` }));
    const wrapper = mount(OnTabs, { props: { modelValue: 'overview', items, ariaLabel: '详情页签' } });
    expect(wrapper.element.tagName).toBe('NAV');
    const links = wrapper.findAll('a');
    expect(links).toHaveLength(3);
    expect(links[0]?.attributes('aria-current')).toBe('page');
    expect(links[0]?.attributes('to')).toBeUndefined();
    expect(wrapper.find('span[aria-disabled="true"]').text()).toBe('依赖');
    await wrapper.trigger('keydown', { key: 'ArrowRight' });
    expect(wrapper.emitted('update:modelValue')).toBeUndefined();

    // Like RouterLink, root generates href; undefined fallthrough attributes would overwrite it.
    const RouterLinkStub = defineComponent({
      props: { to: { type: String, required: true } },
      setup:
        (props, { slots }) =>
        () =>
          h('a', { href: `#${props.to}` }, slots.default?.())
    });
    const routed = mount(OnTabs, { props: { modelValue: 'overview', items, linkAs: RouterLinkStub } });
    expect(routed.get('a').attributes('href')).toBe('#/apps/ghostty/overview');
  });
});

describe('OnTooltip', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const mountTooltip = (props: Record<string, unknown> = {}) =>
    mount(OnTooltip, {
      props: { content: '复制安装命令', ...props },
      slots: { default: '<button type="button">复制</button>' },
      attachTo: document.body
    });

  const tooltipFor = (wrapper: ReturnType<typeof mountTooltip>) =>
    new DOMWrapper<HTMLElement>(document.getElementById(wrapper.get('button').attributes('aria-describedby')!)!);

  it('Appears after 400 ms of hover, disappears immediately on leave, and associates the trigger with its description', async () => {
    const wrapper = mountTooltip();
    const tooltip = tooltipFor(wrapper);
    expect(wrapper.get('button').attributes('aria-describedby')).toBe(tooltip.attributes('id'));
    await wrapper.get('span.relative').trigger('mouseenter');
    vi.advanceTimersByTime(399);
    await wrapper.vm.$nextTick();
    expect(tooltip.classes()).toContain('invisible');
    vi.advanceTimersByTime(1);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    expect(tooltip.classes()).toContain('visible');
    await wrapper.get('span.relative').trigger('mouseleave');
    expect(tooltip.classes()).toContain('invisible');
    wrapper.unmount();
  });

  it('Also opens on keyboard focus and closes with Escape', async () => {
    const wrapper = mountTooltip({ delay: 0, placement: 'bottom' });
    const tooltip = tooltipFor(wrapper);
    await wrapper.get('button').trigger('focusin');
    vi.advanceTimersByTime(0);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    expect(tooltip.classes()).toContain('visible');
    await wrapper.get('span.relative').trigger('keydown', { key: 'Escape' });
    expect(tooltip.classes()).toContain('invisible');
    await wrapper.get('span.relative').trigger('keydown', { key: 'Escape' });
    await wrapper.get('button').trigger('focusout');
    expect(tooltip.classes()).toContain('invisible');
    wrapper.unmount();
  });

  it('Stays hidden when disabled or empty and removes the description association', async () => {
    const wrapper = mountTooltip({ disabled: true, placement: 'left' });
    const tooltip = new DOMWrapper(document.body.querySelector('[role="tooltip"]')!);
    expect(wrapper.get('button').attributes('aria-describedby')).toBeUndefined();
    await wrapper.get('span.relative').trigger('mouseenter');
    vi.advanceTimersByTime(500);
    await wrapper.vm.$nextTick();
    expect(tooltip.classes()).toContain('invisible');

    await wrapper.setProps({ disabled: false });
    expect(wrapper.get('button').attributes('aria-describedby')).toBeDefined();
    await wrapper.get('span.relative').trigger('mouseenter');
    vi.advanceTimersByTime(400);
    await wrapper.setProps({ disabled: true });
    expect(tooltip.classes()).toContain('invisible');
    await wrapper.setProps({ disabled: false, content: '', placement: 'right' });
    expect(wrapper.get('button').attributes('aria-describedby')).toBeUndefined();
    wrapper.unmount();
  });

  it('Keeps the description outside scrolling content, flips at the top edge and stays within the right edge', async () => {
    const wrapper = mountTooltip({ delay: 0 });
    const tooltip = tooltipFor(wrapper);
    expect(tooltip.element.parentElement).toBe(document.body);
    expect(wrapper.find('[role="tooltip"]').exists()).toBe(false);
    expect(tooltip.isVisible()).toBe(false);
    vi.spyOn(wrapper.get('span.relative').element, 'getBoundingClientRect').mockReturnValue({
      left: window.innerWidth - 18,
      right: window.innerWidth - 8,
      top: 2,
      bottom: 14,
      width: 10,
      height: 12
    } as DOMRect);
    vi.spyOn(tooltip.element, 'getBoundingClientRect').mockReturnValue({ width: 200, height: 30 } as DOMRect);

    await wrapper.get('span.relative').trigger('mouseenter');
    vi.advanceTimersByTime(0);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    expect(tooltip.isVisible()).toBe(true);
    expect(tooltip.element.style.left).toBe(`${window.innerWidth - 208}px`);
    expect(tooltip.element.style.top).toBe('20px');

    // Scrolling an ancestor closes the floating tooltip instead of leaving it at stale coordinates.
    wrapper.get('span.relative').element.dispatchEvent(new Event('scroll'));
    await wrapper.vm.$nextTick();
    expect(tooltip.isVisible()).toBe(false);
    wrapper.unmount();
    vi.restoreAllMocks();
  });
});

describe('Locale injection', () => {
  it('Provides a reactive locale through the createOnUi plugin', async () => {
    const { createOnUi } = await import('../src/composables/locale');
    const { default: OnGetButton } = await import('../src/components/OnGetButton.vue');
    const locale = ref<'zh-CN' | 'en-US'>('zh-CN');
    const wrapper = mount(OnGetButton, { props: { state: 'open' }, global: { plugins: [createOnUi({ locale })] } });
    expect(wrapper.text()).toBe('打开');
    locale.value = 'en-US';
    await wrapper.vm.$nextTick();
    expect(wrapper.text()).toBe('Open');
  });
});

describe('OnTooltip lazy (C7)', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('Renders its description only once the trigger is used, without a description link', async () => {
    const wrapper = mount(OnTooltip, {
      props: { content: '近 30 天：17,394', lazy: true, delay: 0 },
      slots: { default: '<span class="bar">bar</span>' },
      attachTo: document.body
    });
    // Nothing reaches the page (or the server HTML, where teleports land first in the body) before a hover.
    expect(document.body.querySelector('[role="tooltip"]')).toBeNull();
    expect(wrapper.get('.bar').attributes('aria-describedby')).toBeUndefined();
    await wrapper.get('span.relative').trigger('mouseenter');
    vi.advanceTimersByTime(0);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    const tooltip = document.body.querySelector<HTMLElement>('[role="tooltip"]');
    expect(tooltip?.textContent?.trim()).toBe('近 30 天：17,394');
    await wrapper.get('span.relative').trigger('mouseleave');
    expect(tooltip?.classList.contains('invisible')).toBe(true);
    wrapper.unmount();
  });
});
