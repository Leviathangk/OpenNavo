import { AUTHORING_LOCALE, DESKTOP_LOCALES, LOCALE_METADATA, SITE_LOCALES } from '@opennavo/shared/locales';
import type { Schemas } from '@/typings/api/opennavo';

/** Content locales (12 §1, L1): every site language from packages/shared/src/locales.json. */
export type ContentLocale = Schemas['Locale'];

/** Mac client languages (client release notes); a subset of ContentLocale. */
export type AppContentLocale = Schemas['AppLocale'];

/** Writes require only source-language text (04 §9.1); new content defaults to English, with other languages translated by AI. */
export const DEFAULT_SOURCE_LOCALE: ContentLocale = AUTHORING_LOCALE;

/** Admin locale order: default source language first. */
export const CONTENT_LOCALES: readonly ContentLocale[] = [
  AUTHORING_LOCALE,
  ...SITE_LOCALES.filter(code => code !== AUTHORING_LOCALE)
];

/** Client release notes cover every language the Mac client ships in (AGENTS.md). */
export const APP_CONTENT_LOCALES: readonly AppContentLocale[] = [
  AUTHORING_LOCALE,
  ...DESKTOP_LOCALES.filter(code => code !== AUTHORING_LOCALE)
];

/** Use each language's native name (e.g. English, Japanese), independent of admin UI language. */
export function localeName(code: ContentLocale): string {
  return code === 'zh-Hant'
    ? `${LOCALE_METADATA[code].name} ← ${LOCALE_METADATA['zh-CN'].name}`
    : LOCALE_METADATA[code].name;
}

export const SOURCE_LOCALE_OPTIONS = CONTENT_LOCALES.filter(code => code !== 'zh-Hant').map(code => ({
  label: localeName(code),
  value: code
}));
export const APP_SOURCE_LOCALE_OPTIONS = APP_CONTENT_LOCALES.filter(code => code !== 'zh-Hant').map(code => ({
  label: localeName(code),
  value: code
}));

export const CONTENT_LOCALE_OPTIONS = CONTENT_LOCALES.map(code => ({ label: localeName(code), value: code }));

export const APP_CONTENT_LOCALE_OPTIONS = APP_CONTENT_LOCALES.map(code => ({ label: localeName(code), value: code }));

type LocalizedMap<T> = Partial<Record<ContentLocale, T | null | undefined>> | null | undefined;

/** First nonempty locale in caller order (UI/source, etc.); otherwise English, then any available locale. */
export function pickLocalized<T>(map: LocalizedMap<T>, ...prefer: (ContentLocale | null | undefined)[]): T | undefined {
  if (!map) return undefined;
  for (const code of [...prefer, 'en-US' as const, ...CONTENT_LOCALES]) {
    const value = code ? map[code] : undefined;
    if (value !== null && value !== undefined && value !== '') return value;
  }
  return undefined;
}

/** Translation status colors (12: no review; source / machine / manually corrected / queued / failed only). */
export const TRANSLATION_STATUS_TAG = {
  none: 'default',
  source: 'success',
  machine: 'info',
  manual: 'primary',
  pending: 'warning',
  failed: 'error',
  skipped: 'default'
} as const;

/** Editable multilingual text: locale → field → text; empty values become null. */
export type LocalizedTexts = Partial<Record<ContentLocale, Record<string, string | null>>>;

/** Loaded translation status for each locale. */
export type LocalizedStatuses = Partial<Record<ContentLocale, Schemas['TranslationStatus']>>;

type ViewEntry = { status?: Schemas['TranslationStatus'] } & object;

/** Convert read mappings into editable text/status, copying only listed keys and excluding server metadata from writes. */
export function fromView(
  view: Partial<Record<ContentLocale, ViewEntry | null | undefined>> | null | undefined,
  keys: readonly string[]
): { texts: LocalizedTexts; statuses: LocalizedStatuses } {
  const texts: LocalizedTexts = {};
  const statuses: LocalizedStatuses = {};
  for (const code of CONTENT_LOCALES) {
    const entry = view?.[code];
    if (!entry) continue;
    const record = entry as Record<string, unknown>;
    texts[code] = Object.fromEntries(
      keys.map(key => [key, typeof record[key] === 'string' ? (record[key] as string) : null])
    );
    if (entry.status) statuses[code] = entry.status;
  }
  return { texts, statuses };
}

/** New content: one set of empty fields in the source language. */
export function blankTexts(keys: readonly string[], locale: ContentLocale = DEFAULT_SOURCE_LOCALE): LocalizedTexts {
  return { [locale]: Object.fromEntries(keys.map(key => [key, null])) };
}

function normalize(entry: Record<string, string | null> | undefined, keys: readonly string[]) {
  return Object.fromEntries(keys.map(key => [key, entry?.[key]?.trim() || null])) as Record<string, string | null>;
}

const isEmpty = (entry: Record<string, string | null>) => Object.values(entry).every(value => value === null);
const sameEntry = (a: Record<string, string | null>, b: Record<string, string | null>) =>
  Object.keys(a).every(key => a[key] === b[key]);

interface LocalizedBodyOptions<T> {
  texts: LocalizedTexts;
  original: LocalizedTexts;
  sourceLocale: ContentLocale;
  keys: readonly string[];
  /** Trimmed fields (empty → null) into one contract text group. */
  entry: (fields: Record<string, string | null>) => T;
}

/**
 * Build write mappings (04 §9.1): always send source language; send other languages only when changed (manual correction).
 * Omit unchanged translations so the server preserves them. With clearable collections/features, send null for entirely cleared language groups.
 * Other entity types cannot clear a single language; treat an entirely cleared group as unchanged.
 */
export function localizedBody<T>(
  options: LocalizedBodyOptions<T> & { clearable: true }
): Partial<Record<ContentLocale, T | null>>;
export function localizedBody<T>(
  options: LocalizedBodyOptions<T> & { clearable?: false }
): Partial<Record<ContentLocale, T>>;
export function localizedBody<T>(
  options: LocalizedBodyOptions<T> & { clearable?: boolean }
): Partial<Record<ContentLocale, T | null>> {
  const { texts, original, sourceLocale, keys, entry, clearable = false } = options;
  const body: Partial<Record<ContentLocale, T | null>> = {};
  for (const code of CONTENT_LOCALES) {
    if (code === 'zh-Hant') continue;
    const current = normalize(texts[code], keys);
    if (code === sourceLocale) {
      body[code] = entry(current);
      continue;
    }
    const before = normalize(original[code], keys);
    if (sameEntry(current, before)) continue;
    if (isEmpty(current)) {
      if (clearable && !isEmpty(before)) body[code] = null;
      continue;
    }
    body[code] = entry(current);
  }
  return body;
}
