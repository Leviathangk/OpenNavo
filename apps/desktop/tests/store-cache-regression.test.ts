import { flushPromises } from '@vue/test-utils';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { computed } from 'vue';
import { commands, events, unwrap } from '@/ipc/client';
import type { LocalItem } from '@/ipc/client';
import type { MasApp } from '@/ipc/bindings';
import { startLocalState, stopStateSubscriptions } from '@/stores';
import { useMasStore } from '@/stores/mas';
import { useLinksStore } from '@/stores/links';
import { useLocalAppsStore } from '@/stores/localApps';
import { setup } from './helpers';

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => {
    resolve = done;
  });
  return { promise, resolve };
}
const ok = <T>(data: T) => ({ status: 'ok' as const, data });
afterEach(() => {
  stopStateSubscriptions();
  vi.restoreAllMocks();
});

describe('Storefront cache revisions', () => {
  it.each(['get', 'search', 'bundles', 'bundle-miss'] as const)(
    '%s cannot cache an old storefront response after reset',
    async entry => {
      await setup('/discover');
      const store = useMasStore();
      const [fixture] = await unwrap(commands.masLookup([1666327168]));
      const oldApp: MasApp = { ...fixture!, currency: 'CNY', formattedPrice: '¥68', price: 68 };
      const newApp: MasApp = { ...oldApp, currency: 'USD', formattedPrice: '$9', price: 9 };
      await store.loadStatus();
      const old = deferred<ReturnType<typeof ok<MasApp[]>>>();
      const oldBundles = deferred<ReturnType<typeof ok<Record<string, MasApp>>>>();
      const lookup = vi.spyOn(commands, 'masLookup').mockResolvedValue(ok([newApp]));
      const search = vi.spyOn(commands, 'masSearch').mockResolvedValue(ok([newApp]));
      const bundles = vi.spyOn(commands, 'masLookupBundles').mockResolvedValue(ok({ [newApp.bundleId]: newApp }));
      if (entry === 'get') lookup.mockReturnValueOnce(old.promise);
      else if (entry === 'search') search.mockReturnValueOnce(old.promise);
      else bundles.mockReturnValueOnce(oldBundles.promise);
      const pending =
        entry === 'get'
          ? store.get(oldApp.adamId)
          : entry === 'search'
            ? store.search('fixture')
            : store.byBundles([oldApp.bundleId]);
      await flushPromises();
      store.reset();
      const status = await unwrap(commands.masStatus());
      vi.spyOn(commands, 'masStatus').mockResolvedValue(ok({ ...status, country: 'us' }));
      await store.loadStatus();
      old.resolve(ok([oldApp]));
      oldBundles.resolve(ok(entry === 'bundle-miss' ? {} : { [oldApp.bundleId]: oldApp }));
      await pending;
      expect(store.peek(oldApp.adamId)?.currency).toBe('USD');
      expect((await store.get(oldApp.adamId))?.currency).toBe('USD');
      expect((await store.byBundles([oldApp.bundleId])).get(oldApp.bundleId)?.currency).toBe('USD');
      if (entry === 'search') {
        expect((await store.search('fixture'))[0]?.currency).toBe('USD');
        expect(search).toHaveBeenCalledTimes(2);
      }
    }
  );
});

describe('Link catalog revisions', () => {
  it.each(['miss', 'error'] as const)(
    'retries a %s explicitly without computed retries or duplicate concurrent reads',
    async failure => {
      await setup('/discover');
      const fixture = (await unwrap(commands.catalogGet('link', 'lumen-notes'))) as LocalItem;
      const get = vi.spyOn(commands, 'catalogGet');
      if (failure === 'miss') get.mockResolvedValueOnce(ok(null));
      else get.mockRejectedValueOnce(new Error('unavailable'));
      const store = useLinksStore();
      const shown = computed(() => store.installed(fixture.token));
      expect(shown.value).toBeUndefined();
      await flushPromises();
      for (let i = 0; i < 10; i++) {
        store.get(fixture.token);
        expect(shown.value).toBeUndefined();
      }
      expect(get).toHaveBeenCalledOnce();
      const next = deferred<ReturnType<typeof ok<LocalItem>>>();
      get.mockReturnValueOnce(next.promise);
      const a = store.load(fixture.token),
        b = store.load(fixture.token);
      expect(get).toHaveBeenCalledTimes(2);
      next.resolve(ok(fixture));
      expect(await a).toEqual(fixture);
      expect(await b).toEqual(fixture);
    }
  );

  it('catalog:synced refreshes successes, misses and errors and recognizes installed links', async () => {
    await setup('/discover');
    await startLocalState();
    await flushPromises();
    const fixture = (await unwrap(commands.catalogGet('link', 'adobe-photoshop'))) as LocalItem;
    expect(fixture).not.toBeNull();
    const store = useLinksStore();
    const installedApp = useLocalAppsStore().items.find(
      app => app.bundleId && fixture.bundleIds?.includes(app.bundleId)
    );
    expect(installedApp).toBeDefined();
    const get = vi.spyOn(commands, 'catalogGet');
    get.mockResolvedValueOnce(ok(null));
    await store.load('miss');
    get.mockRejectedValueOnce(new Error('offline'));
    await expect(store.load('error')).rejects.toThrow('offline');
    store.remember([{ ...fixture, token: 'success', bundleIds: [] }]);
    get.mockImplementation(async (_, token) => ok({ ...fixture, token }));
    const shown = computed(() => store.installed('miss'));
    expect(shown.value).toBeUndefined();
    await events.catalogSynced.emit(await unwrap(commands.catalogStatus()));
    await flushPromises();
    for (const token of ['miss', 'error', 'success']) expect(store.installed(token)?.id).toBe(installedApp!.id);
    expect(shown.value?.id).toBe(installedApp!.id);
    expect(get).toHaveBeenCalledTimes(5);
  });

  it('an earlier in-flight catalog read cannot overwrite the refreshed record', async () => {
    await setup('/discover');
    const fixture = (await unwrap(commands.catalogGet('link', 'lumen-notes'))) as LocalItem;
    const old = deferred<ReturnType<typeof ok<LocalItem>>>();
    const get = vi
      .spyOn(commands, 'catalogGet')
      .mockReturnValueOnce(old.promise)
      .mockResolvedValue(ok({ ...fixture, bundleIds: ['new.bundle'] }));
    const store = useLinksStore();
    const first = store.load(fixture.token);
    await store.refresh();
    old.resolve(ok({ ...fixture, bundleIds: ['old.bundle'] }));
    await first;
    expect(store.get(fixture.token)?.bundleIds).toEqual(['new.bundle']);
    expect(get).toHaveBeenCalledTimes(2);
  });
});
