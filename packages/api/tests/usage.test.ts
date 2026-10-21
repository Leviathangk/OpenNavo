import { describe, expect, it, vi } from 'vitest';
import { createUsageTracker, usageDay, type UsageActivity } from '../src/usage';

const acknowledge = async (a: UsageActivity) => usageDay(Date.parse(a.occurredAt));

function fixture() {
  const values = new Map<string, string>();
  const storage = {
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    }
  };
  const send = vi.fn().mockImplementation(acknowledge);
  let clock = Date.parse('2026-10-22T15:59:00Z');
  let lock = Promise.resolve();
  const withLock = (_name: string, action: () => void) => {
    const next = lock.then(action);
    lock = next.catch(() => {});
    return next;
  };
  const make = (version = '', uuid = '2a77e45e-1ac2-4d72-b9c2-aaf2a42f8234') =>
    createUsageTracker({
      platform: 'web',
      version: () => version,
      locale: () => 'en-US',
      storage,
      withLock,
      canCollect: () => storage.getItem('onv:analytics-consent') !== 'denied',
      send,
      now: () => clock,
      uuid: () => uuid
    });
  return {
    make,
    send,
    values,
    storage,
    advance: (ms: number) => {
      clock += ms;
    }
  };
}

async function settle() {
  await new Promise(resolve => setTimeout(resolve, 0));
}

describe('opted-in daily usage outbox', () => {
  it('does not collect before consent; deduplicates reloads and records the new Shanghai day', async () => {
    const f = fixture();
    const tracker = f.make();
    tracker.activity();
    await tracker.flush();
    expect(f.values.size).toBe(0);
    expect(f.send).not.toHaveBeenCalled();
    tracker.setEnabled(true);
    tracker.activity();
    await settle();
    tracker.activity();
    await settle();
    expect(f.send).toHaveBeenCalledTimes(1);
    tracker.dispose();
    const reloaded = f.make();
    reloaded.setEnabled(true);
    reloaded.activity();
    await settle();
    expect(f.send).toHaveBeenCalledTimes(1);
    f.advance(60_000);
    reloaded.activity();
    await settle();
    expect(f.send).toHaveBeenCalledTimes(2);
    expect(usageDay(Date.parse(f.send.mock.calls[1]![0].occurredAt))).toBe('2026-10-23');
  });
  it('retains original offline dates, bounds the queue, and retries without throwing', async () => {
    const f = fixture();
    f.send.mockRejectedValue(new Error('offline'));
    const tracker = f.make();
    tracker.setEnabled(true);
    for (let day = 0; day < 12; day++) {
      tracker.activity();
      await settle();
      f.advance(86_400_000);
    }
    const saved = JSON.parse([...f.values.values()][0]!);
    expect(saved.pending.length).toBeLessThanOrEqual(8);
    f.send.mockImplementation(acknowledge);
    await tracker.flush();
    expect(JSON.parse([...f.values.values()][0]!).pending).toEqual([]);
    const dates = f.send.mock.calls.slice(-7).map(([a]) => a.occurredAt);
    expect(new Set(dates).size).toBe(7);
  });
  it('cancels in-flight reports and never resurrects identity or pending records after revocation', async () => {
    const f = fixture();
    let finish!: () => void;
    f.send.mockImplementation(
      () =>
        new Promise<string>(resolve => {
          finish = () => resolve('2026-10-22');
        })
    );
    const tracker = f.make();
    tracker.setEnabled(true);
    tracker.activity();
    await settle();
    const signal = f.send.mock.calls[0]![1] as AbortSignal;
    await tracker.revoke();
    expect(signal.aborted).toBe(true);
    expect(f.values.size).toBe(0);
    finish();
    await settle();
    expect(f.values.size).toBe(0);
    tracker.activity();
    expect(f.send).toHaveBeenCalledTimes(1);
  });
  it('does not invent ephemeral identities when persistent storage is unavailable', async () => {
    const f = fixture();
    f.storage.setItem = () => {
      throw new Error('blocked');
    };
    const tracker = f.make();
    tracker.setEnabled(true);
    tracker.activity();
    await tracker.flush();
    expect(f.send).not.toHaveBeenCalled();
  });
  it('coalesces repeated activity while a request is in flight', async () => {
    const f = fixture();
    let finish!: () => void;
    f.send.mockImplementation(
      () =>
        new Promise<string>(resolve => {
          finish = () => resolve('2026-10-22');
        })
    );
    const tracker = f.make();
    tracker.setEnabled(true);
    tracker.activity();
    tracker.activity();
    await settle();
    expect(f.send).toHaveBeenCalledTimes(1);
    finish();
    await settle();
    tracker.activity();
    await settle();
    expect(f.send).toHaveBeenCalledTimes(1);
  });
  it('discards future queued and sent days after clock rollback, including after reload', async () => {
    const f = fixture();
    const tracker = f.make();
    tracker.setEnabled(true);
    f.advance(2 * 86_400_000);
    await tracker.activity(); // A day acknowledged while the clock was ahead.
    f.advance(86_400_000);
    f.send.mockRejectedValue(new Error('future timestamp rejected'));
    await tracker.activity();
    tracker.dispose();
    f.advance(-3 * 86_400_000);
    f.send.mockReset().mockImplementation(acknowledge);
    const reloaded = f.make();
    reloaded.setEnabled(true);
    await reloaded.activity();
    expect(f.send).toHaveBeenCalledTimes(1);
    expect(f.send.mock.calls[0]![0].occurredAt).toBe('2026-10-22T15:59:00.000Z');
    const saved = JSON.parse([...f.values.values()][0]!);
    expect(saved.pending).toEqual([]);
    expect(Object.keys(saved.sent)).toEqual(['2026-10-22']);
  });
  it('preserves offline days from two already-open tabs across a browser restart', async () => {
    const f = fixture();
    f.send.mockRejectedValue(new Error('offline'));
    const first = f.make();
    const second = f.make('', 'da77e45e-1ac2-4d72-b9c2-aaf2a42f8234');
    first.setEnabled(true);
    second.setEnabled(true);
    await first.activity();
    f.advance(86_400_000);
    await second.activity();
    first.dispose();
    second.dispose();
    f.send.mockReset().mockImplementation(acknowledge);
    const reloaded = f.make();
    reloaded.setEnabled(true);
    await reloaded.flush();
    expect(f.send.mock.calls.map(([a]) => usageDay(Date.parse(a.occurredAt)))).toEqual(['2026-10-22', '2026-10-23']);
    expect(new Set(f.send.mock.calls.map(([a]) => a.anonymousId)).size).toBe(1);
    expect(JSON.parse([...f.values.values()][0]!).pending).toEqual([]);
  });
  it("does not overwrite another tab's new offline day when an older HTTP request completes", async () => {
    const f = fixture();
    let finish!: () => void;
    f.send.mockImplementationOnce(
      () =>
        new Promise<string>(resolve => {
          finish = () => resolve('2026-10-22');
        })
    );
    const first = f.make();
    const second = f.make();
    first.setEnabled(true);
    second.setEnabled(true);
    const inFlight = first.activity();
    await settle();
    f.advance(86_400_000);
    f.send.mockRejectedValue(new Error('offline'));
    await second.activity();
    finish();
    await inFlight;
    const saved = JSON.parse([...f.values.values()][0]!);
    expect(Object.keys(saved.sent)).toEqual(['2026-10-22']);
    expect(saved.pending.map((a: { occurredAt: string }) => usageDay(Date.parse(a.occurredAt)))).toEqual([
      '2026-10-23'
    ]);
    // The formerly stale tab must also honor the other tab's acknowledgement.
    f.send.mockReset().mockImplementation(acknowledge);
    await second.flush();
    expect(f.send).toHaveBeenCalledTimes(1);
  });
  it('retains a newer same-day version recorded by another tab during HTTP', async () => {
    const f = fixture();
    let finish!: () => void;
    f.send.mockImplementationOnce(
      () =>
        new Promise<string>(resolve => {
          finish = () => resolve('2026-10-22');
        })
    );
    const first = f.make('1.0.0');
    const second = f.make('1.0.1');
    first.setEnabled(true);
    second.setEnabled(true);
    const inFlight = first.activity();
    await settle();
    f.send.mockRejectedValue(new Error('offline'));
    await second.activity();
    finish();
    await inFlight;
    const saved = JSON.parse([...f.values.values()][0]!);
    expect(saved.pending).toHaveLength(1);
    expect(saved.pending[0].version).toBe('1.0.1');
    expect(saved.sent).toEqual({});
  });
  it('serializes simultaneous first visits and blocks queued writes after cross-tab consent denial', async () => {
    const f = fixture();
    f.send.mockRejectedValue(new Error('offline'));
    const first = f.make();
    const second = f.make('', 'da77e45e-1ac2-4d72-b9c2-aaf2a42f8234');
    first.setEnabled(true);
    second.setEnabled(true);
    await Promise.all([first.activity(), second.activity()]);
    expect(new Set(f.send.mock.calls.map(([a]) => a.anonymousId)).size).toBe(1);
    f.storage.setItem('onv:analytics-consent', 'denied');
    await first.revoke();
    await second.activity(); // Even before its storage event is delivered.
    expect(f.storage.getItem('onv:usage:v1:web')).toBeNull();
  });
  it('acknowledges the server day with a three-minute fast clock and still reports after real midnight', async () => {
    const f = fixture();
    // Fixture is 23:59 Shanghai, move client clock to 00:01 while server is 23:58.
    f.advance(2 * 60_000);
    f.send.mockResolvedValue('2026-10-22');
    const first = f.make();
    first.setEnabled(true);
    await first.activity();
    expect(JSON.parse([...f.values.values()][0]!).sent).toEqual({ '2026-10-22': '' });
    first.dispose();
    const restarted = f.make();
    restarted.setEnabled(true);
    f.advance(2 * 60_000); // Server now 00:00; client 00:03.
    f.send.mockResolvedValue('2026-10-23');
    await restarted.activity();
    await restarted.activity();
    expect(f.send).toHaveBeenCalledTimes(2);
    expect(JSON.parse([...f.values.values()][0]!).sent).toEqual({
      '2026-10-22': '',
      '2026-10-23': ''
    });
  });
  it('keeps the queue when an old server omits its acknowledged day', async () => {
    const f = fixture();
    f.send.mockResolvedValue('');
    const tracker = f.make();
    tracker.setEnabled(true);
    await tracker.activity();
    const saved = JSON.parse([...f.values.values()][0]!);
    expect(saved.sent).toEqual({});
    expect(saved.pending).toHaveLength(1);
    f.send.mockImplementation(acknowledge);
    await tracker.flush();
    expect(JSON.parse([...f.values.values()][0]!).pending).toEqual([]);
  });
});
