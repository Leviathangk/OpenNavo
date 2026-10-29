export { createPublicClient, unwrap } from './client';
export type { PublicClient, PublicClientOptions } from './client';
export { ApiError, NETWORK_ERROR, isApiError } from './errors';
export type { ApiErrorInit } from './errors';
export type { components as PublicComponents, operations as PublicOperations, paths as PublicPaths } from './public';
export type { components as AdminComponents, operations as AdminOperations, paths as AdminPaths } from './admin';

import type { components } from './public';

type Schemas = components['schemas'];

// Common public API models for component props and pages.
export type PackageKind = Schemas['PackageKind'];
export type PackageSummary = Schemas['PackageSummary'];
export type PackageDetail = Schemas['PackageDetail'];
/** Optional repository signals added by the public package detail contract. */
export interface PackageRepository {
  url?: string;
  stars?: number | null;
  pushedAt?: string | null;
  archived?: boolean;
  signalsCheckedAt?: string | null;
}
export type PackageDetailWithRepository = PackageDetail & { repository?: PackageRepository };
export type CaskPlatform = Schemas['CaskPlatform'];
export type CaskArtifact = Schemas['CaskArtifact'];
export type Artifacts = Schemas['Artifacts'];
export type InstallStats = Schemas['InstallStats'];
export type Screenshot = Schemas['Screenshot'];
export type Dependencies = Schemas['Dependencies'];
export type DependencyNode = Schemas['DependencyNode'];
export type ReleaseEntry = Schemas['ReleaseEntry'];
export type ReleaseSection = Schemas['ReleaseSection'];
export type ReleasePage = Schemas['ReleasePage'];
export type CategoryRef = Schemas['CategoryRef'];
export type CategoryNode = Schemas['CategoryNode'];
export type CollectionSummary = Schemas['CollectionSummary'];
export type CollectionDetail = Schemas['CollectionDetail'];
export type Feature = Schemas['Feature'];
export type HomeData = Schemas['HomeData'];
export type SearchHit = Schemas['SearchHit'];
export type SearchResult = Schemas['SearchResult'];
export type SuggestItem = Schemas['SuggestItem'];
export type RankingEntry = Schemas['RankingEntry'];
export type CatalogItem = Schemas['CatalogItem'];
export type ClientConfig = Schemas['ClientConfig'];
export type DesktopReleaseInfo = Schemas['DesktopReleaseInfo'];
export type SiteSettings = Schemas['SiteSettings'];
export { createUsageTracker } from './usage';
export type { UsageActivity } from './usage';
