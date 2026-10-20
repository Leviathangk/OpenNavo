import type { BodyOf, DataOf, QueryOf, Schemas } from '@/typings/api/opennavo';
import { request } from '../request';

// Homebrew commit sources (read-only) and release entries (07 §7.3 version tab, §7.7).

export const fetchChangelogSources = (id: number) =>
  request<DataOf<'listChangelogSources'>>({ url: `/packages/${id}/changelog-sources` });

export const fetchReleaseList = (params: QueryOf<'listAdminReleases'>) =>
  request<DataOf<'listAdminReleases'>>({ url: '/releases', params });

export const fetchReleaseDetail = (id: number) => request<DataOf<'getAdminRelease'>>({ url: `/releases/${id}` });

export const updateRelease = (id: number, data: BodyOf<'updateRelease'>) =>
  request({ url: `/releases/${id}`, method: 'put', data });

export const updateReleaseI18n = (id: number, locale: Schemas['Locale'], data: BodyOf<'updateReleaseI18n'>) =>
  request({ url: `/releases/${id}/i18n/${locale}`, method: 'put', data });

// Release-list authoring: all cataloged versions including those without notes; write/clear editorial notes.

export const fetchAdminVersions = (params: QueryOf<'listAdminVersions'>) =>
  request<DataOf<'listAdminVersions'>>({ url: '/versions', params });

export const upsertReleaseNotes = (packageId: number, version: string, data: BodyOf<'upsertReleaseNotes'>) =>
  request<DataOf<'upsertReleaseNotes'>>({
    url: `/packages/${packageId}/versions/${encodeURIComponent(version)}/notes`,
    method: 'put',
    data
  });

export const fetchPackageChangelogSettings = (id: number) =>
  request<DataOf<'getPackageChangelogSettings'>>({ url: `/packages/${id}/changelog-settings` });
export const updatePackageChangelogSettings = (id: number, data: BodyOf<'updatePackageChangelogSettings'>) =>
  request<DataOf<'updatePackageChangelogSettings'>>({ url: `/packages/${id}/changelog-settings`, method: 'put', data });
