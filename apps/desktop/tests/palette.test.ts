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

describe('Command palette', () => {
  it('Empty input lists navigation; Down/Enter opens the destination', async () => {
    const { press, router } = await mountPalette();
    expect(document.body.textContent).toContain('前往发现');
    await press('ArrowDown');
    await press('Enter');
    expect(router.currentRoute.value.path).toBe('/categories');
  });

  it('Typing lists apps; Enter opens details', async () => {
    const { type, press, router, open } = await mountPalette();
    await type('obsidian');
    expect(document.body.querySelector('[role="option"]')?.textContent).toContain('Obsidian');
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
