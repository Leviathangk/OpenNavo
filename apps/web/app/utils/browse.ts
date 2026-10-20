// A–Z directory (T1, plan 4.2), pure for testing. GET /browse buckets the names visitors read (the API's
// store.BrowseBucket): numbers, a–z after folding accents, then names in other scripts. Every leaf page is
// linked from its root page, so any app the directory lists is three clicks from the home page.
import type { PublicComponents } from '@opennavo/api';

export type BrowseKind = 'apps' | 'fonts';
export type BrowseBucket = PublicComponents['schemas']['BrowseBucket'];
export type BrowseItem = PublicComponents['schemas']['BrowseItem'];

/** Names per leaf page: the API's page size (store.BrowsePageSize). */
export const BROWSE_PAGE_SIZE = 300;

/** Bucket keys in directory order. */
export const BROWSE_BUCKETS: readonly string[] = ['0-9', ...'abcdefghijklmnopqrstuvwxyz', 'other'];

export function isBrowseBucket(value: unknown): value is string {
  return typeof value === 'string' && BROWSE_BUCKETS.includes(value);
}

/** Directory path without a language prefix: /browse, /browse/fonts, /browse/s, /browse/fonts/s?page=2. */
export function browsePath(kind: BrowseKind, bucket?: string, page = 1): string {
  const root = kind === 'fonts' ? '/browse/fonts' : '/browse';
  if (!bucket) return root;
  return `${root}/${bucket}${page > 1 ? `?page=${page}` : ''}`;
}

/** Every page of a bucket, for the root page's direct links. */
export function bucketPages(bucket: Pick<BrowseBucket, 'pages'>): number[] {
  return Array.from({ length: Math.max(1, bucket.pages) }, (_, index) => index + 1);
}

/** Message key and parameters for a bucket's heading ("Mac apps starting with S"). */
export function bucketHeading(kind: BrowseKind, bucket: string): { key: string; params: Record<string, string> } {
  const group = kind === 'fonts' ? 'browse.fontBucket' : 'browse.bucket';
  if (bucket === '0-9') return { key: `${group}.digit`, params: {} };
  if (bucket === 'other') return { key: `${group}.other`, params: {} };
  return { key: `${group}.letter`, params: { letter: bucket.toUpperCase() } };
}

/** Short bucket label for links and breadcrumbs: S, 0–9 or the localized "Other". */
export function bucketLabel(bucket: string, t: (key: string) => string): string {
  if (bucket === '0-9') return t('browse.bucketLabel.digit');
  if (bucket === 'other') return t('browse.bucketLabel.other');
  return bucket.toUpperCase();
}

/** Names the directory lists in all: the root page states the whole count. */
export function browseTotal(buckets: readonly Pick<BrowseBucket, 'count'>[] | null | undefined): number {
  return (buckets ?? []).reduce((sum, bucket) => sum + bucket.count, 0);
}
