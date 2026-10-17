import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import type { GetState } from '@opennavo/ui';
import type { Kind } from '@/ipc/bindings';
import { IpcError, commands, unwrap } from '@/ipc/client';
import { useEnvStore, useLibraryStore, useTasksStore, useUpdatesStore } from '@/stores';
import { useLinksStore } from '@/stores/links';
import { useToasts } from './useToasts';
import { updateRequest } from '@/stores/updates';

export interface PackageState {
  state: GetState;
  /**
   * 0–100 while running. The ring never turns into a spinner (like the toolbar ring): unknown progress holds the last
   * known value, or reads as 0 before there is one.
   */
  progress?: number;
  /** Override the default button label (Install Homebrew first when unavailable, 08 §10.15). */
  label?: string;
}

/** 06 §13: derive Get button state and click actions (install, update, open). */
export function usePackageState() {
  const env = useEnvStore();
  const library = useLibraryStore();
  const updates = useUpdatesStore();
  const tasks = useTasksStore();
  const links = useLinksStore();
  const toasts = useToasts();
  const router = useRouter();
  const { t, te } = useI18n();

  /** The package's task, including an update an app delivers itself (plan §11.4), which runs as a local-app task. */
  function taskOf(kind: Kind, token: string) {
    const row = updates.find(kind, token);
    const routed = row && row.source !== 'homebrew' ? updateRequest(row).target : undefined;
    return tasks.forPackage(kind, token) ?? (routed ? tasks.forPackage(routed.kind, routed.token) : undefined);
  }

  function stateOf(kind: Kind, token: string, disabled = false): PackageState {
    // Link apps (store/security plan §4) never install through OpenNavo: Download opens their site; once the app is on
    // this Mac (matched by bundle ID) the button opens it.
    if (kind === 'link') return { state: links.installed(token) ? 'open' : 'download' };
    // Without Homebrew, show Install Homebrew first and navigate to Welcome to install it (08 §10.15).
    if (env.info && !env.hasBrew) return { state: 'get', label: t('package.getNeedsBrew') };
    const row = updates.find(kind, token);
    const task = taskOf(kind, token);
    if (task?.state === 'running') return { state: 'running', progress: tasks.shownPercent(task) };
    if (task?.state === 'queued') return { state: 'queued' };
    const installed = library.find(kind, token);
    if (!installed) return { state: disabled ? 'unavailable' : 'get' };
    if (row && !row.pinned && !row.ignored && !row.informationalUrl && !row.relaunchPending) return { state: 'update' };
    return { state: kind === 'cask' ? 'open' : 'installed' };
  }

  function errorText(error: unknown): string {
    if (error instanceof IpcError) {
      const key = `errors.${error.code}`;
      return te(key) ? t(key) : error.message;
    }
    return error instanceof Error ? error.message : String(error);
  }

  async function act(kind: Kind, token: string, state: GetState) {
    try {
      if (kind === 'link') {
        const local = state === 'open' ? links.installed(token) : undefined;
        if (local) await unwrap(commands.localAppOpen(local.id));
        else await links.open(token);
        return;
      }
      if (state === 'get') {
        if (env.info && !env.hasBrew) {
          await router.push({ name: 'welcome' });
          return;
        }
        await tasks.enqueue('install', { kind, token });
      } else if (state === 'update') {
        // The row knows where the update comes from: Homebrew, or the app's own feed (store/security plan §11.4).
        const row = updates.find(kind, token);
        if (row) await updates.upgrade([row]);
        // Explicit casks always use --greedy (06 §6.3); Rust constructs the arguments.
        else await tasks.enqueue('upgrade', { kind, token }, 'manual', kind === 'cask' ? { greedy: true } : {});
      } else if (state === 'open') {
        await library.ensureInstalledRecord(kind, token);
        await unwrap(commands.appOpen({ kind, token }));
      }
    } catch (error) {
      toasts.push({
        tone: 'danger',
        title: t('errors.actionFailed'),
        description: errorText(error)
      });
    }
  }

  return { stateOf, taskOf, act, errorText };
}
