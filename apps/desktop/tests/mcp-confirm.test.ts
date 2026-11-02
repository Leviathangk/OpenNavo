import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import { formatBytes } from '@opennavo/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { OnButton } from '@opennavo/ui';
import McpConfirmPage from '@/pages/McpConfirmPage.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { setup } from './helpers';

const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
  vi.useRealTimers();
});

async function page(mock: string) {
  window.history.replaceState(null, '', `/?mock=${mock}`);
  const { pinia, router } = await setup('/mcp-confirm');
  const wrapper = mount(McpConfirmPage, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
  wrappers.push(wrapper);
  await flushPromises();
  return wrapper;
}

describe('Local MCP confirmation window', () => {
  it('Shows the request as plain text with the caller lines and the footer', async () => {
    const wrapper = await page('mcp-confirm-uninstall');
    const text = wrapper.text();
    expect(text).toContain('Rectangle');
    expect(text).toContain('/Applications/Rectangle.app');
    expect(wrapper.get('[data-test="mcp-confirm-caller"]').text()).toContain('Developer ID Application: Anthropic PBC');
    // The team is what makes the name checkable: it must sit beside the signer.
    expect(wrapper.get('[data-test="mcp-confirm-team"]').text()).toBe('Q6L2SF6YDW');
    expect(wrapper.get('[data-test="mcp-confirm-origin"]').text()).toBe('Claude（未验证）');
    expect(wrapper.get('[data-test="mcp-confirm-claims"]').text()).toBe('claude-ai');
    expect(wrapper.get('[data-test="mcp-confirm-footer"]').text()).toBe(
      '只有你点了「允许」才会执行。每个请求单独确认。'
    );
  });

  it('Shows an unsigned caller by path', async () => {
    const wrapper = await page('mcp-confirm-install');
    expect(wrapper.get('[data-test="mcp-confirm-caller"]').text()).toBe('未签名 · /opt/homebrew/bin/node');
  });

  it('Names no team for a caller whose signature could not be verified', async () => {
    // The Rust side only fills `teamId` next to a trusted signer; the window must
    // never invent one, or the two lines would disagree about who asked.
    const wrapper = await page('mcp-confirm-install');
    expect(wrapper.find('[data-test="mcp-confirm-team"]').exists()).toBe(false);
  });

  it('Defaults to Deny: focus, Return and Escape all deny; Allow waits', async () => {
    const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
    const wrapper = await page('mcp-confirm-install');
    expect(document.activeElement).toBe(wrapper.get('[data-test="mcp-confirm-deny"]').element);
    expect(wrapper.get('[data-test="mcp-confirm-allow"]').attributes('disabled')).toBeDefined();
    await wrapper.get('[data-test="mcp-confirm-allow"]').trigger('click');
    expect(answer).not.toHaveBeenCalled();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    await flushPromises();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(answer).toHaveBeenCalledWith('confirm-1', false);
    expect(answer.mock.calls.every(([, allow]) => allow === false)).toBe(true);
  });

  it('Puts Deny on the left, Allow on the right, with Deny first in Tab order', async () => {
    const wrapper = await page('mcp-confirm-install');
    const buttons = wrapper.findAll('footer button');
    expect(buttons.map(b => b.attributes('data-test'))).toEqual(['mcp-confirm-deny', 'mcp-confirm-allow']);
    expect(buttons.every(b => !b.attributes('tabindex') || b.attributes('tabindex') === '0')).toBe(true);
    expect(document.activeElement).toBe(buttons[0]?.element);
  });

  for (const key of ['Enter', 'Escape']) {
    it(`Still denies on ${key} after Allow is armed`, async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true });
      const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
      const wrapper = await page('mcp-confirm-install');
      await vi.advanceTimersByTimeAsync(1100);
      expect(wrapper.get('[data-test="mcp-confirm-allow"]').attributes('disabled')).toBeUndefined();
      (wrapper.get('[data-test="mcp-confirm-allow"]').element as HTMLElement).focus();
      window.dispatchEvent(new KeyboardEvent('keydown', { key }));
      await flushPromises();
      expect(answer).toHaveBeenCalledTimes(1);
      expect(answer).toHaveBeenCalledWith('confirm-1', false);
    });
  }

  it('Allows only by a click after the delay', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
    const wrapper = await page('mcp-confirm-install');
    await vi.advanceTimersByTimeAsync(1100);
    await wrapper.get('[data-test="mcp-confirm-allow"]').trigger('click');
    await flushPromises();
    expect(answer).toHaveBeenCalledWith('confirm-1', true);
  });

  it('Answers nothing at all from Allow before it is armed', async () => {
    // The guard in answer() is what still holds if the disabled attribute ever stops doing its job (a DOM change, or
    // a refactor that drops it), so it needs a test of its own. A plain click cannot reach it: OnButton swallows the
    // click of a disabled button, so the guard is exercised through the component's own click event instead.
    const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
    const wrapper = await page('mcp-confirm-install');
    expect(wrapper.get('[data-test="mcp-confirm-allow"]').attributes('disabled')).toBeDefined();
    wrapper
      .findAllComponents(OnButton)
      .find(button => button.attributes('data-test') === 'mcp-confirm-allow')!
      .vm.$emit('click');
    await flushPromises();
    expect(answer).not.toHaveBeenCalled();
  });

  it('Renders nothing from HTML and loads no remote content', () => {
    const source = readFileSync('src/pages/McpConfirmPage.vue', 'utf8');
    expect(source).not.toMatch(/v-html|innerHTML|<a\s|<iframe|<img\s|https?:\/\//);
  });
});

describe('Deep-link install confirmation window', () => {
  it('Says the request came from a web link and shows the opennavo:// source', async () => {
    const wrapper = await page('deeplink-confirm-install');
    expect(wrapper.get('h1').text()).toBe('确认来自网页链接的安装');
    expect(wrapper.text()).toContain('Ghostty');
    expect(wrapper.text()).toContain('cask/ghostty');
    expect(wrapper.get('[data-test="mcp-confirm-caller"]').text()).toBe('网页链接（opennavo://）');
    expect(wrapper.get('[data-test="mcp-confirm-source"]').text()).toBe(
      'opennavo://package/cask/ghostty?action=install'
    );
    expect(wrapper.find('[data-test="mcp-confirm-items"]').exists()).toBe(false);
  });

  it('Lists every app of a collection with the count, marking the skipped ones', async () => {
    const wrapper = await page('deeplink-confirm-collection');
    expect(wrapper.get('[data-test="mcp-confirm-summary"]').text()).toBe('共 6 个应用：安装 3 个，跳过 3 个');
    const rows = wrapper.findAll('[data-test="mcp-confirm-items"] li');
    expect(rows).toHaveLength(6);
    const disabled = wrapper.get('[data-skip="disabled"]');
    expect(disabled.text()).toContain('Legacy Tool');
    expect(disabled.text()).toContain('已停用，不加入队列');
    expect(wrapper.get('[data-skip="installed"]').text()).toContain('已安装');
    expect(wrapper.get('[data-skip="unsupported"]').text()).toContain('不支持');
  });

  it('Keeps the same default: Deny focused on the left, Return and Escape deny, Allow waits', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
    const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
    const wrapper = await page('deeplink-confirm-collection');
    const buttons = wrapper.findAll('footer button');
    expect(buttons.map(button => button.attributes('data-test'))).toEqual(['mcp-confirm-deny', 'mcp-confirm-allow']);
    expect(document.activeElement).toBe(buttons[0]!.element);
    await buttons[1]!.trigger('click');
    expect(answer).not.toHaveBeenCalled();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
    await flushPromises();
    expect(answer).toHaveBeenCalledWith('confirm-4', false);
    await vi.advanceTimersByTimeAsync(1000);
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(answer.mock.calls.every(([, allow]) => allow === false)).toBe(true);
  });
});

describe("OpenNavo's own destructive confirmations (#77)", () => {
  for (const flag of ['brewfile', 'cleanup', 'uninstall', 'delete', 'purge', 'brew-uninstall'])
    it(`${flag}: lists what will run without a caller line, and keeps the same safe default`, async () => {
      const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
      const wrapper = await page(`own-confirm-${flag}`);
      expect(wrapper.text()).not.toContain('未验证');
      expect(wrapper.find('[data-test="mcp-confirm-caller"]').exists()).toBe(false);
      expect(wrapper.findAll('li').length).toBeGreaterThan(0);
      const buttons = wrapper.findAll('footer button');
      expect(buttons.map(b => b.attributes('data-test'))).toEqual(['mcp-confirm-deny', 'mcp-confirm-allow']);
      expect(document.activeElement).toBe(buttons[0]?.element);
      expect(buttons[1]?.attributes('disabled')).toBeDefined();
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }));
      await flushPromises();
      expect(answer).toHaveBeenCalledWith('confirm-own', false);
      expect(answer.mock.calls.every(([, allow]) => allow === false)).toBe(true);
    });

  it('Shows the size and warns when items are deleted for good, not moved to the Trash', async () => {
    const moved = await page('own-confirm-uninstall');
    expect(moved.get('[data-test="mcp-confirm-summary"]').text()).toContain(
      formatBytes(24_310_000, { locale: 'zh-CN' })
    );
    expect(moved.find('[data-test="mcp-confirm-permanent"]').exists()).toBe(false);
    moved.unmount();
    wrappers.splice(0);
    const deleted = await page('own-confirm-delete');
    expect(deleted.find('[data-test="mcp-confirm-permanent"]').exists()).toBe(true);
  });

  it('Lists a Brewfile by its path, with taps, adoptions and the entries that will be skipped', async () => {
    const wrapper = await page('own-confirm-brewfile');
    expect(wrapper.text()).toContain('/Users/mock/Documents/Brewfile');
    expect(wrapper.text()).toContain('cask/font-maple-mono [subframe7536/tap]');
    expect(wrapper.get('[data-test="mcp-confirm-adopt"]').text()).toBe('接管已有的应用');
    expect(wrapper.get('[data-skip="declined"]').text()).toContain('你已拒绝');
  });

  it('Says what happens to the files instead of the MCP line, and says "can\'t be recovered" only once', async () => {
    const footer = async (flag: string) => {
      const wrapper = await page(`own-confirm-${flag}`);
      const text = wrapper.find('[data-test="mcp-confirm-footer"]');
      const result = [text.exists() ? text.text() : null, wrapper.find('[data-test="mcp-confirm-permanent"]').exists()];
      wrapper.unmount();
      wrappers.splice(0);
      return result;
    };
    expect(await footer('uninstall')).toEqual(['移到废纸篓后可以放回。', false]);
    expect(await footer('cleanup')).toEqual(['移到废纸篓的项目在清空前可以放回；命令类清理无法撤销。', false]);
    expect((await footer('brewfile'))[0]).toContain('Homebrew');
    for (const flag of ['delete', 'purge', 'brew-uninstall']) expect(await footer(flag)).toEqual([null, true]);
  });

  it('Translates the cleanup groups Rust names by key', async () => {
    const wrapper = await page('own-confirm-cleanup');
    expect(wrapper.text()).toContain('日志与崩溃报告');
    expect(wrapper.text()).not.toMatch(/\blogs\b/);
  });

  it('Keeps the file list for a cleanup an AI app asked for, and still names that app', async () => {
    // The Rust side gives an MCP cleanup the same file list OpenNavo's own window
    // draws, so the list must stay; the caller line must appear, or the user would
    // read it as something OpenNavo decided to do by itself.
    const wrapper = await page('mcp-confirm-cleanup');
    expect(wrapper.text()).toContain('确认清理');
    expect(wrapper.get('[data-test="mcp-confirm-items"]').findAll('li').length).toBeGreaterThan(0);
    expect(wrapper.text()).toContain('日志与崩溃报告');
    expect(wrapper.get('[data-test="mcp-confirm-caller"]').text()).toContain('Developer ID Application: Anthropic PBC');
    expect(wrapper.get('[data-test="mcp-confirm-team"]').text()).toBe('Q6L2SF6YDW');
    expect(wrapper.get('[data-test="mcp-confirm-footer"]').text()).toBe(
      '移到废纸篓的项目在清空前可以放回；命令类清理无法撤销。'
    );
  });
});

describe('Confirmation keyboard', () => {
  for (const mock of ['mcp-confirm-install', 'deeplink-confirm-install', 'deeplink-confirm-collection']) {
    it(`Never allows from the keyboard, even with Allow focused (${mock})`, async () => {
      vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] });
      const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
      const wrapper = await page(mock);
      await vi.advanceTimersByTimeAsync(1000);
      await flushPromises();
      const allow = wrapper.get('[data-test="mcp-confirm-allow"]').element as HTMLButtonElement;
      expect(allow.disabled).toBe(false);
      allow.focus();
      expect(document.activeElement).toBe(allow);
      for (const key of ['Enter', ' ']) {
        for (const type of ['keydown', 'keyup']) {
          const event = new KeyboardEvent(type, { key, bubbles: true, cancelable: true });
          allow.dispatchEvent(event);
          expect(event.defaultPrevented, `${type} ${key}`).toBe(true);
        }
      }
      await flushPromises();
      expect(answer).toHaveBeenCalled();
      expect(answer.mock.calls.every(([, value]) => value === false)).toBe(true);
    });
  }
});

// #104 A-2: a keyboard-only user has to be able to allow without a mouse, and without making Return alone approve.
describe('Keyboard allow chord (#104 A-2)', () => {
  async function armed(mock = 'mcp-confirm-install') {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
    const wrapper = await page(mock);
    await vi.advanceTimersByTimeAsync(1100);
    await flushPromises();
    const allow = wrapper.get('[data-test="mcp-confirm-allow"]').element as HTMLButtonElement;
    expect(allow.disabled).toBe(false);
    return { answer, wrapper, allow };
  }

  function chord(target: EventTarget, key: string, init: KeyboardEventInit = {}) {
    const event = new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init });
    target.dispatchEvent(event);
    return event;
  }

  for (const mock of ['mcp-confirm-install', 'deeplink-confirm-install', 'deeplink-confirm-collection']) {
    it(`Command-Return allows while Allow holds focus (${mock})`, async () => {
      const { answer, allow } = await armed(mock);
      allow.focus();
      expect(document.activeElement).toBe(allow);
      chord(allow, 'Enter', { metaKey: true });
      await flushPromises();
      expect(answer).toHaveBeenCalledTimes(1);
      expect(answer.mock.calls[0]?.[1]).toBe(true);
    });
  }

  it('Command-Return does nothing while Deny still holds the initial focus', async () => {
    // Allow is the approving control, so the chord only counts on it. Otherwise the default focus would carry an
    // allow shortcut and the deliberate Tab would be gone.
    const { answer, wrapper } = await armed();
    expect(document.activeElement).toBe(wrapper.get('[data-test="mcp-confirm-deny"]').element);
    chord(window, 'Enter', { metaKey: true });
    await flushPromises();
    expect(answer.mock.calls.every(([, value]) => value === false)).toBe(true);
  });

  it('Command-Return before the delay falls back to deny, never to allow', async () => {
    // Before arming Allow is disabled, so it cannot hold focus: the chord can never reach the approving control and
    // the ordinary Return rule applies. Denying is the safe side of that, and the point is that it never allows.
    const answer = vi.spyOn(commands, 'mcpConfirmAnswer');
    await page('mcp-confirm-install');
    chord(window, 'Enter', { metaKey: true });
    await flushPromises();
    expect(answer.mock.calls.every(([, value]) => value === false)).toBe(true);
  });

  for (const extra of [{ shiftKey: true }, { altKey: true }, { ctrlKey: true }]) {
    it(`Command-Return with ${Object.keys(extra)[0]} does not allow`, async () => {
      const { answer, allow } = await armed();
      allow.focus();
      chord(allow, 'Enter', { metaKey: true, ...extra });
      await flushPromises();
      expect(answer.mock.calls.every(([, value]) => value === false)).toBe(true);
    });
  }

  it('Command-Return mid-composition denies, not allows', async () => {
    // A Chinese or Japanese input method reports Enter while the user is only picking a candidate. Without this guard
    // the chord would approve a Local MCP write the moment someone chose a word with Command held.
    const { answer, allow } = await armed();
    allow.focus();
    const event = new KeyboardEvent('keydown', { key: 'Enter', metaKey: true, bubbles: true, cancelable: true });
    Object.defineProperty(event, 'isComposing', { value: true });
    allow.dispatchEvent(event);
    await flushPromises();
    // The handler ran (one answer went out) and refused: `every` alone would also pass on an empty call list, so a
    // handler that stopped reacting to Enter altogether would look identical to a correct guard.
    expect(answer).toHaveBeenCalledTimes(1);
    expect(answer).toHaveBeenCalledWith('confirm-1', false);
  });

  it('Space with the Command modifier still denies', async () => {
    const { answer, allow } = await armed();
    allow.focus();
    chord(allow, ' ', { metaKey: true });
    await flushPromises();
    expect(answer.mock.calls.every(([, value]) => value === false)).toBe(true);
  });

  it('Advertises the chord to assistive technology and shows it on the button', async () => {
    const { wrapper } = await armed();
    const allow = wrapper.get('[data-test="mcp-confirm-allow"]');
    expect(allow.attributes('aria-keyshortcuts')).toBe('Meta+Enter');
    expect(allow.get('kbd').text()).toBe('⌘⏎');
  });

  it('Names the Allow button by its copy alone, without the shortcut hint', async () => {
    // The hint is decoration: aria-keyshortcuts already announces the chord, so a screen reader reading the hint
    // again would say "Allow Command-Return" as if that were the name of the control.
    const { wrapper } = await armed();
    const allow = wrapper.get('[data-test="mcp-confirm-allow"]');
    // A button takes its accessible name from its contents, and aria-hidden subtrees are left out of that per
    // accname 2.0 §4.3.2, which is what this strips. No accessible-name library is installed, so the rule is spelled
    // out here rather than approximated by textContent.
    const clone = allow.element.cloneNode(true) as HTMLElement;
    clone.querySelectorAll('[aria-hidden="true"]').forEach(hidden => hidden.remove());
    expect(clone.textContent?.trim()).toBe(i18n.global.t('mcpConfirm.allow'));
    expect(clone.textContent).not.toContain('⌘⏎');
    expect(allow.get('kbd').attributes('aria-hidden')).toBe('true');
  });
});
