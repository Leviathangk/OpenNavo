import { describe, expect, it } from 'vitest';
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
});
