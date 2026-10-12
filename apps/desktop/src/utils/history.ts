import type { HistoryQuery, Task } from '@/ipc/bindings';
import { commands, unwrap } from '@/ipc/client';

/**
 * Cask-only catalog (ADR-018): history shows only apps and maintenance tasks without targets (e.g. cache cleanup).
 * Filter legacy command-line records in the UI; native code no longer creates them, so subtract only those actually read from the total.
 */
export function isListedTask(task: Task): boolean {
  return task.target === null || task.target.kind !== 'formula';
}

// The UI may read multiple pages, but every IPC call must respect the native limit of 200 records.
export async function loadHistory(query: HistoryQuery) {
  const items: Task[] = [];
  let total = 0;
  do {
    const page = await unwrap(
      commands.historyList({
        ...query,
        limit: Math.min(200, query.limit - items.length),
        offset: query.offset + items.length
      })
    );
    total = page.total;
    items.push(...page.items);
    if (page.items.length === 0) break;
  } while (items.length < query.limit && query.offset + items.length < total);
  const listed = items.filter(isListedTask);
  return { items: listed, total: total - (items.length - listed.length) };
}
