import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Kind, OutdatedItem, UpdateSource } from '@/ipc/bindings';
import { shown } from '@/stores/updates';
import { setup } from './helpers';
import cases from '../../../packages/shared/tests/fixtures/updateVisibility.json';

// The page's half of a rule Rust also implements, in `scheduler::visible()`. The two are written separately and
// agree today; what neither side could do on its own was notice a change that moved only the other. Both now read
// `packages/shared/tests/fixtures/updateVisibility.json` — one row per source and kind — so a divergence turns a
// test red on both sides instead of leaving each half green against its own hand-written cases.
//
// This is not a fix: nothing here changes behaviour. `shown` gained an `export` so the table can call it directly;
// `updates-shown.test.ts` still covers the store's public read path.

const KINDS: readonly Kind[] = ['cask', 'formula', 'link'];
const SOURCES: readonly UpdateSource[] = ['homebrew', 'mas', 'sparkle', 'electron', 'github', 'vendor', 'msupdate'];

/** One row of `updates_list`. Only source and kind matter to the rule; the rest is a well-formed row. */
function row(source: UpdateSource, kind: Kind): OutdatedItem {
  const token = `${source}-${kind}`;
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

beforeEach(async () => {
  await setup('/updates');
});
afterEach(() => vi.restoreAllMocks());

describe('The page and Rust read one shared visibility table', () => {
  // A loop over an empty or short table says nothing, and says it quietly: `for (const c of cases)` passes when
  // `cases` is empty, the same trap as `[].every()`. Count and coverage come first so a dropped row cannot hide
  // behind the loop below.
  it('Carries exactly one row per source and kind', () => {
    expect(cases.length).toBe(21);
    const keys = cases.map(c => `${c.source}/${c.kind}`);
    expect(new Set(keys).size).toBe(21);
    for (const source of SOURCES) for (const kind of KINDS) expect(keys).toContain(`${source}/${kind}`);
  });

  it('Names every source and kind the bindings define, and nothing else', () => {
    // The two lists above are hand-kept, and a union type has no runtime length to compare against. The
    // exhaustive assignments below fail to compile if `Kind` or `UpdateSource` grows a variant these lists lack,
    // and the set comparisons catch a list that names something the table does not carry.
    const everyKind: Record<Kind, true> = { cask: true, formula: true, link: true };
    const everySource: Record<UpdateSource, true> = {
      homebrew: true,
      mas: true,
      sparkle: true,
      electron: true,
      github: true,
      vendor: true,
      msupdate: true
    };
    expect(new Set(cases.map(c => c.kind))).toEqual(new Set(Object.keys(everyKind)));
    expect(new Set(cases.map(c => c.source))).toEqual(new Set(Object.keys(everySource)));
    expect(KINDS.length * SOURCES.length).toBe(cases.length);
  });

  it.each(cases)('Keeps $source/$kind: $expectedVisible', ({ source, kind, expectedVisible }) => {
    expect(shown([row(source as UpdateSource, kind as Kind)])).toHaveLength(expectedVisible ? 1 : 0);
  });

  it('Applies the whole table at once, keeping the rows it keeps in order', () => {
    // A rule that only answered one row correctly would still pass the per-row cases above.
    const rows = cases.map(c => row(c.source as UpdateSource, c.kind as Kind));
    const expected = cases.filter(c => c.expectedVisible).map(c => `${c.source}-${c.kind}`);
    expect(shown(rows).map(item => item.token)).toEqual(expected);
  });
});
