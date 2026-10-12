import { commands, events } from './bindings';
import type { AppError, CatalogItem_Serialize, SearchHit_Serialize, Settings, TaskKind } from './bindings';

export { commands, events };

/** Local catalog entry from Rust (06 §5.1; fields match OpenAPI CatalogItem). */
export type LocalItem = CatalogItem_Serialize;
export type LocalSearchHit = SearchHit_Serialize;

/**
 * Full settings (06 §11). Rust uses `#[serde(default)]`, making generated fields optional; missing writes use defaults,
 * not current values. settings_get / settings_set always return full objects, and the UI always sends full objects.
 */
export type AppSettings = Required<Settings>;

/** IPC command failure: `code` is a 06 §5.4 client error code used to select UI copy. */
export class IpcError extends Error {
  readonly code: string;
  readonly detail: string | null;

  constructor(error: AppError) {
    super(error.message);
    this.name = 'IpcError';
    this.code = error.code;
    this.detail = error.detail;
  }
}

type CommandResult<T> = { status: 'ok'; data: T } | { status: 'error'; error: AppError };

/** tauri-specta returns `{ status, data | error }`; consistently throw IpcError in the UI for await / try handling. */
export async function unwrap<T>(result: Promise<CommandResult<T>>): Promise<T> {
  const value = await result;
  if (value.status === 'error') throw new IpcError(value.error);
  return value.data;
}

/** Associate local state (installed, tasks, ignored) by `kind/token`; `app/<id>` keys local apps outside Homebrew. */
export const packageKey = (kind: TaskKind, token: string) => `${kind}/${token}`;
