import { ref } from 'vue';
import { defineStore } from 'pinia';

export type HermesSettingsTab = 'model' | 'mac' | 'notifications' | 'tools';

/** Cross-component UI state for the assistant: the right rail and header open the same settings sheet. */
export const useHermesUiStore = defineStore('hermesUi', () => {
  const settingsOpen = ref(false);
  const settingsTab = ref<HermesSettingsTab>('model');
  const profilesOpen = ref(false);
  const agentProfilesOpen = ref(false);

  function openSettings(tab: HermesSettingsTab = 'model') {
    settingsTab.value = tab;
    settingsOpen.value = true;
  }

  function closeSettings() {
    settingsOpen.value = false;
  }

  function openProfiles() {
    profilesOpen.value = true;
  }

  function closeProfiles() {
    profilesOpen.value = false;
  }

  function openAgentProfiles() {
    agentProfilesOpen.value = true;
  }

  function closeAgentProfiles() {
    agentProfilesOpen.value = false;
  }

  return {
    settingsOpen,
    settingsTab,
    profilesOpen,
    agentProfilesOpen,
    openSettings,
    closeSettings,
    openProfiles,
    closeProfiles,
    openAgentProfiles,
    closeAgentProfiles
  };
});
