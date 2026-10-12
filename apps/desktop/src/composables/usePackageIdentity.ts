import { i18n } from '@/i18n';
import type { LocalApp, Task, TaskKind } from '@/ipc/bindings';
import { localAppIcon, useLocalAppsStore } from '@/stores/localApps';
import { useNamesStore } from '@/stores/names';
import { useAppLocale } from './useAppLocale';

/**
 * kind/token → localized display name and icon (task cards, history, menu bar).
 * `app` targets are local apps outside Homebrew: name and icon come from local_apps_list; once uninstalled
 * (or in the tray window, which cannot read local apps) the task's recorded targetName is the fallback.
 */
export function usePackageIdentity() {
  const names = useNamesStore();
  const localApps = useLocalAppsStore();
  const { pick } = useAppLocale();

  /** Local app: use its normalized native icon and the catalog's localized name. */
  function localApp(app: LocalApp) {
    const token = app.cask ?? app.catalogToken;
    const entry = token ? names.lookup('cask', token) : undefined;
    const name = entry ? pick(entry.displayName, entry.name, entry.sourceLocale) : app.name;
    return {
      name,
      // Letter tiles are keyed by token; the bundle ID keeps the tile color stable for apps outside the catalog.
      icon: {
        kind: 'cask' as const,
        token: token ?? app.bundleId ?? app.id,
        name,
        src: localAppIcon(app) ?? entry?.iconUrl ?? null,
        accent: entry?.accentColor ?? null
      }
    };
  }

  function displayName(kind: TaskKind, token: string, fallback?: string | null): string {
    if (kind === 'app') {
      const app = localApps.find(token);
      return app ? localApp(app).name : (fallback ?? token);
    }
    const entry = names.lookup(kind, token);
    return entry ? pick(entry.displayName, entry.name, entry.sourceLocale) : (fallback ?? token);
  }

  function icon(kind: TaskKind, token: string, fallback?: string | null) {
    if (kind === 'app') {
      const app = localApps.find(token);
      if (app) return localApp(app).icon;
      const name = fallback ?? token;
      return { kind: 'cask' as const, token, name, src: null, accent: null };
    }
    const entry = names.lookup(kind, token);
    return {
      kind,
      token,
      name: displayName(kind, token, fallback),
      src: entry?.iconUrl ?? null,
      accent: entry?.accentColor ?? null
    };
  }

  /** Task target name, falling back to the name recorded when the task was queued. Clean Up runs have no app target. */
  const taskName = (task: Task) =>
    task.op === 'clean'
      ? i18n.global.t('cleanup.title')
      : task.target
        ? displayName(task.target.kind, task.target.token, task.targetName)
        : (task.targetName ?? '');

  const taskIcon = (task: Task) =>
    task.op !== 'clean' && task.target ? icon(task.target.kind, task.target.token, task.targetName) : undefined;

  return { displayName, icon, localApp, taskName, taskIcon };
}
