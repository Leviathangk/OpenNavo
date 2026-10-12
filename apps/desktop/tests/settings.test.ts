import { flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { commands } from '@/ipc/client';
import { useSettingsStore } from '@/stores/settings';
import { setup } from './helpers';

afterEach(() => vi.restoreAllMocks());

describe('Serialized full-settings writes', () => {
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
