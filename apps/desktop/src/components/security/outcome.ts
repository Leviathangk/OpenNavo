import type { ActOutcome } from '@/ipc/bindings';
import { IpcError } from '@/ipc/client';

/** An Ok IPC response can still contain per-item failures or cancellation. */
export function requireDone(outcome: ActOutcome | null, id: string) {
  const failure = outcome?.failed.find(item => item.id === id);
  if (failure || !outcome?.done.includes(id)) {
    throw new IpcError({
      code: failure?.code ?? 'E_UNKNOWN',
      message: failure?.code ?? 'E_UNKNOWN',
      detail: null
    });
  }
}
