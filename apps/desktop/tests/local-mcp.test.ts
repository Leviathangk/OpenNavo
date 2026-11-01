import { flushPromises, mount } from '@vue/test-utils';
import type { VueWrapper } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import LocalMcpSettings from '@/components/settings/LocalMcpSettings.vue';
import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { useToasts } from '@/composables/useToasts';
import {
  MCP_COPIED_PATHS_KEY,
  MCP_COPIED_PATH_KEY,
  copiedMcpPaths,
  copiedMcpState,
  mcpAppMoved,
  mcpConfig,
  mcpLegacyStale,
  mcpMovedClients,
  normalizeMcpPath
} from '@/utils/mcpConfig';
import { setup } from './helpers';

const APP = '/Applications/OpenNavo.app/Contents/MacOS/opennavo-desktop';
const wrappers: VueWrapper[] = [];
afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount());
  useToasts().clear();
  localStorage.removeItem(MCP_COPIED_PATH_KEY);
  localStorage.removeItem(MCP_COPIED_PATHS_KEY);
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

async function page(mock = '') {
  window.history.replaceState(null, '', mock ? `/?mock=${mock}` : '/');
  const { pinia, router } = await setup('/settings/mcp');
  const wrapper = mount(LocalMcpSettings, { global: { plugins: [pinia, router, i18n] } });
  wrappers.push(wrapper);
  await flushPromises();
  return wrapper;
}
const checked = (wrapper: VueWrapper, test: string) =>
  wrapper.get(`[data-test="${test}"]`).attributes('aria-checked') === 'true';

describe('Local MCP config presets', () => {
  it('Launches the absolute app binary with the mcp subcommand in every preset', () => {
    expect(JSON.parse(mcpConfig('claude', APP))).toEqual({
      mcpServers: { opennavo: { command: APP, args: ['mcp'] } }
    });
    expect(JSON.parse(mcpConfig('cursor', APP))).toEqual({
      mcpServers: { opennavo: { command: APP, args: ['mcp'] } }
    });
    expect(JSON.parse(mcpConfig('generic', APP))).toEqual({ command: APP, args: ['mcp'] });
    expect(mcpConfig('codex', APP)).toBe(`[mcp_servers.opennavo]\ncommand = "${APP}"\nargs = ["mcp"]\n`);
  });

  it('Escapes TOML strings', () => {
    expect(mcpConfig('codex', '/Users/a "b"\\c/x')).toContain('command = "/Users/a \\"b\\"\\\\c/x"');
  });

  it('Detects a moved app only after a configuration was copied', () => {
    expect(mcpAppMoved(APP, null)).toBe(false);
    expect(mcpAppMoved(APP, APP)).toBe(false);
    expect(mcpAppMoved(APP, '/Users/me/Downloads/OpenNavo.app/Contents/MacOS/opennavo-desktop')).toBe(true);
    expect(mcpAppMoved(null, APP)).toBe(false);
  });

  it('Ignores case, trailing and repeated slashes when comparing paths', () => {
    expect(normalizeMcpPath('/Applications//OpenNavo.app/')).toBe('/applications/opennavo.app');
    expect(mcpAppMoved(APP, APP.toUpperCase())).toBe(false);
    expect(mcpAppMoved(APP, `${APP}/`)).toBe(false);
    expect(mcpAppMoved(APP, APP.replace('/Contents/', '//Contents/'))).toBe(false);
    // NFC and NFD spellings of the same folder name are one path.
    expect(mcpAppMoved('/Users/me/Caf\u00e9/x', '/Users/me/Cafe\u0301/x')).toBe(false);
  });

  it('Treats a case-only difference as a move on a case-sensitive volume', () => {
    const lower = APP.toLowerCase();
    // Case-insensitive (the macOS default): both spellings are the same app.
    expect(normalizeMcpPath(lower, false)).toBe(normalizeMcpPath(APP, false));
    expect(mcpAppMoved(APP, lower, false)).toBe(false);
    // Case-sensitive: the two spellings name different directories, so the copied path is stale.
    expect(normalizeMcpPath(lower, true)).not.toBe(normalizeMcpPath(APP, true));
    expect(mcpAppMoved(APP, lower, true)).toBe(true);
    // Trailing and repeated slashes still compare equal either way.
    expect(mcpAppMoved(APP, `${APP}/`, true)).toBe(false);
    expect(mcpMovedClients(APP, { claude: lower, cursor: APP }, true)).toEqual(['claude']);
  });

  it('Does not expand the old single-slot key into every client', () => {
    const OLD = '/Users/me/Downloads/OpenNavo.app/Contents/MacOS/opennavo-desktop';
    localStorage.setItem(MCP_COPIED_PATH_KEY, OLD);
    // The key names no client, so it is reported on its own instead of four guessed clients.
    expect(copiedMcpState()).toEqual({ paths: {}, legacy: OLD });
    expect(copiedMcpPaths()).toEqual({});
    expect(mcpMovedClients(APP)).toEqual([]);
    expect(mcpLegacyStale(APP, OLD)).toBe(true);
    expect(mcpLegacyStale(OLD, OLD)).toBe(false);
    expect(mcpLegacyStale(null, OLD)).toBe(false);
    // Once a configuration is copied for any client, the ambiguous key is gone and only that client is tracked.
    localStorage.setItem(MCP_COPIED_PATHS_KEY, JSON.stringify({ claude: OLD, cursor: APP, codex: 7 }));
    expect(copiedMcpState()).toEqual({ paths: { claude: OLD, cursor: APP }, legacy: null });
    expect(mcpLegacyStale(APP, null)).toBe(false);
    // A corrupt per-client value keeps the old key readable instead of forgetting the copy.
    localStorage.setItem(MCP_COPIED_PATHS_KEY, '{not json');
    expect(copiedMcpState()).toEqual({ paths: {}, legacy: OLD });
    localStorage.removeItem(MCP_COPIED_PATH_KEY);
    expect(copiedMcpState()).toEqual({ paths: {}, legacy: null });
  });
});

describe('Settings: Local MCP switches', () => {
  it('Is off by default with writes unavailable', async () => {
    const wrapper = await page();
    expect(checked(wrapper, 'mcp-enabled')).toBe(false);
    expect(checked(wrapper, 'mcp-write')).toBe(false);
    expect(wrapper.get('[data-test="mcp-write"]').attributes('disabled')).toBeDefined();
  });

  it('Shows the state the backend reports, never an optimistic one', async () => {
    const wrapper = await page();
    let finish!: () => void;
    const real = commands.mcpSetEnabled;
    vi.spyOn(commands, 'mcpSetEnabled').mockImplementationOnce(async enabled => {
      await new Promise<void>(resolve => (finish = resolve));
      return real(enabled);
    });
    await wrapper.get('[data-test="mcp-enabled"]').trigger('click');
    await flushPromises();
    expect(checked(wrapper, 'mcp-enabled')).toBe(false);
    finish();
    await flushPromises();
    expect(checked(wrapper, 'mcp-enabled')).toBe(true);
    await wrapper.get('[data-test="mcp-write"]').trigger('click');
    await flushPromises();
    expect(checked(wrapper, 'mcp-write')).toBe(true);
    // Turning Local MCP off also turns writes off.
    await wrapper.get('[data-test="mcp-enabled"]').trigger('click');
    await flushPromises();
    expect(checked(wrapper, 'mcp-enabled')).toBe(false);
    expect(checked(wrapper, 'mcp-write')).toBe(false);
  });

  it('Says Local MCP is still on when turning it off fails', async () => {
    const wrapper = await page('mcp-write-on,mcp-off-fails');
    await wrapper.get('[data-test="mcp-enabled"]').trigger('click');
    await flushPromises();
    expect(checked(wrapper, 'mcp-enabled')).toBe(true);
    expect(wrapper.find('[data-test="mcp-still-on"]').text()).toBe(i18n.global.t('settings.mcp.stillOn'));
    const alert = wrapper.get('[data-test="mcp-failure"]');
    expect(alert.attributes('role')).toBe('alert');
    expect(alert.text()).toContain(i18n.global.t('settings.mcp.offFailed'));
    expect(alert.text()).toContain('E_MCP_BROKER');
    expect((await commands.mcpStatus()).enabled).toBe(true);
  });

  it('Says writes are still allowed when turning them off fails', async () => {
    const wrapper = await page('mcp-write-on,mcp-off-fails');
    await wrapper.get('[data-test="mcp-write"]').trigger('click');
    await flushPromises();
    expect(checked(wrapper, 'mcp-write')).toBe(true);
    expect(wrapper.find('[data-test="mcp-write-still-on"]').exists()).toBe(true);
    expect(wrapper.get('[data-test="mcp-failure"]').text()).toContain(i18n.global.t('settings.mcp.writeOffFailed'));
  });
});

describe('Settings: Local MCP errors', () => {
  const stopped = {
    enabled: true,
    writeEnabled: false,
    running: false,
    socketPath: null,
    error: 'E_MCP_BROKER: bind failed',
    commandPath: APP,
    caseSensitive: false
  };

  it('Shows a startup error and that Local MCP is not running, with writes unavailable', async () => {
    vi.spyOn(commands, 'mcpStatus').mockResolvedValue(stopped);
    const wrapper = await page();
    const alert = wrapper.get('[data-test="mcp-failure"]');
    expect(alert.attributes('role')).toBe('alert');
    expect(wrapper.get('[data-test="mcp-not-running"]').text()).toBe(i18n.global.t('settings.mcp.notRunning'));
    expect(wrapper.get('[data-test="mcp-error"]').text()).toBe('E_MCP_BROKER: bind failed');
    expect(checked(wrapper, 'mcp-enabled')).toBe(true);
    expect(wrapper.get('[data-test="mcp-write"]').attributes('disabled')).toBeDefined();
  });

  it('Still lets writes be turned off while Local MCP is not running', async () => {
    vi.spyOn(commands, 'mcpStatus').mockResolvedValue({ ...stopped, writeEnabled: true });
    const wrapper = await page();
    expect(wrapper.get('[data-test="mcp-write"]').attributes('disabled')).toBeUndefined();
  });

  it('Shows no alert while Local MCP runs normally', async () => {
    const wrapper = await page('mcp-on');
    expect(wrapper.find('[data-test="mcp-failure"]').exists()).toBe(false);
    expect(wrapper.get('[data-test="mcp-write"]').attributes('disabled')).toBeUndefined();
  });

  it('Offers a retry when the first status read fails', async () => {
    const status = vi.spyOn(commands, 'mcpStatus').mockRejectedValueOnce(new Error('ipc down'));
    const wrapper = await page();
    expect(wrapper.get('[data-test="mcp-load-failed"]').text()).toContain(i18n.global.t('settings.mcp.statusFailed'));
    expect(wrapper.find('[data-test="mcp-enabled"]').exists()).toBe(false);
    await wrapper.get('[data-test="mcp-load-retry"]').trigger('click');
    await flushPromises();
    expect(status).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-test="mcp-load-failed"]').exists()).toBe(false);
    expect(wrapper.find('[data-test="mcp-enabled"]').exists()).toBe(true);
  });
});

describe('Settings: Local MCP configuration', () => {
  it('Copies the selected preset and warns once the app has moved', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const wrapper = await page();
    expect(wrapper.find('[data-test="mcp-moved"]').exists()).toBe(false);
    await wrapper.get('[data-test="mcp-copy"]').trigger('click');
    await flushPromises();
    expect(writeText).toHaveBeenCalledWith(mcpConfig('claude', APP));
    expect(JSON.parse(localStorage.getItem(MCP_COPIED_PATHS_KEY) ?? '{}')).toEqual({ claude: APP });
    wrapper.unmount();
    wrappers.pop();

    const OLD = '/Users/me/Downloads/OpenNavo.app/Contents/MacOS/opennavo-desktop';
    localStorage.setItem(MCP_COPIED_PATHS_KEY, JSON.stringify({ claude: OLD, cursor: OLD }));
    const moved = await page();
    expect(moved.get('[data-test="mcp-moved"]').text()).toContain(i18n.global.t('settings.mcp.moved'));
    const label = (client: string) => i18n.global.t(`settings.mcp.clients.${client}`);
    expect(moved.get('[data-test="mcp-moved-clients"]').text()).toContain(`${label('claude')}, ${label('cursor')}`);
    // Re-copying only Cursor leaves Claude Desktop flagged.
    const cursor = moved.findAll('button').find(button => button.text() === label('cursor'));
    await cursor!.trigger('click');
    await moved.get('[data-test="mcp-copy"]').trigger('click');
    await flushPromises();
    expect(moved.get('[data-test="mcp-moved-clients"]').text()).toBe(
      i18n.global.t('settings.mcp.movedClients', { clients: label('claude') })
    );
  });

  it('Warns about the old single-slot key without naming clients', async () => {
    const OLD = '/Users/me/Downloads/OpenNavo.app/Contents/MacOS/opennavo-desktop';
    localStorage.setItem(MCP_COPIED_PATH_KEY, OLD);
    const wrapper = await page();
    expect(wrapper.get('[data-test="mcp-moved"]').text()).toBe(i18n.global.t('settings.mcp.movedLegacy'));
    // No client is singled out: the old key cannot say which one was copied.
    expect(wrapper.find('[data-test="mcp-moved-clients"]').exists()).toBe(false);
    // Copying one configuration replaces the ambiguous record with a per-client one.
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
      configurable: true
    });
    await wrapper.get('[data-test="mcp-copy"]').trigger('click');
    await flushPromises();
    expect(localStorage.getItem(MCP_COPIED_PATH_KEY)).toBeNull();
    expect(wrapper.find('[data-test="mcp-moved"]').exists()).toBe(false);
  });

  it('Re-checks the copied paths when the window regains focus', async () => {
    const wrapper = await page();
    expect(wrapper.find('[data-test="mcp-moved"]').exists()).toBe(false);
    localStorage.setItem(
      MCP_COPIED_PATHS_KEY,
      JSON.stringify({ codex: '/Volumes/Old/OpenNavo.app/Contents/MacOS/opennavo-desktop' })
    );
    const status = vi.spyOn(commands, 'mcpStatus');
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    expect(status).toHaveBeenCalled();
    expect(wrapper.get('[data-test="mcp-moved-clients"]').text()).toContain(
      i18n.global.t('settings.mcp.clients.codex')
    );
  });

  it('Stops listening for focus and visibility after it unmounts', async () => {
    const add = vi.spyOn(window, 'addEventListener');
    const remove = vi.spyOn(window, 'removeEventListener');
    const docAdd = vi.spyOn(document, 'addEventListener');
    const docRemove = vi.spyOn(document, 'removeEventListener');
    const wrapper = await page();
    // Find the listeners this page registered, by handler identity, so unrelated ones do not count.
    const added = add.mock.calls.filter(([name]) => name === 'focus').map(([, handler]) => handler);
    const docAdded = docAdd.mock.calls.filter(([name]) => name === 'visibilitychange').map(([, handler]) => handler);
    expect(added.length).toBeGreaterThan(0);
    expect(docAdded.length).toBeGreaterThan(0);
    wrappers.splice(wrappers.indexOf(wrapper), 1);
    wrapper.unmount();
    for (const handler of added) expect(remove).toHaveBeenCalledWith('focus', handler);
    for (const handler of docAdded) expect(docRemove).toHaveBeenCalledWith('visibilitychange', handler);
    // A later focus no longer reads the status through this page.
    const status = vi.spyOn(commands, 'mcpStatus');
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    expect(status).not.toHaveBeenCalled();
  });
});

describe('Settings: Local MCP write request log', () => {
  const body = (test: string) => document.body.querySelectorAll(`[data-test="${test}"]`);

  it('Lists entries newest first with the caller lines but never the pid', async () => {
    const wrapper = await page('mcp-write-on');
    await wrapper.get('[data-test="mcp-audit-view"]').trigger('click');
    await flushPromises();
    const entries = [...body('mcp-audit-entry')].map(node => node.textContent ?? '');
    expect(entries).toHaveLength(2);
    expect(entries[0]).toContain('/Applications/Rectangle.app');
    expect(entries[0]).toContain('Developer ID Application: Anthropic PBC');
    expect(entries[0]).toContain('可能来自：Claude（未验证）');
    expect(entries[0]).toContain('自称：claude-ai');
    expect(entries[1]).toContain('未签名 · /opt/homebrew/bin/node');
    expect(entries.join(' ')).not.toContain('4182');
  });

  it('Shows the empty state', async () => {
    const wrapper = await page('mcp-on');
    await wrapper.get('[data-test="mcp-audit-view"]').trigger('click');
    await flushPromises();
    expect(body('mcp-audit-empty')).toHaveLength(1);
  });

  it('Clears only after confirmation', async () => {
    const wrapper = await page('mcp-write-on');
    const clear = vi.spyOn(commands, 'mcpAuditClear');
    await wrapper.get('[data-test="mcp-audit-clear"]').trigger('click');
    await flushPromises();
    expect(clear).not.toHaveBeenCalled();
    (document.body.querySelector('[data-on-confirm]') as HTMLElement).click();
    await flushPromises();
    expect(clear).toHaveBeenCalledTimes(1);
    const listed = await commands.mcpAuditList();
    expect(listed.status === 'ok' && listed.data).toEqual([]);
  });

  it('Reports a failed read', async () => {
    const wrapper = await page('mcp-write-on');
    vi.spyOn(commands, 'mcpAuditList').mockResolvedValueOnce({
      status: 'error',
      error: { code: 'E_AUDIT_UNAVAILABLE', message: 'x', detail: null }
    });
    await wrapper.get('[data-test="mcp-audit-view"]').trigger('click');
    await flushPromises();
    expect(body('mcp-audit-failed')).toHaveLength(1);
  });
});

describe('Settings: Local MCP status ordering', () => {
  it('A slow focus refresh never overwrites the state a switch change reported', async () => {
    const wrapper = await page();
    const stale = await commands.mcpStatus();
    let finish!: () => void;
    vi.spyOn(commands, 'mcpStatus').mockImplementationOnce(async () => {
      await new Promise<void>(resolve => (finish = resolve));
      return stale;
    });
    window.dispatchEvent(new Event('focus'));
    await flushPromises();
    await wrapper.get('[data-test="mcp-enabled"]').trigger('click');
    await flushPromises();
    expect(checked(wrapper, 'mcp-enabled')).toBe(true);
    finish();
    await flushPromises();
    expect(checked(wrapper, 'mcp-enabled')).toBe(true);
  });
});
