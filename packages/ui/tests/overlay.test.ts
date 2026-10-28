import { config as testConfig } from '@vue/test-utils';
import { ref as localeRef } from 'vue';
import { ON_UI_LOCALE as fixtureLocaleKey } from '../src/composables/locale';
import { mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick } from 'vue';
import OnCommandPalette from '../src/components/OnCommandPalette.vue';
import type { OnCommandGroup } from '../src/components/OnCommandPalette.vue';
import OnConfirm from '../src/components/OnConfirm.vue';
import OnMenu from '../src/components/OnMenu.vue';
import OnModal from '../src/components/OnModal.vue';
import OnSheet from '../src/components/OnSheet.vue';
import OnToastRegion from '../src/components/OnToastRegion.vue';
import { moveFocus, usingKeyboard } from '../src/composables/overlay';
import { createToastQueue } from '../src/composables/toast';
import { withLocale } from './helpers';

const mounted: VueWrapper[] = [];

function track<T extends VueWrapper>(wrapper: T): T {
  mounted.push(wrapper);
  return wrapper;
}

afterEach(() => {
  for (const wrapper of mounted.splice(0)) wrapper.unmount();
  document.body.innerHTML = '';
});

/** Wait for overlay mounting/focus transfer, which occurs after nextTick. */
async function settle() {
  await nextTick();
  await nextTick();
}

const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"], [role="alertdialog"]');
const key = (target: EventTarget | null | undefined, init: KeyboardEventInit) =>
  target?.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }));

describe('OnModal', () => {
  it('Renders only when open, moves focus inside, labels the title and description, and locks scrolling', async () => {
    const opener = document.createElement('button');
    document.body.append(opener);
    opener.focus();
    const wrapper = track(
      mount(OnModal, {
        props: { open: false, title: '反馈问题', description: '我们会在 3 个工作日内回复。' },
        slots: { default: '<input id="field" />' },
        attachTo: document.body
      })
    );
    expect(dialog()).toBeNull();

    await wrapper.setProps({ open: true });
    await settle();
    const panel = dialog();
    expect(panel?.getAttribute('aria-modal')).toBe('true');
    const title = document.getElementById(panel?.getAttribute('aria-labelledby') ?? '');
    expect(title?.textContent).toBe('反馈问题');
    expect(document.getElementById(panel?.getAttribute('aria-describedby') ?? '')?.textContent?.trim()).toBe(
      '我们会在 3 个工作日内回复。'
    );
    expect(panel?.className).toContain('max-w-560px');
    // First focusable element is the top-right Close button.
    expect(document.activeElement?.getAttribute('aria-label')).toBe('关闭');
    expect(document.documentElement.style.overflow).toBe('hidden');

    await wrapper.setProps({ open: false });
    await settle();
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(opener);
    expect(document.documentElement.style.overflow).toBe('');
  });

  it('Closes with Escape, the close button, or a press and release on the backdrop', async () => {
    const wrapper = track(mount(OnModal, { props: { open: true, title: '设置' }, attachTo: document.body }));
    await settle();
    key(dialog(), { key: 'Escape' });
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false]);
    expect(wrapper.emitted('close')).toHaveLength(1);

    (document.body.querySelector('[aria-label="关闭"]') as HTMLElement).click();
    expect(wrapper.emitted('close')).toHaveLength(2);

    const backdrop = dialog()?.parentElement as HTMLElement;
    backdrop.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    backdrop.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(wrapper.emitted('close')).toHaveLength(3);
    // Pressing inside and releasing on the backdrop, as when selecting text, must not close.
    dialog()?.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    backdrop.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(wrapper.emitted('close')).toHaveLength(3);
  });

  it('Ignores Escape and backdrop clicks when not dismissible and traps Tab inside the panel', async () => {
    const wrapper = track(
      mount(OnModal, {
        props: { open: true, title: '导入', dismissible: false, initialFocus: '#first' },
        slots: { default: '<input id="first" /><button id="last">好</button>' },
        attachTo: document.body
      })
    );
    await settle();
    expect(document.activeElement?.id).toBe('first');
    key(dialog(), { key: 'Escape' });
    const backdrop = dialog()?.parentElement as HTMLElement;
    backdrop.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    backdrop.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(wrapper.emitted('close')).toBeUndefined();

    const last = document.getElementById('last') as HTMLElement;
    last.focus();
    key(last, { key: 'Tab' });
    expect(document.activeElement?.getAttribute('aria-label')).toBe('关闭');
    key(document.activeElement, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(last);
  });
});

describe('OnModal reopened while closing', () => {
  it('Stays open when it opens again before its exit transition ends', async () => {
    const wrapper = track(
      mount(OnModal, {
        props: { open: true, title: '开启权限' },
        attachTo: document.body,
        global: { stubs: { transition: false } }
      })
    );
    await settle();
    expect(dialog()).not.toBeNull();
    await wrapper.setProps({ open: false });
    await wrapper.setProps({ open: true });
    await new Promise(resolve => setTimeout(resolve, 100));
    await settle();
    expect(dialog()).not.toBeNull();
  });
});

describe('OnConfirm', () => {
  it('For ordinary confirmation, focuses confirm, delegates confirmation, and closes on cancel', async () => {
    const wrapper = track(
      mount(OnConfirm, {
        props: { open: true, title: '在 OpenNavo 中安装 Ghostty？', description: '来自网页的安装请求。' },
        attachTo: document.body
      })
    );
    await settle();
    expect(dialog()?.getAttribute('role')).toBe('alertdialog');
    expect(document.activeElement?.textContent?.trim()).toBe('确认');
    (document.activeElement as HTMLElement).click();
    expect(wrapper.emitted('confirm')).toHaveLength(1);
    expect(wrapper.emitted('update:open')).toBeUndefined();

    (document.body.querySelector('[data-on-cancel]') as HTMLElement).click();
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false]);
    expect(wrapper.emitted('cancel')).toHaveLength(1);
    key(dialog(), { key: 'Escape' });
    expect(wrapper.emitted('cancel')).toHaveLength(2);
  });

  it('For destructive confirmation, uses a red button, focuses cancel, ignores Escape, and disables cancel while running', async () => {
    const wrapper = track(
      mount(OnConfirm, {
        props: { open: true, title: '卸载 Docker Desktop？', tone: 'danger', confirmLabel: '卸载' },
        attachTo: document.body,
        global: withLocale('en-US')
      })
    );
    await settle();
    expect(document.activeElement?.textContent?.trim()).toBe('Cancel');
    const confirm = document.body.querySelector('[data-on-confirm]') as HTMLElement;
    expect(confirm.className).toContain('text-status-danger');
    expect(confirm.textContent?.trim()).toBe('卸载');
    key(dialog(), { key: 'Escape' });
    expect(wrapper.emitted('cancel')).toBeUndefined();

    await wrapper.setProps({ loading: true });
    expect(confirm.getAttribute('aria-busy')).toBe('true');
    expect((document.body.querySelector('[data-on-cancel]') as HTMLButtonElement).disabled).toBe(true);
  });
});

describe('OnSheet', () => {
  it('Slides in from the right at width 420 and retains the scroll lock until the last stacked dialog closes', async () => {
    const sheet = track(
      mount(OnSheet, {
        props: { open: true, title: '任务日志' },
        slots: { default: '日志全文' },
        attachTo: document.body
      })
    );
    await settle();
    const panel = dialog();
    expect(panel?.className).toContain('w-420px');
    expect(panel?.textContent).toContain('日志全文');

    const confirm = track(mount(OnConfirm, { props: { open: true, title: '取消任务？' }, attachTo: document.body }));
    await settle();
    expect(document.body.querySelectorAll('[aria-modal="true"]')).toHaveLength(2);
    await confirm.setProps({ open: false });
    await settle();
    expect(document.documentElement.style.overflow).toBe('hidden');

    (document.body.querySelector('[aria-label="关闭"]') as HTMLElement).click();
    expect(sheet.emitted('update:open')?.[0]).toEqual([false]);
    await sheet.setProps({ open: false });
    await settle();
    expect(document.documentElement.style.overflow).toBe('');
  });
});

describe('Focus ring after script focus', () => {
  const pointer = () => document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));

  it('Rings the first control only when the user opened the sheet by keyboard, not when the app or a pointer did', async () => {
    const calls: [string | null, FocusOptions | undefined][] = [];
    const original = HTMLElement.prototype.focus;
    const spy = vi.spyOn(HTMLElement.prototype, 'focus').mockImplementation(function (this: HTMLElement, options) {
      calls.push([this.getAttribute('aria-label'), options]);
      original.call(this, options);
    });
    const sheet = track(mount(OnSheet, { props: { open: false, title: '传感器' }, attachTo: document.body }));
    // WebKit rings script focus that no click in this document preceded, e.g. a sheet opened from the menu bar popover.
    pointer();
    await sheet.setProps({ open: true });
    await settle();
    expect(calls.at(-1)).toEqual(['关闭', { focusVisible: false }]);

    key(document.activeElement, { key: 'Escape' });
    await sheet.setProps({ open: false });
    await settle();
    await sheet.setProps({ open: true });
    await settle();
    expect(calls.at(-1)).toEqual(['关闭', undefined]);
    spy.mockRestore();
  });

  it('Leaves text fields showing focus however it arrives', () => {
    pointer();
    const input = document.createElement('input');
    const button = document.createElement('button');
    document.body.append(input, button);
    const inputFocus = vi.spyOn(input, 'focus');
    const buttonFocus = vi.spyOn(button, 'focus');
    moveFocus(input);
    moveFocus(button);
    expect(inputFocus).toHaveBeenCalledWith();
    expect(buttonFocus).toHaveBeenCalledWith({ focusVisible: false });
  });

  it('Treats shortcuts and bare modifiers as not navigating by keyboard', () => {
    pointer();
    key(document.body, { key: 'k', metaKey: true });
    key(document.body, { key: 'Shift', shiftKey: true });
    expect(usingKeyboard()).toBe(false);
    key(document.body, { key: 'Tab', shiftKey: true });
    expect(usingKeyboard()).toBe(true);
  });
});

describe('OnToastRegion', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Use real TransitionGroup instead of the default test stub to assert the rendered ol.
  const global = { stubs: { 'transition-group': false } };

  const items = [
    { id: 'a', tone: 'success' as const, title: '已安装 Ghostty' },
    {
      id: 'b',
      tone: 'danger' as const,
      title: '更新失败',
      description: 'Docker Desktop 需要管理员权限。',
      actionLabel: '查看'
    },
    { id: 'c', title: '已复制', duration: 0 },
    { id: 'd', title: '已加入 Brewfile 清单' }
  ];

  it('Shows at most three notifications, hides older ones first, and announces failures immediately', () => {
    const wrapper = track(mount(OnToastRegion, { props: { items }, global }));
    expect(wrapper.get('ol').attributes('aria-live')).toBe('polite');
    expect(wrapper.findAll('li').map(item => item.text())).toEqual([
      expect.stringContaining('更新失败'),
      expect.stringContaining('已复制'),
      expect.stringContaining('已加入 Brewfile 清单')
    ]);
    expect(wrapper.get('[role="alert"]').text()).toContain('需要管理员权限');
  });

  it('Dismisses after four seconds by default, pauses on hover, resumes the remaining time, and persists with duration zero', async () => {
    const wrapper = track(mount(OnToastRegion, { props: { items: items.slice(2) }, global }));
    vi.advanceTimersByTime(3000);
    await wrapper.get('ol').trigger('mouseenter');
    vi.advanceTimersByTime(5000);
    expect(wrapper.emitted('dismiss')).toBeUndefined();
    await wrapper.get('ol').trigger('mouseleave');
    vi.advanceTimersByTime(999);
    expect(wrapper.emitted('dismiss')).toBeUndefined();
    vi.advanceTimersByTime(1);
    expect(wrapper.emitted('dismiss')).toEqual([['d']]);
    vi.advanceTimersByTime(60_000);
    expect(wrapper.emitted('dismiss')).toHaveLength(1);
  });

  it('Supports action and close buttons', async () => {
    const wrapper = track(mount(OnToastRegion, { props: { items: [items[1] as (typeof items)[1]] }, global }));
    await wrapper.get('[role="alert"] button').trigger('click');
    expect(wrapper.emitted('action')).toEqual([['b']]);
    expect(wrapper.emitted('dismiss')).toEqual([['b']]);
    await wrapper.get('button[aria-label="关闭通知"]').trigger('click');
    expect(wrapper.emitted('dismiss')).toHaveLength(2);
  });

  it('Replaces notifications with the same ID in createToastQueue', () => {
    const queue = createToastQueue();
    const first = queue.push({ title: '正在更新 Docker Desktop', id: 'job-1' });
    queue.push({ title: '已复制' });
    queue.push({ title: '已更新 Docker Desktop', id: 'job-1', tone: 'success' });
    expect(first).toBe('job-1');
    expect(queue.items.value.map(item => item.title)).toEqual(['已复制', '已更新 Docker Desktop']);
    queue.dismiss('job-1');
    expect(queue.items.value).toHaveLength(1);
    queue.clear();
    expect(queue.items.value).toEqual([]);
  });
});

describe('OnMenu', () => {
  const items = [
    { key: 'finder', label: '在访达中显示', icon: 'folder' },
    { key: 'copy', label: '复制安装命令', icon: 'copy', shortcut: '⌘C' },
    { key: 'pin', label: '固定版本', icon: 'pin', disabled: true },
    { key: 'uninstall', label: '卸载', icon: 'trash', danger: true, separator: true }
  ];

  const menu = () => document.body.querySelector<HTMLElement>('[role="menu"]');

  it('Renders choice items as radio items with the current one checked, still reachable by keyboard', async () => {
    const choices = [
      { key: 'system', label: '跟随系统', checked: true },
      { key: 'light', label: '浅色', checked: false },
      { key: 'dark', label: '深色', checked: false }
    ];
    const wrapper = track(mount(OnMenu, { props: { items: choices, label: '外观' }, attachTo: document.body }));
    await wrapper.get('button').trigger('pointerdown');
    await wrapper.get('button').trigger('click');
    await nextTick();
    const radios = Array.from(menu()?.querySelectorAll<HTMLElement>('[role="menuitemradio"]') ?? []);
    expect(radios.map(item => item.getAttribute('aria-checked'))).toEqual(['true', 'false', 'false']);
    expect(radios[0]?.querySelector('svg, .on-icon, [aria-hidden]')).not.toBeNull();
    key(document.activeElement, { key: 'ArrowDown' });
    key(document.activeElement, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toContain('浅色');
    (document.activeElement as HTMLElement).click();
    await nextTick();
    expect(wrapper.emitted('select')).toEqual([['light']]);
  });

  it('Opens on click without preselecting an item, cycles past disabled items, then closes and restores focus after selection', async () => {
    const wrapper = track(mount(OnMenu, { props: { items }, attachTo: document.body }));
    const trigger = wrapper.get('button');
    expect(trigger.attributes('aria-label')).toBe('更多操作');
    await trigger.trigger('pointerdown');
    await trigger.trigger('click');
    await nextTick();
    expect(trigger.attributes('aria-expanded')).toBe('true');
    expect(menu()?.querySelectorAll('[role="separator"]')).toHaveLength(1);
    // Like native menus, a pointer open highlights nothing until hover or arrows.
    expect(document.activeElement).toBe(menu());

    key(document.activeElement, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toContain('在访达中显示');
    key(document.activeElement, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toContain('复制安装命令');
    key(document.activeElement, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toContain('卸载');
    expect((document.activeElement as HTMLElement).className).toContain('text-status-danger');
    key(document.activeElement, { key: 'ArrowDown' });
    expect(document.activeElement?.textContent).toContain('在访达中显示');
    key(document.activeElement, { key: 'End' });
    expect(document.activeElement?.textContent).toContain('卸载');

    (document.activeElement as HTMLElement).click();
    await nextTick();
    expect(wrapper.emitted('select')).toEqual([['uninstall']]);
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger.element);
  });

  it('Keyboard opens select the first item', async () => {
    const wrapper = track(mount(OnMenu, { props: { items }, attachTo: document.body }));
    const trigger = wrapper.get('button');
    await trigger.trigger('keydown', { key: 'Enter' });
    await trigger.trigger('click');
    await nextTick();
    expect(document.activeElement?.textContent).toContain('在访达中显示');
  });

  it('Focuses menu items only after positioning makes the menu visible, since browsers cannot focus visibility-hidden elements', async () => {
    const states: string[] = [];
    const original = HTMLElement.prototype.focus;
    const spy = vi.spyOn(HTMLElement.prototype, 'focus').mockImplementation(function (this: HTMLElement, options) {
      if (this.getAttribute('role') === 'menuitem')
        states.push(this.closest<HTMLElement>('[role="menu"]')?.style.visibility ?? '');
      original.call(this, options);
    });
    const wrapper = track(mount(OnMenu, { props: { items }, attachTo: document.body }));
    await wrapper.get('button').trigger('keydown', { key: 'ArrowDown' });
    await settle();
    spy.mockRestore();
    expect(states).toEqual(['']);
  });

  it('Opens on ArrowUp at the last item and closes on Escape or outside clicks', async () => {
    const wrapper = track(mount(OnMenu, { props: { items, label: 'More' }, attachTo: document.body }));
    const trigger = wrapper.get('button');
    await trigger.trigger('keydown', { key: 'ArrowUp' });
    await nextTick();
    expect(document.activeElement?.textContent).toContain('卸载');
    key(document.activeElement, { key: 'Escape' });
    await nextTick();
    expect(menu()).toBeNull();
    expect(document.activeElement).toBe(trigger.element);

    await trigger.trigger('click');
    await nextTick();
    expect(menu()).not.toBeNull();
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
    await nextTick();
    expect(menu()).toBeNull();
    expect(wrapper.emitted('select')).toBeUndefined();
  });
});

describe('OnCommandPalette', () => {
  const groups: OnCommandGroup[] = [
    {
      key: 'apps',
      label: '应用与工具',
      items: [
        {
          key: 'docker-desktop',
          label: 'Docker Desktop',
          description: 'App · 已安装 4.92.1',
          app: { kind: 'cask', token: 'docker-desktop', name: 'Docker Desktop' },
          badge: '可更新',
          hint: '↵ 打开'
        },
        { key: 'docker', label: 'docker', app: { kind: 'formula', token: 'docker', name: 'docker' }, hint: '⌘↵ 安装' }
      ]
    },
    {
      key: 'actions',
      label: '操作',
      items: [{ key: 'update', label: '更新 Docker Desktop', icon: 'refresh-cw', iconTone: 'accent' }]
    }
  ];

  const input = () => document.body.querySelector<HTMLInputElement>('input[role="combobox"]');
  const selected = () => document.body.querySelector('[role="option"][aria-selected="true"]')?.textContent ?? '';

  it('Focuses the input on open, cycles across groups with arrows, and emits update:query on input', async () => {
    const wrapper = track(
      mount(OnCommandPalette, { props: { open: true, query: 'dock', groups }, attachTo: document.body })
    );
    await settle();
    expect(document.activeElement).toBe(input());
    expect(dialog()?.getAttribute('aria-label')).toBe('命令面板');
    expect(document.body.textContent).toContain('应用与工具');
    expect(selected()).toContain('Docker Desktop');
    expect(input()?.getAttribute('aria-activedescendant')).toBe(
      document.body.querySelector('[role="option"][aria-selected="true"]')?.id
    );

    key(input(), { key: 'ArrowDown' });
    key(input(), { key: 'ArrowDown' });
    await nextTick();
    expect(selected()).toContain('更新 Docker Desktop');
    key(input(), { key: 'ArrowDown' });
    await nextTick();
    expect(selected()).toContain('Docker Desktop');
    key(input(), { key: 'ArrowUp' });
    await nextTick();
    expect(selected()).toContain('更新 Docker Desktop');

    const field = input() as HTMLInputElement;
    field.value = 'docker c';
    field.dispatchEvent(new Event('input'));
    expect(wrapper.emitted('update:query')?.[0]).toEqual(['docker c']);
  });

  it('Selects with Enter, adds meta with Command-Enter, copies with Command-C when no text is selected, and closes with Escape', async () => {
    const wrapper = track(
      mount(OnCommandPalette, { props: { open: true, query: '', groups }, attachTo: document.body })
    );
    await settle();
    key(input(), { key: 'Enter' });
    key(input(), { key: 'Enter', metaKey: true });
    const selects = wrapper.emitted('select') ?? [];
    expect(selects.map(([item, options]) => [(item as { key: string }).key, options])).toEqual([
      ['docker-desktop', { meta: false }],
      ['docker-desktop', { meta: true }]
    ]);

    key(input(), { key: 'c', metaKey: true });
    expect(wrapper.emitted('copy')).toHaveLength(1);
    const field = input() as HTMLInputElement;
    field.value = 'dock';
    field.setSelectionRange(0, 4);
    key(field, { key: 'c', metaKey: true });
    expect(wrapper.emitted('copy')).toHaveLength(1);

    key(input(), { key: 'Escape' });
    expect(wrapper.emitted('update:open')?.[0]).toEqual([false]);
  });

  it('Activates on mouse movement, selects on click, and shows empty or searching state without results', async () => {
    const wrapper = track(
      mount(OnCommandPalette, { props: { open: true, query: 'x', groups }, attachTo: document.body })
    );
    await settle();
    const options = document.body.querySelectorAll<HTMLElement>('[role="option"]');
    options[1]?.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }));
    await nextTick();
    expect(selected()).toContain('docker');
    options[1]?.click();
    expect(wrapper.emitted('select')?.[0]?.[0]).toMatchObject({ key: 'docker' });

    await wrapper.setProps({ groups: [] });
    expect(document.body.querySelector('[role="listbox"] [role="status"]')?.textContent?.trim()).toBe('没有匹配的结果');
    await wrapper.setProps({ loading: true });
    expect(document.body.querySelector('[role="listbox"] [role="status"]')?.textContent?.trim()).toBe('正在搜索…');
  });
});

describe('OnMenu custom triggers', () => {
  it('Provides attrs to the trigger slot with the same behavior as the default button after binding', async () => {
    const wrapper = mount(OnMenu, {
      props: { items: [{ key: 'copy', label: '复制安装命令' }] },
      slots: {
        trigger: `<template #trigger="{ attrs, open }"><button type="button" class="get" v-bind="attrs">获取{{ open ? '▴' : '▾' }}</button></template>`
      },
      attachTo: document.body
    });
    mounted.push(wrapper);
    const trigger = wrapper.get('button.get');
    expect(trigger.attributes('aria-haspopup')).toBe('menu');
    await trigger.trigger('keydown', { key: 'Enter' });
    await trigger.trigger('click');
    await settle();
    expect(trigger.attributes('aria-expanded')).toBe('true');
    expect(trigger.text()).toBe('获取▴');
    expect(document.activeElement?.textContent).toContain('复制安装命令');
    (document.activeElement as HTMLElement).click();
    await nextTick();
    expect(wrapper.emitted('select')).toEqual([['copy']]);
    expect(document.activeElement).toBe(trigger.element);
  });
});

// These interaction fixtures explicitly exercise the Chinese UI.
testConfig.global.provide = { ...testConfig.global.provide, [fixtureLocaleKey as symbol]: localeRef('zh-CN') };
