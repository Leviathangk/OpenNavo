export const PROBE_MAX_ATTEMPTS = 3;
export const PROBE_BUDGET_MS = 10_000;

export class ProbeBusyError extends Error {}

interface ProbeResponse {
  status: number;
  headers: Headers;
  body?: ReadableStream | null;
}

interface ProbeClock {
  now: () => number;
  random: () => number;
  pause: (ms: number) => Promise<void>;
}

async function fetchWithinBudget(
  fetch: (path: string, signal: AbortSignal) => Promise<ProbeResponse>,
  path: string,
  remaining: number
) {
  const controller = new AbortController();
  let timer: ReturnType<typeof setTimeout>;
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(
      () => {
        controller.abort();
        reject(new Error('temporary probe timeout'));
      },
      Math.max(1, Math.ceil(remaining))
    );
  });
  try {
    // Nitro local calls may finish after cancellation. Bound our wait as well as the transport.
    return await Promise.race([fetch(path, controller.signal), timeout]);
  } finally {
    clearTimeout(timer!);
  }
}

/** Retry-After is a lower bound, including when it exceeds this run's time budget. */
export function probeRetryDelay(header: string | null, attempt: number, now: number, random: number): number {
  if (header !== null && /^\d+$/.test(header.trim())) {
    const seconds = Number(header);
    if (Number.isSafeInteger(seconds) && seconds <= 2147483647) return Math.max(100, seconds * 1000);
  }
  const date = header ? Date.parse(header) : NaN;
  if (Number.isFinite(date) && date > now) return date - now;
  return 500 * 2 ** attempt * (1 + random);
}

/** A process-wide admission bound, with no waiting queue or retained per-URL state. */
export function createPageProbe(concurrency: unknown) {
  const parsed = Number(concurrency);
  const limit = Number.isInteger(parsed) && parsed >= 1 ? Math.min(parsed, 4) : 1;
  let active = 0;
  let cooldownUntil = 0;
  return async (
    paths: string[],
    fetch: (path: string, signal: AbortSignal) => Promise<ProbeResponse>,
    clock: ProbeClock = {
      now: Date.now,
      random: Math.random,
      pause: ms => new Promise(resolve => setTimeout(resolve, ms))
    }
  ) => {
    if (active >= limit) throw new ProbeBusyError('temporary probe capacity failure');
    active++;
    const statuses: Record<string, number> = {};
    const temporaryFailures: Record<string, { retryAfterSeconds: number }> = {};
    const deadline = clock.now() + PROBE_BUDGET_MS;
    try {
      for (const path of paths) {
        let retryAt = Math.max(cooldownUntil, clock.now() + 1000);
        statuses[path] = 503;
        if (cooldownUntil > clock.now()) {
          temporaryFailures[path] = { retryAfterSeconds: Math.ceil((cooldownUntil - clock.now()) / 1000) };
          continue;
        }
        for (let attempt = 0; attempt < PROBE_MAX_ATTEMPTS; attempt++) {
          const remaining = deadline - clock.now();
          if (remaining <= 0) break;
          try {
            const response = await fetchWithinBudget(fetch, path, remaining);
            statuses[path] = response.status;
            await response.body?.cancel();
            if (response.status !== 503 && response.status !== 429) break;
            retryAt =
              clock.now() + probeRetryDelay(response.headers.get('retry-after'), attempt, clock.now(), clock.random());
          } catch {
            statuses[path] = 503;
            retryAt = clock.now() + probeRetryDelay(null, attempt, clock.now(), clock.random());
          }
          if (attempt + 1 >= PROBE_MAX_ATTEMPTS || retryAt >= deadline) break;
          await clock.pause(Math.max(0, retryAt - clock.now()));
        }
        if (statuses[path] === 503 || statuses[path] === 429) {
          // One scalar cooldown covers later batches too, including legacy callers that only read statuses.
          cooldownUntil = Math.max(cooldownUntil, retryAt);
          temporaryFailures[path] = { retryAfterSeconds: Math.max(1, Math.ceil((retryAt - clock.now()) / 1000)) };
        }
      }
      return { statuses, temporaryFailures };
    } finally {
      active--;
    }
  };
}
