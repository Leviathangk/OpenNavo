import { watch } from 'vue';
import { useStateSubscriptions } from './stateSubscriptions';
import { events } from '@/ipc/client';
import { useCatalogStore } from './catalog';
import { useCleanupStore } from './cleanup';
import { useSecurityStore } from './security';
import { useFirewallStore } from './firewall';
import { useMasStore } from './mas';
import { useLinksStore } from './links';
import { useDownloadsStore } from './downloads';
import { useMonitorStore } from './monitor';
import { useEnvStore } from './env';
import { useLibraryStore } from './library';
import { useLocalAppsStore } from './localApps';
import { useUninstallStore } from './uninstall';
import { useNamesStore } from './names';
import { usePermissionPromptStore } from './permissionPrompt';
import { usePermissionsStore } from './permissions';
import { useSettingsStore } from './settings';
import { useTasksStore } from './tasks';
import { useUpdatesStore } from './updates';

export {
  useCatalogStore,
  useEnvStore,
  useLibraryStore,
  useLocalAppsStore,
  usePermissionPromptStore,
  usePermissionsStore,
  useSettingsStore,
  useTasksStore,
  useUpdatesStore
};

/** Subscribe to Rust events (06 §5.3) and load initial state once before mounting. */
export function startLocalState() {
  return useStateSubscriptions().start('local', async register => {
    const env = useEnvStore();
    const catalog = useCatalogStore();
    const library = useLibraryStore();
    const localApps = useLocalAppsStore();
    const uninstall = useUninstallStore();
    const cleanup = useCleanupStore();
    const monitor = useMonitorStore();
    const security = useSecurityStore();
    const firewall = useFirewallStore();
    const updates = useUpdatesStore();
    const mas = useMasStore();
    const links = useLinksStore();
    const downloads = useDownloadsStore();
    const tasks = useTasksStore();
    const settings = useSettingsStore();
    const names = useNamesStore();
    const prompt = usePermissionPromptStore();
    const permissions = usePermissionsStore();

    await Promise.all([
      register(
        events.taskUpdated.listen(event => {
          tasks.applyUpdate(event.payload);
          prompt.observe(event.payload);
        })
      ),
      register(
        events.permissionsChanged.listen(event => {
          permissions.apply(event.payload);
          void localApps.load().catch(() => undefined);
        })
      ),
      register(events.taskLog.listen(event => tasks.appendLog(event.payload))),
      register(events.libraryChanged.listen(() => void library.load().catch(() => undefined))),
      register(events.localAppsChanged.listen(event => localApps.changed(event.payload.reason))),
      register(
        events.uninstallScanProgress.listen(event => {
          uninstall.applyProgress(event.payload);
        })
      ),
      register(events.cleanupScanProgress.listen(event => cleanup.applyProgress(event.payload))),
      register(events.cleanupChanged.listen(event => cleanup.applySummary(event.payload))),
      register(events.monitorSample.listen(event => monitor.applySample(event.payload))),
      register(events.fanChanged.listen(event => monitor.applyFanChange(event.payload))),
      register(events.securityChanged.listen(event => security.changed(event.payload))),
      register(events.avProgress.listen(event => security.applyProgress(event.payload))),
      register(events.avFinished.listen(event => security.applyFinished(event.payload))),
      register(events.startupAdded.listen(event => security.applyAdded(event.payload.items))),
      register(events.avSchedule.listen(() => void security.loadSchedule().catch(() => undefined))),
      register(events.appSignaturesChanged.listen(event => security.applySignatureChanges(event.payload.apps))),
      register(events.installScanned.listen(event => security.applyInstallScan(event.payload))),
      register(
        events.pasteBlocked.listen(event => {
          security.paste = event.payload;
        })
      ),
      register(events.firewallStatus.listen(event => firewall.applyStatus(event.payload))),
      register(events.firewallAlerts.listen(event => firewall.applyAlerts(event.payload.alerts))),
      register(events.firewallChanged.listen(event => firewall.applyChanged(event.payload.what))),
      register(events.firewallActivity.listen(event => firewall.applyActivity(event.payload.events))),
      register(events.downloadDetected.listen(event => downloads.detected(event.payload))),
      register(events.downloadVerified.listen(event => downloads.verified(event.payload))),
      register(events.updatesChanged.listen(() => void updates.load().catch(() => undefined))),
      register(
        events.appstoreChanged.listen(event => {
          mas.changed(event.payload.reason);
          if (event.payload.reason !== 'region') void updates.load().catch(() => undefined);
        })
      ),
      register(
        events.catalogSynced.listen(event => {
          catalog.status = event.payload;
          catalog.revision += 1;
          void links.refresh();
          void names.refresh().catch(() => undefined);
          void catalog.loadCategories().catch(() => undefined);
        })
      ),
      register(
        events.envChanged.listen(event => {
          env.info = event.payload;
        })
      ),
      register(
        events.localeChanged.listen(() => {
          void settings.load().catch(() => undefined);
          void catalog.loadCategories().catch(() => undefined);
        })
      )
    ]);

    // Returning from System Settings: stop guidance waiting and read current permissions (06 §12.7).
    const onFocus = () => {
      void permissions.returned();
      void localApps.load().catch(() => undefined);
      // Homebrew or the command line tools may have just been installed in Terminal. Once Homebrew is found there is
      // nothing to wait for, so only a missing one is looked for again.
      if (env.info && !env.hasBrew) void env.detect().catch(() => undefined);
    };
    window.addEventListener('focus', onFocus);
    await register(() => window.removeEventListener('focus', onFocus));

    // Changing automatic checks or their schedule changes the next check time.
    await register(
      watch(
        () => [settings.value?.autoCheck, settings.value?.checkTime],
        (next, previous) => {
          if (previous.some(item => item !== undefined)) void updates.loadStatus().catch(() => undefined);
        }
      )
    );

    // Settings determine the startup route. Other regions load independently without blocking the first frame.
    await settings.load().catch(() => undefined);
    void Promise.allSettled([
      env.detect(),
      catalog.loadStatus(),
      catalog.loadCategories(),
      library.load(),
      localApps.load(),
      updates.load(),
      tasks.load(),
      permissions.refresh(),
      cleanup.loadSummary(),
      cleanup.loadRules(),
      security.loadOverview(),
      // The sidebar counts connections waiting for an answer.
      firewall.loadStatus().then(() => (firewall.installed ? firewall.loadAlerts() : undefined))
    ]);
  });
}

/** Permission overlay (06 §12.7): the guide capability permits only permission/language reads and permission-change events during guidance. */
export function startGuideState() {
  return useStateSubscriptions().start('guide', async register => {
    const permissions = usePermissionsStore();
    await register(events.permissionsChanged.listen(event => permissions.apply(event.payload)));
    await permissions.refresh().catch(() => undefined);
  });
}

/**
 * Menu-bar popover (06 §4.2): tray capability permits only installed, update, and task-queue commands.
 * Read only those three; catalog sync rereads previously queried names/icons without calling catalog status/category commands.
 */
export function startTrayState() {
  return useStateSubscriptions().start('tray', async register => {
    const library = useLibraryStore();
    const updates = useUpdatesStore();
    const tasks = useTasksStore();
    const names = useNamesStore();

    await Promise.all([
      register(events.taskUpdated.listen(event => tasks.applyUpdate(event.payload))),
      register(events.libraryChanged.listen(() => void library.load().catch(() => undefined))),
      register(events.updatesChanged.listen(() => void updates.load().catch(() => undefined))),
      register(events.catalogSynced.listen(() => void names.refresh().catch(() => undefined)))
    ]);
    await Promise.allSettled([library.load(), updates.load(), tasks.load()]);
  });
}

/**
 * Monitoring popover (monitor proposal §12): its capability allows only monitoring and fan commands,
 * so it reads nothing else; the page subscribes for the module Rust asks it to show.
 */
export function startMonitorPopoverState() {
  return useStateSubscriptions().start('monitor-popover', async register => {
    const monitor = useMonitorStore();
    await Promise.all([
      register(events.monitorSample.listen(event => monitor.applySample(event.payload))),
      register(events.fanChanged.listen(event => monitor.applyFanChange(event.payload)))
    ]);
  });
}

/**
 * Firewall question window (firewall plan §4.4): its capability allows only reading and answering waiting connections,
 * so it subscribes to the list Rust sends after every change and reads it once.
 */
export function startFirewallAlertState() {
  return useStateSubscriptions().start('firewall-alert', async register => {
    const firewall = useFirewallStore();
    await register(events.firewallAlerts.listen(event => firewall.applyAlerts(event.payload.alerts)));
    await firewall.loadAlerts().catch(() => undefined);
  });
}

/** Release subscriptions before recreating a window application. */
export function stopStateSubscriptions() {
  useStateSubscriptions().stopAll();
}
