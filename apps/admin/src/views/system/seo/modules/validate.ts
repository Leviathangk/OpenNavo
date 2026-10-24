import { LOCKED_CRAWLERS } from '@opennavo/shared';

// Client-side copy of management.validateSEOSettings for inline errors; the server stays authoritative.
const BOT = /^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$/;
const LOCKED = new Set<string>([...LOCKED_CRAWLERS.map(bot => bot.toLowerCase()), '*']);
export const VERIFICATION = /^[A-Za-z0-9_-]{0,100}$/;
export const MEASUREMENT = /^(G-[A-Z0-9]{4,16})?$/;
export const ZONE = /^([a-f0-9]{32})?$/;
export const INDEXNOW_KEY = /^([A-Za-z0-9-]{8,128})?$/;
export const MAX_BOTS = 20;

const control = (value: string) => [...value].some(char => char.charCodeAt(0) < 32 || char.charCodeAt(0) === 127);

export function validLabel(value: string, limit: number) {
  return value.length > 0 && [...value].length <= limit && !control(value);
}

export function validHttps(value: string, limit: number) {
  if (!value || value.length > limit) return false;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' && Boolean(url.host) && !url.username && !url.password;
  } catch {
    return false;
  }
}

export function validRegions(regions: string[]) {
  return (
    regions.length <= 60 && new Set(regions).size === regions.length && regions.every(code => /^[A-Z]{2}$/.test(code))
  );
}

export function validSecret(value: string, limit: number) {
  return value.length <= limit && /^[\x21-\x7e]*$/u.test(value);
}

export type BotProblem = { key: 'invalidBot' | 'lockedBot' | 'overlapBot'; bot: string } | { key: 'tooMany' };

/** First problem in one crawler class; `other` is the other class, which must not share names. */
export function botProblem(bots: string[], other: string[]): BotProblem | null {
  if (bots.length > MAX_BOTS) return { key: 'tooMany' };
  const seen = new Set<string>();
  const others = new Set(other.map(bot => bot.toLowerCase()));
  for (const bot of bots) {
    const name = bot.toLowerCase();
    if (LOCKED.has(name)) return { key: 'lockedBot', bot };
    if (!BOT.test(bot) || seen.has(name)) return { key: 'invalidBot', bot };
    if (others.has(name)) return { key: 'overlapBot', bot };
    seen.add(name);
  }
  return null;
}

/** 32 hex characters, the format IndexNow suggests. */
export function randomIndexNowKey() {
  return [...crypto.getRandomValues(new Uint8Array(16))].map(byte => byte.toString(16).padStart(2, '0')).join('');
}

// Data provider credentials (/system/seo-providers/{key}). Shape checks only: the server validates
// again and stores the values encrypted; nothing here is ever echoed back.
const DOMAIN_PROPERTY = /^sc-domain:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/;
export const MAX_CREDENTIAL = 4096;
export const MAX_SERVICE_ACCOUNT = 16384;

/** A Search Console domain property (sc-domain:example.com) or an https URL-prefix property. */
export function validGscProperty(value: string) {
  return DOMAIN_PROPERTY.test(value) || validHttps(value, MAX_CREDENTIAL);
}

export type ServiceAccountProblem = 'invalidJson' | 'notServiceAccount' | 'missingFields' | 'tooLong';

/** Why a pasted Google service account key is unusable, or null when its shape is right. */
export function serviceAccountProblem(text: string): ServiceAccountProblem | null {
  if (text.length > MAX_SERVICE_ACCOUNT) return 'tooLong';
  let key: unknown;
  try {
    key = JSON.parse(text);
  } catch {
    return 'invalidJson';
  }
  if (!key || typeof key !== 'object' || Array.isArray(key)) return 'invalidJson';
  const record = key as Record<string, unknown>;
  if (record.type !== 'service_account') return 'notServiceAccount';
  const email = record.client_email;
  const privateKey = record.private_key;
  if (typeof email !== 'string' || !email.includes('@')) return 'missingFields';
  if (typeof privateKey !== 'string' || !privateKey.includes('PRIVATE KEY')) return 'missingFields';
  return null;
}

/** The audit origin is an https host with no path or query, and never the public site host. */
export function validOrigin(value: string, siteUrl: string) {
  if (!validHttps(value, MAX_CREDENTIAL)) return false;
  const url = new URL(value);
  if (url.pathname !== '/' || url.search || url.hash) return false;
  try {
    return url.host !== new URL(siteUrl).host;
  } catch {
    return true;
  }
}
