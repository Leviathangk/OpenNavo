import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { readFileSync } from 'node:fs';
import { formatBytes } from '@opennavo/shared';
import { afterEach, describe, expect, it, vi } from 'vitest';
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
