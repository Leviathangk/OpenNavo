import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import type { GetState } from '@opennavo/ui';
import type { Kind } from '@/ipc/bindings';
import { IpcError, commands, unwrap } from '@/ipc/client';
import { useEnvStore, useLibraryStore, useTasksStore, useUpdatesStore } from '@/stores';
import { useToasts } from './useToasts';

export interface PackageState {
  state: GetState;
  /** 0–100 while running; unknown progress reads as 0 so the ring never turns into a spinner (like the toolbar ring). */
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
  const toasts = useToasts();
  const router = useRouter();
  const { t, te } = useI18n();

  function stateOf(kind: Kind, token: string, disabled = false): PackageState {
    // Without Homebrew, show Install Homebrew first and navigate to Welcome to install it (08 §10.15).
    if (env.info && !env.hasBrew) return { state: 'get', label: t('package.getNeedsBrew') };
    const task = tasks.forPackage(kind, token);
    if (task?.state === 'running') return { state: 'running', progress: task.percent ?? 0 };
    if (task?.state === 'queued') return { state: 'queued' };
    const installed = library.find(kind, token);
    if (!installed) return { state: disabled ? 'unavailable' : 'get' };
    const outdated = updates.find(kind, token);
    if (outdated && !outdated.pinned && !outdated.ignored) return { state: 'update' };
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
      if (state === 'get') {
        if (env.info && !env.hasBrew) {
          await router.push({ name: 'welcome' });
          return;
        }
        await tasks.enqueue('install', { kind, token });
      } else if (state === 'update') {
        // Explicit casks always use --greedy (06 §6.3); Rust constructs the arguments.
        await tasks.enqueue('upgrade', { kind, token }, 'manual', kind === 'cask' ? { greedy: true } : {});
      } else if (state === 'open') {
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

  return { stateOf, act, errorText };
}
