import { isValidToken } from '@opennavo/shared';
import type { LocationQuery } from 'vue-router';
import type { LocalApp, StartupItem } from '@/ipc/bindings';

/**
 * Preview deep links (F1, F5): opennavo://startup?label=…, opennavo://uninstall/{token}?preview=1 and
 * opennavo://scan/{token}?preview=1 arrive from Rust as /security/startup?label=…&preview=1,
 * /uninstall/{token}?preview=1 and /security/scan?token=…&preview=1. They only find and preselect the item
 * or app; every action still goes through its page's own buttons and confirmations. The parameters are
 * checked again here with Rust's rules, so a link Rust did not build (another parameter, a repeated or
 * malformed value) shows a harmless "can't open this link" state instead of guessing.
 */
export type Preview = { state: 'none' } | { state: 'invalid' } | { state: 'ok'; value: string };

export const NO_PREVIEW: Preview = { state: 'none' };
const INVALID: Preview = { state: 'invalid' };

const encoder = new TextEncoder();

/** A label Rust lets through (deeplink.rs): 1–255 bytes, trimmed, no control characters, no / \ ? #. */
export function isPreviewLabel(label: string): boolean {
  const bytes = encoder.encode(label).length;
  return bytes > 0 && bytes <= 255 && label.trim() === label && !/[/\\?#]/.test(label) && !/\p{Cc}/u.test(label);
}

/** The query must hold exactly these keys, each once, with preview=1. */
function exact(query: LocationQuery, key: string): string | null {
  const keys = Object.keys(query);
  if (keys.length !== 2 || !keys.includes(key) || !keys.includes('preview')) return null;
  const value = query[key];
  return query.preview === '1' && typeof value === 'string' ? value : null;
}

/** /security/startup?label=…&preview=1: the label to look for among this Mac's startup items. */
export function startupPreview(query: LocationQuery): Preview {
  if (!Object.keys(query).length) return NO_PREVIEW;
  const label = exact(query, 'label');
  return label !== null && isPreviewLabel(label) ? { state: 'ok', value: label } : INVALID;
}

/** /security/scan?token=…&preview=1: the catalog app to offer a scan of. */
export function scanPreview(query: LocationQuery): Preview {
  if (!Object.keys(query).length) return NO_PREVIEW;
  const token = exact(query, 'token');
  return token !== null && isValidToken(token) ? { state: 'ok', value: token } : INVALID;
}

/** /uninstall/{token}?preview=1: the catalog app to show before the existing uninstall flow. */
export function uninstallPreview(token: unknown, query: LocationQuery): Preview {
  const keys = Object.keys(query);
  return typeof token === 'string' &&
    isValidToken(token) &&
    keys.length === 1 &&
    keys[0] === 'preview' &&
    query.preview === '1'
    ? { state: 'ok', value: token }
    : INVALID;
}

/** Startup items carrying exactly this label. A label proves nothing; the page shows signature and path. */
export function matchStartupItems(items: readonly StartupItem[], label: string): StartupItem[] {
  return items.filter(item => item.label === label);
}

/** Apps on this Mac installed by the cask, or matched to it by file name and bundle identity (Rust). */
export function matchLocalApps(apps: readonly LocalApp[], token: string): LocalApp[] {
  return apps.filter(app => app.catalogToken === token);
}
