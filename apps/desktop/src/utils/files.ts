import { isTauri } from '@tauri-apps/api/core';

// Browser mode (pnpm dev:web) has no system file dialog; return a fixed path for simulated IPC.
const BROWSER_PATH = '~/Brewfile';

/** Choose a save path (Brewfile export, history CSV); return null on cancel. */
export async function pickSavePath(defaultName: string, extensions?: string[]): Promise<string | null> {
  if (!isTauri()) return BROWSER_PATH;
  const { save } = await import('@tauri-apps/plugin-dialog');
  return save({
    defaultPath: defaultName,
    filters: extensions ? [{ name: defaultName, extensions }] : undefined
  });
}

/** Choose a file to read (Brewfile restore); return null on cancel. */
export async function pickOpenPath(): Promise<string | null> {
  if (!isTauri()) return BROWSER_PATH;
  const { open } = await import('@tauri-apps/plugin-dialog');
  const selected = await open({ multiple: false, directory: false });
  return typeof selected === 'string' ? selected : null;
}

/** Choose a folder (Clean Up Mac protected list); return null on cancel. */
export async function pickFolder(): Promise<string | null> {
  if (!isTauri()) return '~/Projects/archive';
  const { open } = await import('@tauri-apps/plugin-dialog');
  const selected = await open({ multiple: false, directory: true });
  return typeof selected === 'string' ? selected : null;
}
