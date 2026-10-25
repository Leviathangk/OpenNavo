import { flushPromises } from '@vue/test-utils';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { OutdatedItem, UpdatesStatus } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';
import { useUpdatesStore } from '@/stores/updates';
import { setup } from './helpers';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}
const ok = <T>(data: T) => ({ status: 'ok' as const, data });
const status = (checking: boolean, checkedAt: number): UpdatesStatus => ({
  checking,
  checkedAt,
  nextCheckAt: null,
  failedSources: [],
  failures: []
});
let rows: OutdatedItem[];
beforeEach(async () => {
  await setup('/updates');
  rows = await unwrap(commands.updatesList());
  expect(rows.length).toBeGreaterThan(0);
});
afterEach(() => vi.restoreAllMocks());

it('does not restore rows when an older list response arrives last', async () => {
  const store = useUpdatesStore();
  const old = deferred<ReturnType<typeof ok<OutdatedItem[]>>>();
  vi.spyOn(commands, 'updatesList')
    .mockImplementationOnce(() => old.promise)
    .mockResolvedValueOnce(ok([]));
  const first = store.load();
  await flushPromises();
  await store.load();
  old.resolve(ok(rows));
  await first;
  expect(store.items).toEqual([]);
  expect(store.loaded).toBe(true);
});

it('does not restore checking or failure state from an older status response', async () => {
  const store = useUpdatesStore();
  const old = deferred<ReturnType<typeof ok<UpdatesStatus>>>();
  const current = status(false, 200);
  vi.spyOn(commands, 'updatesStatus')
    .mockImplementationOnce(() => old.promise)
    .mockResolvedValueOnce(ok(current));
  const first = store.loadStatus();
  await store.loadStatus();
  old.resolve(ok({ ...status(true, 100), failedSources: ['sparkle'] }));
  await first;
  expect(store.status).toEqual(current);
  expect(store.checking).toBe(false);
});

it('abandons an old load still waiting for status before requesting its list', async () => {
  const store = useUpdatesStore();
  const old = deferred<ReturnType<typeof ok<UpdatesStatus>>>();
  vi.spyOn(commands, 'updatesStatus')
    .mockImplementationOnce(() => old.promise)
    .mockResolvedValueOnce(ok(status(false, 200)));
  const lists = vi.spyOn(commands, 'updatesList').mockResolvedValue(ok([]));
  const first = store.load();
  await store.load();
  old.resolve(ok(status(true, 100)));
  await first;
  expect(lists).toHaveBeenCalledTimes(1);
  expect(store.items).toEqual([]);
  expect(store.checking).toBe(false);
});

it.each(['load', 'check'] as const)('discards an older %s when the other operation finishes first', async operation => {
  const store = useUpdatesStore();
  const old = deferred<ReturnType<typeof ok<OutdatedItem[]>>>();
  vi.spyOn(commands, 'updatesList').mockImplementation(() =>
    operation === 'load' ? old.promise : Promise.resolve(ok([]))
  );
  vi.spyOn(commands, 'updatesCheck').mockImplementation(() =>
    operation === 'check' ? old.promise : Promise.resolve(ok([]))
  );
  const first = store[operation]();
  await flushPromises();
  await store[operation === 'load' ? 'check' : 'load']();
  old.resolve(ok(rows));
  await first;
  expect(store.items).toEqual([]);
});

it('keeps checking active until all overlapping manual checks finish', async () => {
  const store = useUpdatesStore();
  const first = deferred<ReturnType<typeof ok<OutdatedItem[]>>>();
  const second = deferred<ReturnType<typeof ok<OutdatedItem[]>>>();
  vi.spyOn(commands, 'updatesCheck')
    .mockImplementationOnce(() => first.promise)
    .mockImplementationOnce(() => second.promise);
  vi.spyOn(commands, 'updatesStatus').mockResolvedValue(ok(status(false, 200)));
  const a = store.check();
  const b = store.check();
  second.resolve(ok([]));
  await b;
  expect(store.checking).toBe(true);
  first.resolve(ok(rows));
  await a;
  expect(store.checking).toBe(false);
  expect(store.items).toEqual([]);
});
