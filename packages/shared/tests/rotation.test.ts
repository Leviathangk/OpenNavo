import { describe, expect, it, vi } from 'vitest';
import { rotateDaily, utcDay } from '../src/rotation';

describe('rotateDaily', () => {
  it('selects a deterministic wrapping window for a UTC day', () => {
    const items = Array.from({ length: 10 }, (_, index) => index);
    const day = new Date('2026-11-06T12:00:00Z');
    expect(rotateDaily(items, 4, 4, day)).toEqual([2, 3, 4, 5]);
    expect(rotateDaily(items, 4, 7, day)).toEqual([2, 3, 4, 5, 6, 7, 8]);
    expect(rotateDaily(items, 4, 4, day)).toEqual(rotateDaily(items, 4, 4, new Date('2026-11-06T23:59:59Z')));
    expect(rotateDaily(items, 4, 4, new Date('2026-11-07T00:00:00Z'))).not.toEqual(rotateDaily(items, 4, 4, day));
  });

  it('uses UTC calendar days', () => {
    expect(utcDay(new Date('2026-11-06T23:59:59Z'))).toBe(utcDay(new Date('2026-11-06T08:00:00Z')));
    expect(utcDay(new Date('2026-11-07T00:00:00Z'))).toBe(utcDay(new Date('2026-11-06T23:59:59Z')) + 1);
  });

  it('returns no items for an empty list or non-positive output size', () => {
    expect(rotateDaily([], 4)).toEqual([]);
    expect(rotateDaily([1, 2, 3], 2, 0)).toEqual([]);
    expect(rotateDaily([1, 2, 3], 2, -1)).toEqual([]);
  });

  it('keeps ranked order when rotation is disabled or the list fits the window', () => {
    const items = [1, 2, 3];
    expect(rotateDaily(items, 0, 2)).toEqual([1, 2]);
    expect(rotateDaily(items, -1, 2)).toEqual([1, 2]);
    expect(rotateDaily(items, 3, 2)).toEqual([1, 2]);
    expect(rotateDaily(items, 4, 5)).toEqual(items);
    expect(rotateDaily(items, 3)).not.toBe(items);
  });

  it('wraps at the end of the list and never repeats items for oversized output', () => {
    const items = Array.from({ length: 10 }, (_, index) => index);
    const day = new Date('1970-01-03T00:00:00Z');
    expect(rotateDaily(items, 4, 4, day)).toEqual([8, 9, 0, 1]);
    expect(rotateDaily(items, 4, 20, day)).toEqual([8, 9, 0, 1, 2, 3, 4, 5, 6, 7]);
  });

  it('defaults to the current day and the requested window size', () => {
    vi.useFakeTimers();
    try {
      vi.setSystemTime(new Date('1970-01-03T12:00:00Z'));
      expect(utcDay()).toBe(2);
      expect(rotateDaily([0, 1, 2, 3, 4], 2)).toEqual([4, 0]);
      const items = Array.from({ length: 10 }, (_, index) => index);
      const day = new Date('1970-01-03T12:00:00Z');
      expect(rotateDaily(items, 4, undefined, day)).toEqual(rotateDaily(items, 4, 4, day));
    } finally {
      vi.useRealTimers();
    }
  });
});
