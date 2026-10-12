import { useI18n } from 'vue-i18n';
import type { OnMenuItem } from '@opennavo/ui';
import { installCommand } from '@opennavo/shared';
import type { InstalledItem, Kind } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { useBrewfileStore } from '@/stores/brewfile';
import { useLibraryStore, useTasksStore } from '@/stores';
import { useLocalAppsStore } from '@/stores/localApps';
import { useUninstallStore } from '@/stores/uninstall';
import { openExternal } from '@/utils/external';
import { fetchClientConfig } from './useClientConfig';
import { usePackageState } from './usePackageState';
import { useToasts } from './useToasts';

/**
 * Package More menu (detail header and installed rows): reinstall, pin, reveal in Finder, copy command, website, uninstall.
 * Uninstall opens the shared uninstall dialog (scan → review → run), the same flow as the Installed page.
 */
export function usePackageMenu() {
  const { t } = useI18n();
  const brewfile = useBrewfileStore();
  const library = useLibraryStore();
  const tasks = useTasksStore();
  const localApps = useLocalAppsStore();
  const uninstaller = useUninstallStore();
  const toasts = useToasts();
  const { act, errorText } = usePackageState();

  function itemsFor(kind: Kind, token: string, options: { open?: boolean; update?: boolean } = {}): OnMenuItem[] {
    const installed: InstalledItem | undefined = library.find(kind, token);
    const items: OnMenuItem[] = [];
    if (kind === 'cask')
      items.push({
        key: 'list',
        label: t(brewfile.has(kind, token) ? 'brewfile.removeShort' : 'brewfile.add'),
        icon: 'file-text'
      });
    if (installed) {
      if (options.open && kind === 'cask')
        items.push({ key: 'open', label: t('package.open'), icon: 'arrow-up-right' });
      if (options.update) items.push({ key: 'update', label: t('updates.update'), icon: 'download' });
      items.push({ key: 'reinstall', label: t('package.actions.reinstall'), icon: 'refresh-cw' });
      items.push(
        installed.pinned
          ? { key: 'unpin', label: t('package.actions.unpin'), icon: 'pin' }
          : { key: 'pin', label: t('package.actions.pin'), icon: 'pin' }
      );
      if (kind === 'cask' && installed.appPaths.length)
        items.push({ key: 'reveal', label: t('package.actions.reveal'), icon: 'folder' });
    }
    items.push({
      key: 'copy',
      label: t('package.actions.copyCommand'),
      icon: 'copy',
      separator: Boolean(installed)
    });
    items.push({ key: 'web', label: t('package.actions.web'), icon: 'arrow-up-right' });
    if (installed)
      items.push({
        key: 'uninstall',
        label: t('package.actions.uninstall'),
        icon: 'trash',
        danger: true,
        separator: true
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

  async function select(kind: Kind, token: string, name: string, key: string, command?: string) {
    const target = { kind, token };
    switch (key) {
      case 'list':
        if (brewfile.has(kind, token)) brewfile.remove(kind, token);
        else brewfile.add(kind, token);
        break;
      case 'open':
        await act(kind, token, 'open');
        break;
      case 'update':
        await act(kind, token, 'update');
        break;
      case 'reinstall':
      case 'pin':
      case 'unpin':
        await run(tasks.enqueue(key, target));
        break;
      case 'reveal':
        await run(unwrap(commands.appReveal(target)));
        break;
      case 'copy': {
        const text = command ?? installCommand(kind, token);
        await navigator.clipboard.writeText(text);
        toasts.push({ tone: 'success', title: t('package.commandCopied'), description: text });
        break;
      }
      case 'web': {
        const config = await fetchClientConfig().catch(() => null);
        if (config) await run(openExternal(`${config.links.web}/${kind === 'cask' ? 'apps' : 'cli'}/${token}`));
        break;
      }
      case 'uninstall':
        await uninstall(token);
        break;
    }
  }

  /** Homebrew casks are local apps too; find the entry (reading the list once if needed) and start the dialog. */
  async function uninstall(token: string) {
    if (!localApps.findCask(token)) await run(localApps.load());
    const app = localApps.findCask(token);
    if (!app) {
      toasts.push({
        tone: 'danger',
        title: t('errors.actionFailed'),
        description: t('errors.E_NOT_FOUND')
      });
      return;
    }
    await run(uninstaller.start([app.id]));
  }

  return { itemsFor, select };
}
