import { describe, expect, it, vi } from 'vitest';
import { createStaleCache } from '../server/utils/staleCache';

function setup() {
  let time = 0;
  let version = 0;
  const load = vi.fn(async () => {
    version += 1;
    return version;
  });
  const onError = vi.fn();
  const get = createStaleCache(load, { freshMs: 1000, retryMs: 100, onError, now: () => time });
  return { get, load, onError, advance: (ms: number) => (time += ms) };
}

describe('Stale-while-revalidate value cache', () => {
  it('waits only for the first load, then answers at once and refreshes behind the response', async () => {
    const { get, load, advance } = setup();
    expect(await Promise.all([get(), get()])).toEqual([1, 1]);
    expect(load).toHaveBeenCalledTimes(1);
    advance(999);
    expect(await get()).toBe(1);
    expect(load).toHaveBeenCalledTimes(1);
    advance(2);
    // Stale: the old value answers while one refresh runs.
    expect(await Promise.all([get(), get(), get()])).toEqual([1, 1, 1]);
    await get.idle();
    expect(load).toHaveBeenCalledTimes(2);
    expect(await get()).toBe(2);
  });

  it('fails the caller only when nothing was loaded yet', async () => {
    const { get, load } = setup();
    load.mockRejectedValueOnce(new Error('API down'));
    await expect(get()).rejects.toThrow('API down');
    expect(await get()).toBe(1);
  });

  it('peeks without waiting: nothing until the first load finishes, then the value while it refreshes', async () => {
    const { get, load, onError, advance } = setup();
    load.mockRejectedValueOnce(new Error('API down'));
    expect(get.peek()).toBeUndefined();
    await get.idle();
    expect(onError).toHaveBeenCalledTimes(1);
    expect(get.peek()).toBeUndefined();
    await get.idle();
    expect(get.peek()).toBe(1);
    advance(1001);
    expect(get.peek()).toBe(1);
    await get.idle();
    expect(load).toHaveBeenCalledTimes(3);
    expect(get.peek()).toBe(2);
  });

  it('keeps serving the previous value through failed refreshes, retrying after a pause', async () => {
    const { get, load, onError, advance } = setup();
    expect(await get()).toBe(1);
    advance(1001);
    load.mockRejectedValueOnce(new Error('API down'));
    expect(await get()).toBe(1);
    await get.idle();
    expect(onError).toHaveBeenCalledTimes(1);
    expect(await get()).toBe(1);
    await get.idle();
    expect(load).toHaveBeenCalledTimes(2);
    advance(101);
    expect(await get()).toBe(1);
    await get.idle();
    expect(load).toHaveBeenCalledTimes(3);
    expect(await get()).toBe(2);
  });
});
