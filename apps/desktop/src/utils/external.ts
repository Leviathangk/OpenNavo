import { isTauri } from '@tauri-apps/api/core';

/** Open external links in the system browser (opener permits http:, https: and x-apple.systempreferences:, 06 §14); browser mode opens a new tab. */
export async function openExternal(url: string): Promise<void> {
  if (isTauri()) {
    const { openUrl } = await import('@tauri-apps/plugin-opener');
    await openUrl(url);
    return;
  }
  window.open(url, '_blank', 'noopener,noreferrer');
}

const EXTERNAL_PROTOCOLS = new Set(['http:', 'https:']);

/** Shared UI anchors keep normal browser behavior; the desktop routes their clicks through its scoped opener. */
export function startExternalLinks(): () => void {
  if (!isTauri()) return () => {};
  const onClick = (event: MouseEvent) => {
    if (event.defaultPrevented || event.button !== (event.type === 'auxclick' ? 1 : 0)) return;
    const link = event.composedPath().find(node => node instanceof HTMLAnchorElement);
    if (!(link instanceof HTMLAnchorElement) || link.target.toLowerCase() !== '_blank') return;
    if (!EXTERNAL_PROTOCOLS.has(link.protocol)) return;
    event.preventDefault();
    void openExternal(link.href);
  };
  document.addEventListener('click', onClick);
  document.addEventListener('auxclick', onClick);
  return () => {
    document.removeEventListener('click', onClick);
    document.removeEventListener('auxclick', onClick);
  };
}
