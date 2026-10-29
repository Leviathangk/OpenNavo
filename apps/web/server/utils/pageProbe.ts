export const PROBE_MAX_ATTEMPTS = 3;
export const PROBE_BUDGET_MS = 10_000;
export const PROBE_MAX_RETRY_AFTER_SECONDS = 15 * 60;

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
  let timedOut = false;
  let timer: ReturnType<typeof setTimeout>;
  const timeout = new Promise<never>((_resolve, reject) => {
    timer = setTimeout(
      () => {
        timedOut = true;
        controller.abort();
        reject(new Error('temporary probe timeout'));
      },
      Math.max(1, Math.ceil(remaining))
    );
  });
  try {
    // Nitro local calls may finish after cancellation. Bound our wait as well as the transport.
    const request = fetch(path, controller.signal).then(async response => {
      if (timedOut) await response.body?.cancel();
      return response;
    });
    return await Promise.race([request, timeout]);
  } finally {
    clearTimeout(timer!);
  }
}

/** Honor Retry-After up to the configured cap, even beyond this run's time budget. */
export function probeRetryDelay(
  header: string | null,
  attempt: number,
  now: number,
  random: number,
  maxRetryAfterMs = PROBE_MAX_RETRY_AFTER_SECONDS * 1000
): number {
  const value = header?.trim();
  if (value && /^\d+$/.test(value)) {
    const seconds = Number(value);
    if (Number.isSafeInteger(seconds) && seconds <= 2147483647)
      return Math.min(maxRetryAfterMs, Math.max(100, seconds * 1000));
  }
  // HTTP dates start with a weekday; numeric and signed values must not become dates.
  const date = value && /^[a-z]/i.test(value) ? Date.parse(value) : NaN;
  if (Number.isFinite(date) && date > now) return Math.min(maxRetryAfterMs, date - now);
  return 500 * 2 ** attempt * (1 + random);
}

/** A process-wide admission bound, with no waiting queue or retained per-URL state. */
export function createPageProbe(concurrency: unknown, maxRetryAfterSeconds: unknown = PROBE_MAX_RETRY_AFTER_SECONDS) {
  const parsed = Number(concurrency);
  const limit = Number.isInteger(parsed) && parsed >= 1 ? Math.min(parsed, 4) : 1;
  const retryCap = Number(maxRetryAfterSeconds);
  const maxRetryAfterMs =
    Number.isFinite(retryCap) && retryCap > 0 && Number.isSafeInteger(retryCap * 1000)
      ? retryCap * 1000
      : PROBE_MAX_RETRY_AFTER_SECONDS * 1000;
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
    let pendingRequests = 0;
    let finished = false;
    const trackedFetch = async (path: string, signal: AbortSignal) => {
      pendingRequests++;
      try {
        return await fetch(path, signal);
      } finally {
        pendingRequests--;
        if (finished && pendingRequests === 0) active--;
      }
    };
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
            const response = await fetchWithinBudget(trackedFetch, path, remaining);
            statuses[path] = response.status;
            await response.body?.cancel();
            if (response.status !== 503 && response.status !== 429) break;
            retryAt =
              clock.now() +
              probeRetryDelay(
                response.headers.get('retry-after'),
                attempt,
                clock.now(),
                clock.random(),
                maxRetryAfterMs
              );
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
      // Returning the timeout does not mean Nitro stopped work; keep its admission slot until it settles.
      finished = true;
      if (pendingRequests === 0) active--;
    }
  };
}
