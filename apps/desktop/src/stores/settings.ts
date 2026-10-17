import { ref } from 'vue';
import { defineStore } from 'pinia';
import { commands, unwrap } from '@/ipc/client';
import type { AppSettings } from '@/ipc/client';
import type { MirrorInput, UpdateSource } from '@/ipc/bindings';

/** Settings (06 §11): settings_set replaces the whole object and returns normalized values. */
export const useSettingsStore = defineStore('settings', () => {
  const value = ref<AppSettings | null>(null);
  let pending: Promise<void> = Promise.resolve();

  // Serialize full-object writes and merge the latest saved settings at execution time.
  function serialize(operation: () => Promise<void>) {
    const result = pending.then(operation);
    pending = result.catch(() => {});
    return result;
  }

  async function load() {
    await serialize(async () => {
      value.value = (await unwrap(commands.settingsGet())) as AppSettings;
    });
  }

  async function save(next: AppSettings) {
    await serialize(async () => {
      value.value = (await unwrap(commands.settingsSet(next))) as AppSettings;
    });
  }

  async function update<K extends keyof AppSettings>(key: K, entry: AppSettings[K]) {
    await serialize(async () => {
      if (!value.value) return;
      value.value = (await unwrap(commands.settingsSet({ ...value.value, [key]: entry }))) as AppSettings;
    });
  }

  async function setUpdateSource(source: UpdateSource, enabled: boolean) {
    await serialize(async () => {
      if (!value.value) return;
      value.value = (await unwrap(
        commands.settingsSet({
          ...value.value,
          updateSources: { ...value.value.updateSources, [source]: enabled }
        })
      )) as AppSettings;
    });
  }

  async function setLanguage(locale: AppSettings['locale'] | 'system') {
    await serialize(async () => {
      if (!value.value) return;
      const next =
        locale === 'system'
          ? { ...value.value, localeMode: 'system' as const }
          : { ...value.value, locale, localeMode: 'manual' as const };
      value.value = (await unwrap(commands.settingsSet(next))) as AppSettings;
    });
  }

  async function saveCustomMirror(input: MirrorInput, activate = false) {
    await serialize(async () => {
      if (!value.value) throw new Error('Settings are not loaded');
      const customMirrors = value.value.customMirrors.filter(item => item.key !== input.key);
      const index = value.value.customMirrors.findIndex(item => item.key === input.key);
      customMirrors.splice(index < 0 ? customMirrors.length : index, 0, input);
      const choice = {
        key: input.key,
        apiDomain: input.apiDomain,
        bottleDomain: input.bottleDomain,
        brewGitRemote: input.brewGitRemote,
        coreGitRemote: input.coreGitRemote
      };
      const mirror = activate || value.value.mirror.key === input.key ? choice : value.value.mirror;
      value.value = (await unwrap(commands.settingsSet({ ...value.value, customMirrors, mirror }))) as AppSettings;
    });
  }

  async function removeCustomMirror(key: string) {
    await serialize(async () => {
      if (!value.value) throw new Error('Settings are not loaded');
      const mirror =
        value.value.mirror.key === key
          ? {
              key: 'official',
              apiDomain: null,
              bottleDomain: null,
              brewGitRemote: null,
              coreGitRemote: null
            }
          : value.value.mirror;
      value.value = (await unwrap(
        commands.settingsSet({
          ...value.value,
          customMirrors: value.value.customMirrors.filter(item => item.key !== key),
          mirror
        })
      )) as AppSettings;
    });
  }

  return { value, load, save, update, setUpdateSource, setLanguage, saveCustomMirror, removeCustomMirror };
});
