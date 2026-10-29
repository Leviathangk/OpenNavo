import { afterEach, describe, expect, it, vi } from 'vitest';
import { ref } from 'vue';
import type { DesktopLocale as Locale } from '@opennavo/shared';
import { commands, events, unwrap } from '@/ipc/client';
import { disposeMockIpc, installMockIpc } from '@/ipc/mock';
import { startAppLocale } from '@/composables/startAppLocale';

const stops: (() => void)[] = [];
afterEach(() => {
  stops.splice(0).forEach(stop => stop());
  disposeMockIpc();
  vi.restoreAllMocks();
});

describe('Effective window locale', () => {
  it('Popovers initialize through localeGet without full settings', async () => {
    installMockIpc({ systemLocale: 'ja-JP' });
    const settings = vi.spyOn(commands, 'settingsGet');
    const current = ref<Locale>('en-US');
    const root = document.createElement('html');
    stops.push(await startAppLocale(current, root));
    expect(current.value).toBe('ja-JP');
    expect(root.lang).toBe('ja-JP');
    expect(settings).not.toHaveBeenCalled();
  });

  it('Main/popover react to locale:changed and synchronize html lang', async () => {
    installMockIpc({ systemLocale: 'ja-JP' });
    const main = ref<Locale>('en-US'),
      tray = ref<Locale>('en-US');
    const mainRoot = document.createElement('html'),
      trayRoot = document.createElement('html');
    stops.push(await startAppLocale(main, mainRoot), await startAppLocale(tray, trayRoot));
    await events.localeChanged.emit({ locale: 'ru-RU' });
    expect([main.value, tray.value, mainRoot.lang, trayRoot.lang]).toEqual(['ru-RU', 'ru-RU', 'ru-RU', 'ru-RU']);
  });

  it('Reread changes before subscription without overwriting newer events', async () => {
    installMockIpc();
    let finish!: (value: Awaited<ReturnType<typeof commands.localeGet>>) => void;
    vi.spyOn(commands, 'localeGet')
      .mockResolvedValueOnce({ status: 'ok', data: 'ja-JP' })
      .mockImplementationOnce(
        () =>
          new Promise(resolve => {
            finish = resolve;
          })
      );
    const current = ref<Locale>('en-US');
    const startup = startAppLocale(current, document.createElement('html'));
    await vi.waitFor(() => expect(finish).toBeDefined());
    await events.localeChanged.emit({ locale: 'ru-RU' });
    finish({ status: 'ok', data: 'es-ES' });
    stops.push(await startup);
    expect(current.value).toBe('ru-RU');
  });

  it('Mock broadcasts effective locale after system/manual selection saves', async () => {
    installMockIpc({ systemLocale: 'ja-JP' });
    const initial = await unwrap(commands.settingsGet());
    expect(initial).toMatchObject({ localeMode: 'system', locale: 'ja-JP' });
    const changed: Locale[] = [];
    stops.push(await events.localeChanged.listen(event => changed.push(event.payload.locale)));
    const manual = await unwrap(commands.settingsSet({ ...initial, localeMode: 'manual', locale: 'ru-RU' }));
    expect(await unwrap(commands.localeGet())).toBe('ru-RU');
    const system = await unwrap(commands.settingsSet({ ...manual, localeMode: 'system' }));
    expect(system).toMatchObject({ localeMode: 'system', locale: 'ja-JP' });
    expect(changed).toEqual(['ru-RU', 'ja-JP']);
  });
});
