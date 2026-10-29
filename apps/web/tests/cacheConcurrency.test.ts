import { describe, expect, it } from 'vitest';
import { createStorage, type Driver } from 'unstorage';
import memoryDriver from 'unstorage/drivers/memory';
import { boundedCache, MAX_CACHE_ENTRY_BYTES } from '../server/utils/boundedCache';

const key = 'nitro:routes:_:recent.fixture.json';
const page = { body: '<article>Recently Updated Mac Apps</article>'.repeat(100) };

// Hold the physical write open without timers, just as async gzip/Redis can outlive the response.
function blockedWrite() {
  const inner = memoryDriver();
  const started = Promise.withResolvers<void>();
  const release = Promise.withResolvers<void>();
  let writes = 0;
  const driver: Driver = {
    ...inner,
    async setItem(name, value, options) {
      if (++writes === 1) {
        started.resolve();
        await release.promise;
      }
      await inner.setItem!(name, value, options);
    },
    getItems: async items =>
      Promise.all(items.map(async ({ key: name }) => ({ key: name, value: await inner.getItem(name) })))
  };
  return { inner, storage: createStorage({ driver: boundedCache(driver) }), started, release };
}

describe('pending page cache writes', () => {
  it('accounts for compression before the backing driver has even received the write', async () => {
    const storage = createStorage({ driver: boundedCache(memoryDriver()) });
    const write = storage.setItem(key, page);
    const read = storage.getItem(key);
    const keys = storage.getKeys('nitro:routes');
    await write;
    expect(await read).toEqual(page);
    expect(await keys).toEqual([key]);
  });

  it('makes a cold write visible to individual reads, MGET, existence checks and purge enumeration', async () => {
    const { storage, started, release } = blockedWrite();
    const write = storage.setItem(key, page);
    await started.promise;
    const read = storage.getItem(key);
    const batch = storage.getItems([key]);
    const exists = storage.hasItem(key);
    const keys = storage.getKeys('nitro:routes');
    release.resolve();
    await write;
    expect(await Promise.all([read, batch, exists, keys])).toEqual([page, [{ key, value: page }], true, [key]]);
  });

  it('does not serve the old value while a replacement is being written', async () => {
    const { inner, storage, started, release } = blockedWrite();
    await inner.setItem!(key, JSON.stringify({ body: 'old page' }), {});
    const write = storage.setItem(key, page);
    await started.promise;
    const read = storage.getItem(key);
    release.resolve();
    await write;
    expect(await read).toEqual(page);
  });

  it.each(['invalidation', 'oversized replacement'])(
    'finishes a pending write before %s, so stale HTML cannot reappear',
    async operation => {
      const { storage, started, release } = blockedWrite();
      const write = storage.setItem(key, page);
      await started.promise;
      const remove =
        operation === 'invalidation'
          ? storage.removeItem(key)
          : storage.setItem(key, 'x'.repeat(MAX_CACHE_ENTRY_BYTES + 1));
      release.resolve();
      await Promise.all([write, remove]);
      expect(await storage.getItem(key)).toBeNull();
    }
  );

  it('preserves same-key write order and lets unrelated keys finish independently', async () => {
    const { storage, started, release } = blockedWrite();
    // An uncompressed entry also has to preserve order with an asynchronous backing driver.
    const name = 'sitemap:input';
    const first = storage.setItem(name, 'old page');
    await started.promise;
    const second = storage.setItem(name, 'new page');
    await storage.setItem('unrelated', 'ready');
    expect(await storage.getItem('unrelated')).toBe('ready');
    release.resolve();
    await Promise.all([first, second]);
    expect(await storage.getItem(name)).toBe('new page');
  });

  it('cleans up rejected writes so reads, purges and later writes can recover', async () => {
    const inner = memoryDriver();
    const failure = new Error('fixture cache write failed');
    let fail = true;
    const storage = createStorage({
      driver: boundedCache({
        ...inner,
        setItem: (name, value, options) => {
          if (fail) throw failure;
          return inner.setItem!(name, value, options);
        }
      })
    });
    const write = storage.setItem(key, page);
    const read = storage.getItem(key);
    const keys = storage.getKeys('nitro:routes');
    await expect(write).rejects.toBe(failure);
    expect(await read).toBeNull();
    expect(await keys).toEqual([]);
    expect(await storage.getItem(key)).toBeNull();
    expect(await storage.getKeys('nitro:routes')).toEqual([]);
    await storage.removeItem(key);
    fail = false;
    await storage.setItem(key, page);
    expect(await storage.getItem(key)).toEqual(page);
  });

  it('allows a queued replacement to finish when the preceding write rejects', async () => {
    const { storage, started, release } = blockedWrite();
    const failure = new Error('fixture cache write failed');
    const first = storage.setItem(key, page);
    await started.promise;
    const rejected = expect(first).rejects.toBe(failure);
    const replacement = { body: 'replacement page' };
    const second = storage.setItem(key, replacement);
    release.reject(failure);
    await Promise.all([rejected, second]);
    expect(await storage.getItem(key)).toEqual(replacement);
  });
});
