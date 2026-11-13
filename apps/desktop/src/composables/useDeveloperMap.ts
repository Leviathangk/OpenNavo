import type { PackageSummary } from '@opennavo/api';
import { api } from '@/api';

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

export interface DeveloperPickView {
  package: PackageSummary;
  quote: string;
  publishedAt: string;
}

export interface DeveloperFetchMetadata {
  source: string;
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

interface Envelope<T> {
  code: string;
  msg: string;
  data: T;
}

async function getDeveloper<T>(path: string, locale: string, query: Record<string, string | number> = {}): Promise<T> {
  const response = await (
    api as unknown as {
      GET: (
        path: string,
        options: { params: { query: Record<string, string | number> }; headers: Record<string, string> }
      ) => Promise<{ data?: Envelope<T> }>;
    }
  ).GET(path, {
    params: { query: { locale, ...query } },
    headers: { 'Accept-Language': locale }
  });
  const envelope = response.data;
  if (!envelope || envelope.code !== '0000') throw new Error(envelope?.msg || `developer API ${path} failed`);
  return envelope.data;
}

export function fetchDeveloperMap(locale: string): Promise<DeveloperMapData> {
  return getDeveloper<DeveloperMapData>('/developer-map', locale);
}

export function fetchDeveloperPicks(locale: string, limit = 3): Promise<DeveloperPickView[]> {
  return getDeveloper<DeveloperPickView[]>('/developer-picks', locale, { limit });
}

type PostClient = {
  POST: (path: string, options: { body: Record<string, unknown> }) => Promise<{ data?: Envelope<unknown> }>;
};

async function postDeveloper<T>(path: string, body: Record<string, unknown>): Promise<T> {
  const response = await (api as unknown as PostClient).POST(path, { body });
  const envelope = response.data as Envelope<T> | undefined;
  if (!envelope || envelope.code !== '0000') throw new Error(envelope?.msg || `developer API ${path} failed`);
  return envelope.data;
}

export function fetchDeveloperMetadata(url: string): Promise<DeveloperFetchMetadata> {
  return postDeveloper<DeveloperFetchMetadata>('/developer/fetch', { url });
}

export function submitDeveloperSubmission(body: Record<string, unknown>): Promise<DeveloperSubmitResult> {
  return postDeveloper<DeveloperSubmitResult>('/developer/submissions', body);
}

/** Anonymous receipt lookup; null means the token is unknown or mistyped. */
export async function fetchDeveloperSubmissionStatus(token: string): Promise<DeveloperSubmissionStatus | null> {
  const response = await (
    api as unknown as {
      GET: (
        path: string,
        options: { params: { path: { token: string } } }
      ) => Promise<{ data?: Envelope<DeveloperSubmissionStatus>; response: Response }>;
    }
  ).GET('/developer/submissions/{token}', { params: { path: { token } } });
  if (response.response.status === 404) return null;
  const envelope = response.data;
  if (!envelope || envelope.code !== '0000') throw new Error(envelope?.msg || 'developer status failed');
  return envelope.data;
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
