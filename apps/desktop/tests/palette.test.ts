import { flushPromises, mount } from '@vue/test-utils';
import { afterEach, describe, expect, it } from 'vitest';
import { defineComponent, h, ref } from 'vue';
import CommandPalette from '@/components/palette/CommandPalette.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/bindings';
import { unwrap } from '@/ipc/client';
import { startLocalState } from '@/stores';
import { setup } from './helpers';

// M3-11: complete command palette keyboard support (typing, up/down, Enter to open, Command-Enter to install, Escape to close).
async function mountPalette() {
  const context = await setup('/discover', { tickMs: 1000 });
  await startLocalState();
  const open = ref(true);
  const Host = defineComponent({
    setup: () => () =>
      h(CommandPalette, {
        open: open.value,
        'onUpdate:open': (value: boolean) => (open.value = value)
      })
  });
  const wrapper = mount(Host, {
    global: { plugins: [context.pinia, context.router, i18n] },
    attachTo: document.body
  });
  await flushPromises();
  const input = () => document.body.querySelector<HTMLInputElement>('[role="combobox"]');
  async function type(text: string) {
    const element = input();
    if (!element) throw new Error('面板没有打开');
    element.value = text;
    element.dispatchEvent(new Event('input'));
    await flushPromises();
    await flushPromises();
  }
  async function press(key: string, init: KeyboardEventInit = {}) {
    input()?.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, ...init }));
    await flushPromises();
  }
  return { ...context, open, wrapper, input, type, press };
}

afterEach(() => {
  document.body.innerHTML = '';
});

const optionTexts = () =>
  [...document.body.querySelectorAll('[role="option"]')].map(option => option.textContent ?? '');
const activeText = () => document.body.querySelector('[role="option"][aria-selected="true"]')?.textContent ?? '';
/** The labels of the groups on screen, top to bottom. */
const groupLabels = () =>
  [...document.body.querySelectorAll<HTMLElement>('[role="group"]')]
    .filter(group => group.style.display !== 'none')
    .map(group => group.firstElementChild?.textContent?.trim());
/** Arrow down to the app row of `name`: its hint (↵ open, ⌘↵ install) tells it from an action naming the same app. */
async function moveToApp(press: (key: string) => Promise<void>, name: string) {
  for (let step = 0; step < 12 && !(activeText().includes(name) && activeText().includes('↵')); step++)
    await press('ArrowDown');
  expect(activeText()).toContain(name);
}

describe('Command palette', () => {
  it('Empty input lists navigation; Down/Enter opens the destination', async () => {
    const { press, router } = await mountPalette();
    expect(document.body.textContent).toContain('前往发现');
    await press('ArrowDown');
    await press('Enter');
    expect(router.currentRoute.value.path).toBe('/categories');
  });

  it('Typing lists the actions first, with all the results on top; Enter goes to them', async () => {
    const { type, press, router, open } = await mountPalette();
    await type('obsidian');
    expect(groupLabels().slice(0, 2)).toEqual(['操作', 'App']);
    expect(optionTexts()[0]).toContain('查看「obsidian」的全部结果');
    expect(activeText()).toContain('查看「obsidian」的全部结果');
    // The actions of the best match come after it, and the apps after the actions.
    expect(optionTexts().findIndex(text => text.includes('在访达中显示'))).toBe(1);
    expect(optionTexts().findIndex(text => text.includes('↵ 打开'))).toBeGreaterThan(1);
    await press('Enter');
    expect(router.currentRoute.value.path).toBe('/search');
    expect(router.currentRoute.value.query.q).toBe('obsidian');
    expect(open.value).toBe(false);
  });

  it('The all-results row is there before any app is found, and still first once they arrive', async () => {
    const { type } = await mountPalette();
    await type('zzqxjv');
    expect(optionTexts()).toHaveLength(1);
    expect(optionTexts()[0]).toContain('查看「zzqxjv」的全部结果');
    await type('obsidian');
    expect(optionTexts()[0]).toContain('查看「obsidian」的全部结果');
    expect(optionTexts().length).toBeGreaterThan(2);
  });

  it('Down from the actions reaches the apps; Enter opens details', async () => {
    const { type, press, router, open } = await mountPalette();
    await type('obsidian');
    await moveToApp(press, 'Obsidian');
    await press('Enter');
    expect(router.currentRoute.value.path).toBe('/package/cask/obsidian');
    expect(open.value).toBe(false);
  });

  it('Cask-only results exclude command-line tools', async () => {
    const { type } = await mountPalette();
    await type('ripgrep');
    const options = [...document.body.querySelectorAll('[role="option"]')].map(option => option.textContent ?? '');
    expect(options.some(text => text.startsWith('ripgrep'))).toBe(false);
  });

  it('Updatable packages offer Update; Command-Enter installs uninstalled packages', async () => {
    const { type, press } = await mountPalette();
    await type('docker');
    expect(document.body.textContent).toContain('更新 Docker');
    await type('zed');
    await moveToApp(press, 'Zed');
    await press('Enter', { metaKey: true });
    const active = await unwrap(commands.taskListActive());
    expect(active.some(task => task.op === 'install' && task.target?.token === 'zed')).toBe(true);
  });

  it('Escape closes palette', async () => {
    const { press, open } = await mountPalette();
    await press('Escape');
    expect(open.value).toBe(false);
  });
});
