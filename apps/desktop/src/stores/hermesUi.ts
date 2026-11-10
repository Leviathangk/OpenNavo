import { ref } from 'vue';
import { defineStore } from 'pinia';

export type HermesSettingsTab = 'model' | 'mac' | 'tools';

/** Cross-component UI state for the assistant: the right rail and header open the same settings sheet. */
export const useHermesUiStore = defineStore('hermesUi', () => {
  const settingsOpen = ref(false);
  const settingsTab = ref<HermesSettingsTab>('model');

  function openSettings(tab: HermesSettingsTab = 'model') {
    settingsTab.value = tab;
    settingsOpen.value = true;
  }

  function closeSettings() {
    settingsOpen.value = false;
  }

  return { settingsOpen, settingsTab, openSettings, closeSettings };
});
