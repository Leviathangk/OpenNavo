import { flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { commands } from '@/ipc/client';
import { useSettingsStore } from '@/stores/settings';
import { setup } from './helpers';

afterEach(() => vi.restoreAllMocks());

describe('Serialized full-settings writes', () => {
  it('Serializes changes to separate sources and preserves unrelated settings', async () => {
    await setup();
    const settings = useSettingsStore();
    await settings.load();
    await settings.update('updateSources', { sparkle: false });
    const realSave = commands.settingsSet;
    let finish!: () => void;
    const save = vi.spyOn(commands, 'settingsSet').mockImplementationOnce(async next => {
      await new Promise<void>(resolve => {
        finish = resolve;
      });
      return realSave(next);
    });
    const first = settings.setUpdateSource('homebrew', false);
    const second = settings.setUpdateSource('mas', false);
    const third = settings.update('locale', 'en-US');
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(1);
    finish();
    await Promise.all([first, second, third]);
    expect(settings.value).toMatchObject({
      locale: 'en-US',
      updateSources: { sparkle: false, homebrew: false, mas: false }
    });
    expect(await commands.settingsGet()).toEqual({ status: 'ok', data: settings.value });
  });

  it('Keeps same-source changes in invocation order', async () => {
    await setup();
    const settings = useSettingsStore();
    await settings.load();
    const realSave = commands.settingsSet;
    let finish!: () => void;
    const save = vi.spyOn(commands, 'settingsSet').mockImplementationOnce(async next => {
      await new Promise<void>(resolve => {
        finish = resolve;
      });
      return realSave(next);
    });
    const off = settings.setUpdateSource('homebrew', false);
    const on = settings.setUpdateSource('homebrew', true);
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(1);
    finish();
    await Promise.all([off, on]);
    expect(save.mock.calls.map(([next]) => next.updateSources.homebrew)).toEqual([false, true]);
    expect(settings.value!.updateSources.homebrew).toBe(true);
    expect(await commands.settingsGet()).toEqual({ status: 'ok', data: settings.value });
  });

  it('Continues source changes after a rejected IPC without saving the failed value', async () => {
    await setup();
    const settings = useSettingsStore();
    await settings.load();
    await settings.update('updateSources', { homebrew: true });
    let fail!: (error: Error) => void;
    const save = vi.spyOn(commands, 'settingsSet').mockImplementationOnce(
      () =>
        new Promise((_, reject) => {
          fail = reject;
        })
    );
    const first = settings.setUpdateSource('homebrew', false);
    const rejected = expect(first).rejects.toThrow('IPC disconnected');
    const second = settings.setUpdateSource('mas', false);
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(1);
    fail(new Error('IPC disconnected'));
    await rejected;
    await second;
    expect(settings.value!.updateSources).toEqual({ homebrew: true, mas: false });
    expect(await commands.settingsGet()).toEqual({ status: 'ok', data: settings.value });
  });

  it('Immediate locale change preserves the preceding automatic-update change', async () => {
    await setup();
    const settings = useSettingsStore();
    await settings.load();
    const initial = { ...settings.value!, autoUpgradeFormulae: true };
    settings.value = initial;
    let finish!: () => void;
    const save = vi
      .spyOn(commands, 'settingsSet')
      .mockImplementationOnce(
        next =>
          new Promise(resolve => {
            finish = () => resolve({ status: 'ok', data: next });
          })
      )
      .mockImplementationOnce(async next => ({ status: 'ok', data: next }));
    const first = settings.update('autoUpgradeFormulae', false);
    const second = settings.update('locale', 'en-US');
    await flushPromises();
    expect(save).toHaveBeenCalledTimes(1);
    finish();
    await Promise.all([first, second]);
    expect(save.mock.calls[1]?.[0]).toMatchObject({ autoUpgradeFormulae: false, locale: 'en-US' });
    expect(settings.value).toMatchObject({ autoUpgradeFormulae: false, locale: 'en-US' });
  });

  it('Later saves survive earlier failure without inheriting failed edits', async () => {
    await setup();
    const settings = useSettingsStore();
    await settings.load();
    settings.value!.autoUpgradeFormulae = true;
    const save = vi
      .spyOn(commands, 'settingsSet')
      .mockResolvedValueOnce({
        status: 'error',
        error: { code: 'E_UNKNOWN', message: 'failed', detail: null }
      })
      .mockImplementationOnce(async next => ({ status: 'ok', data: next }));
    const first = settings.update('autoUpgradeFormulae', false);
    const second = settings.update('locale', 'en-US');
    await expect(first).rejects.toThrow('failed');
    await second;
    expect(save.mock.calls[1]?.[0]).toMatchObject({ autoUpgradeFormulae: true, locale: 'en-US' });
  });
});
