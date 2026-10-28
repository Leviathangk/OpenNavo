import { describe, expect, it, vi } from 'vitest';
import { clearSitemapStorage } from '../server/utils/sitemapStorage';

describe('sitemap storage invalidation', () => {
  it('removes every cached sitemap entry after a settings change', async () => {
    const storage = {
      getKeys: vi.fn().mockResolvedValue(['sitemap:index', 'sitemap:bucket']),
      removeItem: vi.fn().mockResolvedValue(undefined)
    };

    await clearSitemapStorage(storage);

    expect(storage.removeItem).toHaveBeenCalledWith('sitemap:index');
    expect(storage.removeItem).toHaveBeenCalledWith('sitemap:bucket');
    expect(storage.removeItem).toHaveBeenCalledTimes(2);
  });
});
