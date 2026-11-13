import type { PackageSummary } from '@opennavo/api';

export interface DeveloperTop {
  name: string;
  apps: number;
  installs30d: number;
}

export interface DeveloperMapPlace {
  key: string;
  kind: 'city' | 'country';
  name: string;
  countryCode: string;
  region: 'americas' | 'europe' | 'apac' | 'other';
  lat: number;
  lng: number;
  developers: number;
  apps: number;
  installs30d: number;
  topDevelopers: DeveloperTop[];
}

export interface DeveloperMapCountry {
  code: string;
  developers: number;
  apps: number;
  installs30d: number;
}

export interface DeveloperMapRecent {
  name: string;
  cityKey?: string;
  city: string;
  countryCode: string;
  apps: number;
  updatedAt: string;
}

export interface DeveloperMapTotals {
  developers: number;
  located: number;
  apps: number;
  locatedApps: number;
  places: number;
  countries: number;
}

export interface DeveloperMapData {
  generatedAt: string;
  totals: DeveloperMapTotals;
  places: DeveloperMapPlace[];
  countries: DeveloperMapCountry[];
  recentDevelopers: DeveloperMapRecent[];
}

// Intl.DisplayNames instances are expensive to build, so keep one per locale for the lifetime of the module.
const regionNames = new Map<string, Intl.DisplayNames>();

/** Country names always come from the reader's locale; they are never stored as interface copy. */
export function developerCountryName(countryCode: string, locale: string): string {
  try {
    let names = regionNames.get(locale);
    if (!names) regionNames.set(locale, (names = new Intl.DisplayNames([locale], { type: 'region' })));
    return names.of(countryCode) ?? countryCode;
  } catch {
    return countryCode;
  }
}

/** City labels are the submitted text kept in the database; only countries need a localized name. */
export function developerPlaceName(place: DeveloperMapPlace, locale: string): string {
  if (place.kind === 'country') return developerCountryName(place.countryCode, locale);
  return place.name;
}

export interface DeveloperPickView {
  package: PackageSummary;
  quote: string;
  publishedAt: string;
}

interface Envelope<T> {
  code: string;
  msg: string;
  data: T;
}

function developerApiBase(): string {
  const config = useRuntimeConfig();
  return import.meta.server ? config.apiBaseInternal || config.public.apiBase : config.public.apiBase;
}

export interface DeveloperFetchMetadata {
  source: 'github' | 'appstore' | 'website' | string;
  name: string;
  summary: string;
  description: string;
  developer: string;
  homepage: string;
  repoUrl: string;
  downloadUrl: string;
  iconUrl: string;
  version: string;
  minMacos: string;
  category: string;
  license: string;
  warnings: string[];
}

export interface DeveloperSubmitResult {
  id: number;
  reference: string;
  token: string;
  statusUrl: string;
}

export interface DeveloperSubmissionEvent {
  status: string;
  note?: string | null;
  actor: 'developer' | 'admin' | 'system';
  createdAt: string;
}

export interface DeveloperSubmissionStatus {
  reference: string;
  kind: 'new' | 'claim' | 'location';
  status: 'submitted' | 'in_review' | 'changes_requested' | 'approved' | 'rejected' | 'published';
  createdAt: string;
  updatedAt: string;
  decidedAt?: string | null;
  decisionNote?: string | null;
  package?: { kind: string; token: string; name: string } | null;
  events: DeveloperSubmissionEvent[];
}

// Same capability header the rest of the web reads send: link apps stay visible.
const DEVELOPER_HEADERS = { 'X-Client-Platform': 'web', 'X-OpenNavo-Caps': 'link' };

async function developerPost<T>(path: string, body: Record<string, unknown>): Promise<T> {
  const response = await $fetch<Envelope<T>>(`${developerApiBase()}${path}`, {
    method: 'POST',
    body,
    headers: DEVELOPER_HEADERS
  });
  if (response.code !== '0000') throw new Error(response.msg || `developer API ${path} failed`);
  return response.data;
}

export async function fetchDeveloperMetadata(url: string): Promise<DeveloperFetchMetadata> {
  return developerPost<DeveloperFetchMetadata>('/developer/fetch', { url });
}

export async function submitDeveloperSubmission(body: Record<string, unknown>): Promise<DeveloperSubmitResult> {
  return developerPost<DeveloperSubmitResult>('/developer/submissions', body);
}

/** Anonymous receipt lookup; 404 means the token is unknown or mistyped. */
export async function fetchDeveloperSubmissionStatus(token: string): Promise<DeveloperSubmissionStatus | null> {
  try {
    return await developerGet<DeveloperSubmissionStatus>(`/developer/submissions/${encodeURIComponent(token)}`, {});
  } catch (error) {
    const status =
      (error as { status?: number; statusCode?: number }).status ?? (error as { statusCode?: number }).statusCode;
    if (status === 404) return null;
    throw error;
  }
}

async function developerGet<T>(path: string, query: Record<string, string | number>): Promise<T> {
  const response = await $fetch<Envelope<T>>(`${developerApiBase()}${path}`, { query, headers: DEVELOPER_HEADERS });
  if (response.code !== '0000') throw new Error(response.msg || `developer API ${path} failed`);
  return response.data;
}

/** Map data is shared by the page and the rail; useAsyncData dedupes both calls per request. */
export function useDeveloperMap() {
  const { locale } = useI18n();
  return useAsyncData(
    () => `developer-map:${locale.value}`,
    () => developerGet<DeveloperMapData>('/developer-map', { locale: toAppLocale(locale.value) })
  );
}

/** Picks are supplementary: a failure hides the section instead of breaking Discover. */
export function useDeveloperPicks(limit = 3) {
  const { locale } = useI18n();
  return useAsyncData(
    () => `developer-picks:${locale.value}:${limit}`,
    async () => {
      try {
        return await developerGet<DeveloperPickView[]>('/developer-picks', {
          locale: toAppLocale(locale.value),
          limit
        });
      } catch {
        return [];
      }
    },
    { default: () => [] as DeveloperPickView[] }
  );
}
