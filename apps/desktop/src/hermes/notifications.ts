import { i18n } from '@/i18n';
import { commands } from '@/ipc/client';
import { en, zhCN } from '@/hermes/content';

import type { HermesNotificationKind } from '@/stores/hermesNotifications';

export const HERMES_NOTIFICATION_BODY_MAX = 140;
export const HERMES_NOTIFICATION_THROTTLE_MS = 1000;

const lastFiredAt = new Map<string, number>();

function throttled(kind: HermesNotificationKind, conversationId: string, now: number): boolean {
  for (const [key, at] of lastFiredAt) {
    if (now - at >= HERMES_NOTIFICATION_THROTTLE_MS) lastFiredAt.delete(key);
  }
  const key = `${kind}:${conversationId}`;
  if (lastFiredAt.has(key)) return true;
  lastFiredAt.set(key, now);
  return false;
}

export function clipHermesNotificationBody(value: string | null | undefined, fallback: string): string {
  const text = (value ?? '').trim() || fallback.trim();
  // Code points, matching Rust `chars()`. UTF-16 `slice` can cut an emoji into a lone
  // surrogate, which serde_json rejects and drops the whole notification.
  const chars = Array.from(text);
  if (chars.length <= HERMES_NOTIFICATION_BODY_MAX) return text;
  return chars.slice(0, HERMES_NOTIFICATION_BODY_MAX).join('');
}

/** Same background test as the official desktop client: hidden, or visible but not focused. */
export function hermesWindowIsBackgrounded(): boolean {
  if (typeof document === 'undefined') return false;
  if (document.hidden) return true;
  return typeof document.hasFocus === 'function' && !document.hasFocus();
}

function notificationCopy() {
  const locale = String(i18n.global.locale.value);
  return locale.startsWith('zh') ? zhCN.settings.notifications : en.settings.notifications;
}

export async function notifyHermesReply(
  kind: HermesNotificationKind,
  conversationId: string,
  text: string | null | undefined
): Promise<boolean> {
  const id = conversationId.trim();
  if (!id) return false;
  if (!hermesWindowIsBackgrounded()) return false;
  if (typeof window === 'undefined' || !('__TAURI_INTERNALS__' in window)) return false;
  if (throttled(kind, id, Date.now())) return false;

  const copy = notificationCopy();
  const title = kind === 'turnDone' ? copy.turnDoneTitle : copy.turnErrorTitle;
  const fallback = kind === 'turnDone' ? copy.turnDoneBody : copy.turnErrorBody;
  const body = clipHermesNotificationBody(text, fallback);

  try {
    const result = await commands.hermesNotifyReply(id, title, body);
    return result.status === 'ok';
  } catch {
    return false;
  }
}
