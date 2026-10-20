import { computed } from 'vue';
import type { AppSignatureEntry, AppSignatureIssue, LocalApp } from '@/ipc/bindings';
import { useAppLocale } from '@/composables/useAppLocale';
import { usePackageIdentity } from '@/composables/usePackageIdentity';
import { useLocalAppsStore } from '@/stores/localApps';
import { useSecurityStore } from '@/stores/security';

/** Most serious first: another developer or a broken signature, then changed contents, then what is merely missing. */
const RANK: Record<AppSignatureIssue, number> = {
  team_changed: 0,
  invalid: 1,
  modified: 2,
  unreadable: 3,
  unsigned: 4,
  adhoc: 5,
  not_notarized: 6
};
/** Another developer signing an app, or a broken signature, is worth more attention than a missing notarization. */
const SERIOUS: readonly AppSignatureIssue[] = ['team_changed', 'invalid'];

export const sortIssues = (issues: readonly AppSignatureIssue[]) => [...issues].sort((a, b) => RANK[a] - RANK[b]);
export const isSerious = (issues: readonly AppSignatureIssue[]) => issues.some(issue => SERIOUS.includes(issue));

/** One app of the last signature check that has something to report, with the local app it is, if it is still there. */
export interface SignatureRow {
  entry: AppSignatureEntry;
  app: LocalApp | null;
  name: string;
  icon: ReturnType<ReturnType<typeof usePackageIdentity>['icon']>;
  /** Most serious first. */
  issues: AppSignatureIssue[];
  serious: boolean;
}

/** The apps with signature problems, most serious first, named and drawn the way Installed shows them. */
export function useSignatureProblems() {
  const security = useSecurityStore();
  const localApps = useLocalAppsStore();
  const { localApp, icon } = usePackageIdentity();
  const { appLocale } = useAppLocale();

  const report = computed(() => security.signatures);
  const rows = computed<SignatureRow[]>(() => {
    const byPath = new Map(localApps.items.map(app => [app.path, app]));
    const collator = new Intl.Collator(appLocale.value);
    return (report.value?.apps ?? [])
      .filter(entry => entry.issues.length)
      .map(entry => {
        const app = byPath.get(entry.path) ?? null;
        const identity = app ? localApp(app) : null;
        const issues = sortIssues(entry.issues);
        return {
          entry,
          app,
          name: identity?.name ?? entry.name,
          icon: identity?.icon ?? icon('app', entry.path, entry.name),
          issues,
          serious: isSerious(issues)
        };
      })
      .sort((a, b) => RANK[a.issues[0]!] - RANK[b.issues[0]!] || collator.compare(a.name, b.name));
  });
  return { report, rows };
}
