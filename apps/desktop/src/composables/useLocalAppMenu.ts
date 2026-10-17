import { useI18n } from 'vue-i18n';
import type { OnMenuItem } from '@opennavo/ui';
import type { LocalApp } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { useTasksStore } from '@/stores';
import { useUninstallStore } from '@/stores/uninstall';
import { usePackageState } from './usePackageState';
import { useToasts } from './useToasts';

/**
 * More menu of an Installed row: the same open / reveal / uninstall actions for every source.
 * `uninstall` is handled by the page, which opens the app info sheet before confirmation.
 */
export function useLocalAppMenu() {
  const { t } = useI18n();
  const tasks = useTasksStore();
  const uninstaller = useUninstallStore();
  const toasts = useToasts();
  const { errorText } = usePackageState();

  function itemsFor(app: LocalApp): OnMenuItem[] {
    const items: OnMenuItem[] = [
      { key: 'open', label: t('package.open'), icon: 'arrow-up-right' },
      { key: 'reveal', label: t('package.actions.reveal'), icon: 'folder' }
    ];
    items.push({
      key: 'uninstall',
      label: t('package.actions.uninstall'),
      icon: 'trash',
      danger: true,
      separator: true,
      disabled: !app.removable
    });
    return items;
  }

  async function run(operation: Promise<unknown>) {
    try {
      await operation;
    } catch (error) {
      toasts.push({
        tone: 'danger',
        title: t('errors.actionFailed'),
        description: errorText(error)
      });
    }
  }

  /** Adopt: install the cask over the existing app; the install flow asks for adoption consent first. Never an App Store copy. */
  const adopt = (app: LocalApp) =>
    // Rust names the cask that matches this copy (LocalApp.homebrewCask, plan §11.4); App Store copies stay there.
    app.homebrewCask && app.source !== 'app_store'
      ? run(tasks.enqueue('install', { kind: 'cask', token: app.homebrewCask }))
      : Promise.resolve();

  async function select(app: LocalApp, _name: string, key: string) {
    switch (key) {
      case 'open':
        return run(unwrap(commands.localAppOpen(app.id)));
      case 'reveal':
        return run(unwrap(commands.localAppReveal(app.id)));
      case 'adopt':
        return adopt(app);
      case 'uninstall':
        if (app.removable) await run(uninstaller.start([app.id]));
        return;
    }
  }

  return { itemsFor, select, adopt };
}
