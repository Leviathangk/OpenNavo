import { describe, expect, it, vi } from 'vitest';
import { createPageProbe, probeRetryDelay, PROBE_BUDGET_MS, ProbeBusyError } from '../server/utils/pageProbe';

function clock() {
  let now = Date.parse('2026-01-01T00:00:00Z');
  const waits: number[] = [];
  return {
    now: () => now,
    random: () => 0.5,
    pause: async (ms: number) => {
      waits.push(ms);
      now += ms;
    },
    advance: (ms: number) => {
      now += ms;
    },
    waits
  };
}
const response = (status: number, retryAfter?: string) => ({
  status,
  headers: new Headers(retryAfter === undefined ? {} : { 'retry-after': retryAfter })
});

describe('bounded internal page probes', () => {
  it('honors seconds and dates, then uses exponential jitter for invalid headers', () => {
    const now = Date.parse('2026-01-01T00:00:00Z');
    expect(probeRetryDelay('120', 0, now, 0)).toBe(120_000);
    expect(probeRetryDelay('Thu, 01 Jan 2026 00:02:00 GMT', 0, now, 0)).toBe(120_000);
    expect(probeRetryDelay(null, 0, now, 0.5)).toBe(750);
    expect(probeRetryDelay('invalid', 1, now, 0.5)).toBe(1500);
    expect(probeRetryDelay('9'.repeat(4000), 0, now, 0.5)).toBe(750);
    expect(probeRetryDelay('0', 0, now, 0)).toBe(100);
    for (const header of ['-1', '-9999', '+9999', '9999999999', 'Thu, 01 Jan 2026 00:00:00 GMT']) {
      expect(probeRetryDelay(header, 0, now, 0.5)).toBe(750);
    }
    for (const header of ['2147483647', 'Fri, 31 Dec 9999 23:59:59 GMT']) {
      expect(probeRetryDelay(header, 0, now, 0)).toBe(15 * 60_000);
      expect(probeRetryDelay(header, 0, now, 0, 45_000)).toBe(45_000);
    }
  });

  it.each(['2147483647', 'Fri, 31 Dec 9999 23:59:59 GMT'])(
    'caps Retry-After %s across batches and resumes after the bounded cooldown',
    async header => {
      for (const configured of [undefined, 45, '60', 0, -1, 'invalid', Infinity]) {
        const seconds = configured === 45 ? 45 : configured === '60' ? 60 : 900;
        const time = clock();
        const fetch = vi.fn(async () => response(503, header));
        const probe = createPageProbe(1, configured);
        const first = await probe(['/recent'], fetch, time);
        expect(first.temporaryFailures['/recent']).toEqual({ retryAfterSeconds: seconds });
        const waiting = await probe(['/ja/recent'], fetch, time);
        expect(waiting.temporaryFailures['/ja/recent']).toEqual({ retryAfterSeconds: seconds });
        expect(fetch).toHaveBeenCalledTimes(1);
        time.advance(seconds * 1000);
        const recovered = await probe(['/recent'], async () => response(200), time);
        expect(recovered.statuses['/recent']).toBe(200);
      }
    }
  );

  it('retries 429/503, releases response bodies and recovers without marking a page missing', async () => {
    const time = clock();
    let calls = 0;
    let cancelled = 0;
    const result = await createPageProbe(1)(
      ['/zh/recent'],
      async (_path, signal) => {
        expect(signal.aborted).toBe(false);
        calls++;
        return {
          ...response(calls === 1 ? 429 : calls === 2 ? 503 : 200, '1'),
          body: {
            cancel: async () => {
              cancelled++;
            }
          } as unknown as ReadableStream
        };
      },
      time
    );
    expect(calls).toBe(3);
    expect(cancelled).toBe(3);
    expect(time.waits).toEqual([1000, 1000]);
    expect(result).toEqual({ statuses: { '/zh/recent': 200 }, temporaryFailures: {} });
  });

  it('defers long Retry-After and exhausted attempts as temporary failures', async () => {
    const time = clock();
    let calls = 0;
    const probe = createPageProbe(1);
    const deferred = await probe(
      ['/apps/x/versions'],
      async () => {
        calls++;
        return response(503, '120');
      },
      time
    );
    expect(calls).toBe(1);
    expect(time.waits).toEqual([]);
    expect(deferred.temporaryFailures['/apps/x/versions']).toEqual({ retryAfterSeconds: 120 });
    const waiting = await probe(
      ['/ja/recent'],
      async () => {
        calls++;
        return response(200);
      },
      time
    );
    expect(calls).toBe(1, 'a later batch must honor the same origin cooldown');
    expect(waiting.temporaryFailures['/ja/recent']).toEqual({ retryAfterSeconds: 120 });
    time.advance(120_000);
    calls = 0;
    const exhausted = await probe(
      ['/recent'],
      async () => {
        calls++;
        return response(429);
      },
      time
    );
    expect(calls).toBe(3);
    expect(exhausted.statuses['/recent']).toBe(429);
    expect(exhausted.temporaryFailures['/recent']).toBeDefined();
    expect(time.waits).toEqual([750, 1500]);
  });

  it('shares one total deadline across paths and does not retry permanent errors', async () => {
    const time = clock();
    const calls: string[] = [];
    const result = await createPageProbe(1)(
      ['/missing', '/slow', '/later'],
      async path => {
        calls.push(path);
        if (path === '/slow') time.advance(PROBE_BUDGET_MS);
        return response(path === '/missing' ? 404 : 503);
      },
      time
    );
    expect(calls).toEqual(['/missing', '/slow']);
    expect(result.statuses).toEqual({ '/missing': 404, '/slow': 503, '/later': 503 });
    expect(result.temporaryFailures['/missing']).toBeUndefined();
    expect(result.temporaryFailures['/later']).toBeDefined();
  });

  it('returns within the budget even when an internal transport ignores cancellation', async () => {
    vi.useFakeTimers();
    try {
      let signal!: AbortSignal;
      const pending = createPageProbe(1)(['/hung', '/later'], async (_path, value) => {
        signal = value;
        return new Promise(() => {});
      });
      await vi.advanceTimersByTimeAsync(PROBE_BUDGET_MS);
      const result = await pending;
      expect(signal.aborted).toBe(true);
      expect(result.statuses).toEqual({ '/hung': 503, '/later': 503 });
      expect(Object.keys(result.temporaryFailures)).toEqual(['/hung', '/later']);
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });

  it('bounds simultaneous callers without retaining a queue, and releases capacity after failure', async () => {
    for (const concurrency of [undefined, 0, -1, 'invalid', 1, 2]) {
      const probe = createPageProbe(concurrency);
      let finish!: () => void;
      const pending = new Promise<void>(resolve => {
        finish = resolve;
      });
      const fetch = async () => {
        await pending;
        return response(200);
      };
      const runs = [probe(['/recent'], fetch)];
      if (concurrency === 2) runs.push(probe(['/ja/recent'], fetch));
      await expect(probe(['/extra'], fetch)).rejects.toBeInstanceOf(ProbeBusyError);
      finish();
      await Promise.all(runs);
      expect((await probe(['/recent'], async () => response(200))).statuses['/recent']).toBe(200);
    }
  });

  it.each(['resolve', 'reject'])(
    'holds capacity after timeout until an uncancellable request settles by %s',
    async settlement => {
      vi.useFakeTimers();
      try {
        const probe = createPageProbe(2);
        const settlers: Array<() => void> = [];
        const cancel = vi.fn(async () => {});
        const success = { ...response(200), body: { cancel } as unknown as ReadableStream };
        const fetch = vi.fn(
          async () =>
            new Promise<typeof success>((resolve, reject) => {
              const shouldReject = settlement === 'reject' && settlers.length < 2;
              settlers.push(() => (shouldReject ? reject(new Error('late failure')) : resolve(success)));
            })
        );
        const runs = [probe(['/one'], fetch), probe(['/two'], fetch)];
        await vi.advanceTimersByTimeAsync(PROBE_BUDGET_MS);
        expect((await Promise.all(runs)).map(result => Object.values(result.statuses))).toEqual([[503], [503]]);
        const extra = vi.fn(async () => response(200));
        await expect(probe(['/extra'], extra)).rejects.toBeInstanceOf(ProbeBusyError);
        expect(extra).not.toHaveBeenCalled();
        settlers[0]();
        await vi.advanceTimersByTimeAsync(0);
        // Retain the second hung request while admitting one replacement.
        await vi.advanceTimersByTimeAsync(2000);
        const replacement = probe(['/replacement'], fetch);
        expect(fetch).toHaveBeenCalledTimes(3);
        await expect(probe(['/extra'], extra)).rejects.toBeInstanceOf(ProbeBusyError);
        settlers[1]();
        settlers[2]();
        await replacement;
        expect((await probe(['/extra'], extra)).statuses['/extra']).toBe(200);
        expect(cancel).toHaveBeenCalledTimes(settlement === 'resolve' ? 3 : 1);
        expect(vi.getTimerCount()).toBe(0);
      } finally {
        vi.useRealTimers();
      }
    }
  );
});
