import { describe, expect, it, vi } from 'vitest';
import { renderMemoryMetrics } from '../server/utils/memoryMetrics';

describe('process memory metrics', () => {
  it('exports label-free gauges for heap, RSS, and external allocations', () => {
    vi.spyOn(process, 'memoryUsage').mockReturnValue({
      rss: 123,
      heapTotal: 456,
      heapUsed: 234,
      external: 345,
      arrayBuffers: 12
    });
    const text = renderMemoryMetrics();
    expect(text).toContain('opennavo_web_heap_used_bytes 234');
    expect(text).toMatch(/opennavo_web_heap_limit_bytes \d+/);
    expect(text).toContain('opennavo_web_rss_bytes 123');
    expect(text).toContain('opennavo_web_external_bytes 345');
    expect(text).not.toMatch(/\{[^}]+\}/);
    vi.restoreAllMocks();
  });
});
