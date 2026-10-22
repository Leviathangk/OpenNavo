import { describe, expect, it } from 'vitest';
import { LOAD_SHED_DEFAULTS, loadShedLevel, shouldShed, type LoadShedTier } from '../server/utils/loadShed';

// Deterministic single-thread queue: one cold SSR consumes 100ms CPU. This conservatively ignores
// delay-triggered shedding and cache hits; real event-loop delay can refuse work earlier.
function simulate(crawlerRps: number, tier: LoadShedTier, enabled = true, seconds = 30) {
  const arrivals: { at: number; tier: LoadShedTier }[] = [];
  for (let i = 0; i < seconds * 3; i++) arrivals.push({ at: (i * 1000) / 3, tier: 'protected' });
  for (let i = 0; i < seconds * crawlerRps; i++) arrivals.push({ at: (i * 1000) / crawlerRps, tier });
  arrivals.sort((a, b) => a.at - b.at);
  const completions: number[] = [];
  let maxInFlight = 0;
  let maxLatencyMs = 0;
  let shed = 0;
  let protectedShed = 0;
  for (const request of arrivals) {
    while (completions.length && completions[0]! <= request.at) completions.shift();
    const level = loadShedLevel(completions.length, 20, LOAD_SHED_DEFAULTS);
    if (enabled && shouldShed(request.tier, level)) {
      shed++;
      if (request.tier === 'protected') protectedShed++;
      continue;
    }
    const end = Math.max(completions.at(-1) ?? 0, request.at) + 100;
    completions.push(end);
    maxInFlight = Math.max(maxInFlight, completions.length);
    maxLatencyMs = Math.max(maxLatencyMs, end - request.at);
  }
  return { maxInFlight, maxLatencyMs, shed, protectedShed };
}

describe('production-capacity synthetic queue', () => {
  it('admits normal 3/s without shedding', () => {
    const result = simulate(0, 'low');
    expect(result).toMatchObject({ maxInFlight: 1, shed: 0, protectedShed: 0 });
    expect(result.maxLatencyMs).toBeCloseTo(100);
  });

  it.each([17, 40])('bounds a %i/s training crawler burst while admitting normal protected traffic', rate => {
    const guarded = simulate(rate, 'low');
    const unguarded = simulate(rate, 'low', false);
    expect(guarded.protectedShed).toBe(0);
    expect(guarded.shed).toBeGreaterThan(0);
    expect(guarded.maxLatencyMs).toBeLessThanOrEqual(1000);
    expect(unguarded.maxLatencyMs).toBeGreaterThan(guarded.maxLatencyMs * 20);
    console.info('synthetic cold SSR: 3 protected/s plus crawler burst', { rate, guarded, unguarded });
  });

  it('reserves capacity for protected traffic during a search-index burst', () => {
    const result = simulate(40, 'medium');
    expect(result.protectedShed).toBe(0);
    expect(result.maxLatencyMs).toBeLessThanOrEqual(1800);
    expect(result.shed).toBeGreaterThan(0);
  });

  it('uses the protected limit only as the final backstop, bounding the queue well below 60s', () => {
    const result = simulate(40, 'protected');
    expect(result.maxInFlight).toBe(64);
    expect(result.maxLatencyMs).toBeLessThanOrEqual(6400);
    expect(result.protectedShed).toBeGreaterThan(0);
  });
});
