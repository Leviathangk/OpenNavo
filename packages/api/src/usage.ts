import type { components } from './public';

export type UsageActivity = components['schemas']['UsageActivity'];
const DAY = 86_400_000;
const KEY = 'onv:usage:v1:';
export const usageDay = (at: number) => new Date(at + 8 * 3_600_000).toISOString().slice(0, 10);
interface History {
  id: string;
  sent: Record<string, string>;
  pending: UsageActivity[];
}
interface Options {
  platform: UsageActivity['platform'];
  version: () => string;
  locale: () => string;
  storage: {
    getItem(key: string): string | null;
    setItem(key: string, value: string): void;
    removeItem(key: string): void;
  };
  /** Must serialize callbacks across every tab sharing storage. Never hold it during HTTP. */
  withLock: (name: string, action: () => void) => Promise<void>;
  canCollect?: () => boolean;
  send: (activity: UsageActivity, signal: AbortSignal) => Promise<string>;
  now?: () => number;
  uuid?: () => string;
}

/** Consent-gated, bounded daily outbox. No account, fingerprint, URL or installed-app data. */
export function createUsageTracker(options: Options) {
  const now = options.now ?? Date.now;
  const key = KEY + options.platform;
  let enabled = false;
  let sending = false;
  let controller: AbortController | undefined;
  let epoch = 0;

  function read(create: boolean): History | undefined {
    const saved = JSON.parse(options.storage.getItem(key) ?? 'null') as History | null;
    if (saved && /^[0-9a-f-]{36}$/i.test(saved.id) && saved.sent && Array.isArray(saved.pending)) {
      return {
        id: saved.id,
        sent: saved.sent,
        pending: saved.pending.filter(
          a =>
            a.anonymousId === saved.id &&
            a.platform === options.platform &&
            typeof a.occurredAt === 'string' &&
            Number.isFinite(Date.parse(a.occurredAt)) &&
            typeof a.version === 'string' &&
            typeof a.locale === 'string'
        )
      };
    }
    return create ? { id: (options.uuid ?? (() => crypto.randomUUID()))(), sent: {}, pending: [] } : undefined;
  }
  function prune(history: History) {
    const at = now();
    const cutoff = at - 7 * DAY;
    // A corrected clock must not leave an unsendable future record blocking the queue.
    history.pending = history.pending
      .filter(a => Date.parse(a.occurredAt) >= cutoff && Date.parse(a.occurredAt) <= at)
      .slice(-8);
    for (const day of Object.keys(history.sent)) {
      if (day < usageDay(cutoff) || day > usageDay(at)) delete history.sent[day];
    }
  }
  async function update(current: number, create: boolean, action: (history: History) => void) {
    try {
      await options.withLock(key, () => {
        if (!enabled || epoch !== current || options.canCollect?.() === false) return;
        // Always mutate the latest stored state, including other tabs' offline days and acknowledgements.
        const history = read(create);
        if (!history) return;
        prune(history);
        action(history);
        options.storage.setItem(key, JSON.stringify(history));
      });
      return true;
    } catch {
      // Restricted storage/locks must not produce ephemeral identities or break the page.
      return false;
    }
  }
  function setEnabled(value: boolean) {
    if (value === enabled) return;
    if (!value) {
      void revoke();
      return;
    }
    epoch++;
    enabled = true;
    controller?.abort();
    sending = false;
  }
  async function flush() {
    if (!enabled || sending) return;
    const current = epoch;
    sending = true;
    controller = new AbortController();
    const signal = controller.signal;
    try {
      let batch: UsageActivity[] = [];
      if (
        !(await update(current, false, history => {
          batch = history.pending.slice();
        }))
      )
        return;
      for (const entry of batch) {
        if (!enabled || epoch !== current) break;
        let present = false;
        // Recheck after every HTTP await: the clock or another tab may have changed the queue.
        if (
          !(await update(current, false, history => {
            present = history.pending.some(a => sameActivity(a, entry));
          })) ||
          !present
        )
          continue;
        if (!enabled || epoch !== current || signal.aborted) break;
        let acknowledgedDay: string;
        try {
          acknowledgedDay = await options.send(entry, signal);
          // Never fall back to the local date: a missing acknowledgement could hide clock correction.
          if (!/^\d{4}-\d{2}-\d{2}$/.test(acknowledgedDay)) break;
        } catch {
          break;
        }
        await update(current, false, history => {
          if (history.id !== entry.anonymousId) return;
          const day = usageDay(Date.parse(entry.occurredAt));
          const pending = history.pending.find(a => usageDay(Date.parse(a.occurredAt)) === day);
          // A newer version recorded while HTTP was in flight must remain queued.
          if (!pending || !sameActivity(pending, entry)) return;
          history.sent[acknowledgedDay] = entry.version;
          history.pending = history.pending.filter(a => !sameActivity(a, entry));
        });
      }
    } finally {
      if (epoch === current) sending = false;
    }
  }
  async function activity() {
    if (!enabled) return;
    const current = epoch;
    const persisted = await update(current, true, history => {
      const at = now();
      const day = usageDay(at);
      const version = options.version();
      if (history.sent[day] === version) return;
      const existing = history.pending.find(a => usageDay(Date.parse(a.occurredAt)) === day);
      if (!existing || existing.version !== version) {
        history.pending = history.pending.filter(a => a !== existing);
        history.pending.push({
          anonymousId: history.id,
          platform: options.platform,
          occurredAt: new Date(at).toISOString(),
          version,
          locale: options.locale()
        });
      }
    });
    if (persisted && epoch === current) await flush();
  }
  function dispose() {
    epoch++;
    enabled = false;
    controller?.abort();
    sending = false;
  }
  // Clear a previous identity on opt-out even when this instance never started.
  async function revoke() {
    dispose();
    try {
      await options.withLock(key, () => options.storage.removeItem(key));
    } catch {
      /* Restricted storage. */
    }
  }
  return { setEnabled, activity, flush, dispose, revoke };
}

function sameActivity(a: UsageActivity, b: UsageActivity) {
  return a.anonymousId === b.anonymousId && a.occurredAt === b.occurredAt && a.version === b.version;
}
