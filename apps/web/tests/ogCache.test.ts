import { describe, expect, it, vi } from 'vitest';
import { clearOgImageCache, ogCacheMatcher } from '../server/utils/ogCache';

describe('share-image cache invalidation', () => {
  it('clears share-image entries only when og invalidation is requested', async () => {
    const keys = [
      '6.10.3:apps:ordinary-app:options-hash:component-hash',
      '6.10.3:apps:ordinary-app:versions:options-hash:component-hash',
      '6.10.3:apps:other-app:options-hash:component-hash',
      '6.10.3:categories:utilities:options-hash'
    ];
    const removeItem = vi.fn(async () => undefined);
    const storage = { getKeys: async () => keys, removeItem };

    expect(await clearOgImageCache(storage, ['/apps/ordinary-app'], false)).toBe(0);
    expect(removeItem).not.toHaveBeenCalled();

    expect(await clearOgImageCache(storage, ['/apps/ordinary-app'], true)).toBe(2);
    expect(removeItem.mock.calls.map(([key]) => key)).toEqual([keys[0], keys[1]]);
    expect(ogCacheMatcher(['/apps/ordinary-app'])('6.10.3:apps:ordinary-apple:options')).toBe(false);
  });
});
