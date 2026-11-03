import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Kind, OutdatedItem, UpdateSource, UpdatesStatus } from '@/ipc/bindings';
import { commands } from '@/ipc/client';
import { useUpdatesStore } from '@/stores/updates';
import { setup } from './helpers';

// Cask-only catalog (ADR-018): the page lists a Homebrew row only when it is a cask. Command-line tools from
// `brew outdated` (formula, link) stay out of the list and out of its count.
//
// This is the page's half of a rule Rust implements verbatim in `scheduler::visible()`; that side is pinned by
// `visible_is_the_pages_rule_not_the_schedulers`. The page's side was already held by `state.test.ts`, whose
// `Cask-only local installed/update lists exclude Homebrew command-line tools` asserts it through
// `useUpdatesStore().items`; what this file adds is a per-branch truth table over `shown()`, so a failure names
// the branch that broke instead of only the list.
//
// `shown()` is now exported, but these cases keep going through the public path: a mocked `updates_list`, then
// `load()`, then `items`. With no finished task and no pin in play, `accept()` applies exactly `shown(list)`, so
// they check the store's wiring as well as the rule. `updates-shared-table.test.ts` drives the exported function
// directly from the same fixture Rust reads.

const status: UpdatesStatus = { checking: false, checkedAt: null, nextCheckAt: null, failedSources: [], failures: [] };
const ok = <T>(data: T) => ({ status: 'ok' as const, data });

/** One row of `updates_list`. Only source and kind matter to the rule; the rest is a well-formed row. */
function row(source: UpdateSource, kind: Kind, token: string): OutdatedItem {
  return {
    source,
    kind,
    token,
    appId: null,
    bundleId: null,
    channel: null,
    major: false,
    notes: null,
    integrity: null,
    requiresAdmin: false,
    running: false,
    relaunchPending: false,
    skippedVersion: null,
    criticalUpdate: false,
    informationalUrl: null,
    downloadHost: null,
    expectedTeamMatch: null,
    name: token,
    installedVersion: '1.0.0',
    currentVersion: '1.1.0',
    autoUpdates: true,
    pinned: false,
    ignored: false,
    dependents: 0,
    downloadSize: null
  };
}

/** Load a list through the store's public read path and return what the page would show. */
async function listed(rows: OutdatedItem[]): Promise<string[]> {
  vi.spyOn(commands, 'updatesStatus').mockResolvedValue(ok(status));
  vi.spyOn(commands, 'updatesList').mockResolvedValue(ok(rows));
  const store = useUpdatesStore();
  await store.load();
  return store.items.map(item => item.token);
}

beforeEach(async () => {
  await setup('/updates');
});
afterEach(() => vi.restoreAllMocks());

describe('The update page lists homebrew casks only (ADR-018)', () => {
  // The truth table. Each case is named for the answer the rule gives, so a failure says which branch broke
  // rather than only that the list changed.
  //
  // Scope: these lock the page's behaviour here, not Rust's `scheduler::visible()`. The two rules agree, but the
  // Rust test `visible_is_the_pages_rule_not_the_schedulers` only covers Homebrew cask/formula/link and one
  // App Store row, so the non-homebrew formula cases below have no counterpart there. Do not read them as a
  // two-way lock: changing Rust alone would not turn anything here red.
  it.each<[UpdateSource, Kind, boolean]>([
    ['homebrew', 'cask', true],
    ['homebrew', 'formula', false],
    ['homebrew', 'link', false],
    ['mas', 'cask', true],
    ['mas', 'formula', true],
    ['sparkle', 'cask', true],
    ['electron', 'link', true],
    ['github', 'formula', true]
  ])('keeps %s/%s: %s', async (source, kind, kept) => {
    expect(await listed([row(source, kind, `${source}-${kind}`)])).toEqual(kept ? [`${source}-${kind}`] : []);
  });

  it('Keeps every non-homebrew row whatever its kind, and drops only the homebrew ones that are not casks', async () => {
    // A whole list at once: a per-row rule that only handled one row correctly would still pass the cases above.
    const rows = [
      row('homebrew', 'cask', 'cask-keep'),
      row('homebrew', 'formula', 'formula-drop'),
      row('homebrew', 'link', 'link-drop'),
      row('mas', 'formula', 'mas-keep'),
      row('sparkle', 'link', 'sparkle-keep')
    ];
    expect(await listed(rows)).toEqual(['cask-keep', 'mas-keep', 'sparkle-keep']);
  });

  it('Leaves the order of the rows it keeps alone', async () => {
    // Filtering must not reorder: Homebrew and App Store rows are already in the order the page shows them.
    const rows = [
      row('sparkle', 'cask', 'first'),
      row('homebrew', 'formula', 'dropped'),
      row('homebrew', 'cask', 'second'),
      row('mas', 'cask', 'third')
    ];
    expect(await listed(rows)).toEqual(['first', 'second', 'third']);
  });
});
