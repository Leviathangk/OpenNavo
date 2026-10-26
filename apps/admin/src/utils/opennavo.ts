import dayjs from 'dayjs';
import type { Schemas } from '@/typings/api/opennavo';
import { $t } from '@/locales';

/** Remove null/undefined/empty query parameters: axios serializes null as empty, which backend typed parsing rejects. */
export function compactParams<T extends object>(params: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(params).filter(([, value]) => value !== null && value !== undefined && value !== '')
  ) as Partial<T>;
}

/** Display times consistently as YYYY-MM-DD HH:mm (07 §7). */
export function formatDateTime(value: string | number | Date | null | undefined): string {
  return value ? dayjs(value).format('YYYY-MM-DD HH:mm') : '—';
}

/** Options for Yes / No / Any tristate filters. */
export function booleanOptions(yes: string, no: string) {
  return [
    { label: yes, value: 'true' },
    { label: no, value: 'false' }
  ];
}

/** Tristate selection string (for NSelect) → query parameter. */
export function toBoolean(value: string | null | undefined): boolean | undefined {
  if (value === 'true') return true;
  if (value === 'false') return false;
  return undefined;
}

/** Publication-status colors for collections/features. */
export const PUBLISH_STATUS_TAG = {
  draft: 'default',
  scheduled: 'info',
  published: 'success',
  archived: 'warning'
} as const;

/**
 * Contracts include Hermes dryRun previews as possible write responses; admin pages never request previews.
 * Exclude the preview branch before reading result fields such as id/url; previews need not contain them.
 */
export function isDryRun(data: unknown): data is Schemas['AgentDryRun'] {
  return typeof data === 'object' && data !== null && (data as { dryRun?: unknown }).dryRun === true;
}

/**
 * A write that carried X-Expected-Revision and lost to a newer change, often an agent's (code 1003). The
 * admin API localizes the message, so only the code is reliable. Pages reload instead of retrying blindly.
 */
export function isRevisionConflict(response: { data?: unknown } | undefined): boolean {
  return String((response?.data as { code?: unknown } | undefined)?.code) === '1003';
}

/** A server write owner (`admin:<id>`, `agent:<clientId>:<tokenId>` or `system`) as editors read it. */
export function writeOwnerLabel(owner: string): string {
  const [kind, id] = owner.split(':');
  if (kind === 'admin' && id) return $t('page.shared.owner.admin', { id });
  if (kind === 'agent' && id) return $t('page.shared.owner.agent', { id });
  return owner === 'system' ? $t('page.shared.owner.system') : owner;
}
