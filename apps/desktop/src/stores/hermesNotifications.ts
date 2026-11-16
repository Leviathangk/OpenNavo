import { computed, ref } from 'vue';
import { defineStore } from 'pinia';

export type HermesNotificationKind = 'turnDone' | 'turnError';

export interface HermesNotificationPrefs {
  enabled: boolean;
  turnDone: boolean;
  turnError: boolean;
}

export const HERMES_NOTIFICATION_STORAGE = 'ONV_HERMES_NOTIFICATIONS_V1';

const DEFAULT_PREFS: HermesNotificationPrefs = {
  enabled: true,
  turnDone: true,
  turnError: true
};

function readPrefs(): HermesNotificationPrefs {
  if (typeof localStorage === 'undefined') return { ...DEFAULT_PREFS };
  try {
    const parsed = JSON.parse(
      localStorage.getItem(HERMES_NOTIFICATION_STORAGE) ?? ''
    ) as Partial<HermesNotificationPrefs>;
    return {
      enabled: typeof parsed.enabled === 'boolean' ? parsed.enabled : DEFAULT_PREFS.enabled,
      turnDone: typeof parsed.turnDone === 'boolean' ? parsed.turnDone : DEFAULT_PREFS.turnDone,
      turnError: typeof parsed.turnError === 'boolean' ? parsed.turnError : DEFAULT_PREFS.turnError
    };
  } catch {
    return { ...DEFAULT_PREFS };
  }
}

export const useHermesNotificationsStore = defineStore('hermesNotifications', () => {
  const prefs = ref<HermesNotificationPrefs>(readPrefs());
  const enabled = computed(() => prefs.value.enabled);
  const turnDone = computed(() => prefs.value.turnDone);
  const turnError = computed(() => prefs.value.turnError);

  function persist() {
    try {
      localStorage.setItem(HERMES_NOTIFICATION_STORAGE, JSON.stringify(prefs.value));
    } catch {
      // Storage can be unavailable; the current window keeps this session's choice.
    }
  }

  function setEnabled(value: boolean) {
    prefs.value = { ...prefs.value, enabled: value };
    persist();
  }

  function setKind(kind: HermesNotificationKind, value: boolean) {
    prefs.value = { ...prefs.value, [kind]: value };
    persist();
  }

  function isEnabled(kind: HermesNotificationKind): boolean {
    return prefs.value.enabled && prefs.value[kind];
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('storage', event => {
      if (event.key === HERMES_NOTIFICATION_STORAGE) prefs.value = readPrefs();
    });
  }

  return { prefs, enabled, turnDone, turnError, setEnabled, setKind, isEnabled };
});
