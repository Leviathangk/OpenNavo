import { describe, expect, it } from 'vitest';
import {
  BROWSE_BUCKETS,
  browsePath,
  browseTotal,
  bucketHeading,
  bucketLabel,
  bucketPages,
  isBrowseBucket
} from '../app/utils/browse';
import { routeQueryRule } from '../shared/utils/routeQuery';

describe('A–Z directory (T1)', () => {
  it('Knows the API buckets in directory order and nothing else', () => {
    expect(BROWSE_BUCKETS).toHaveLength(28);
    expect(BROWSE_BUCKETS[0]).toBe('0-9');
    expect(BROWSE_BUCKETS.at(-1)).toBe('other');
    for (const value of ['a', 'z', '0-9', 'other']) expect(isBrowseBucket(value)).toBe(true);
    for (const value of ['A', 'aa', '0', 'fonts', '', undefined, ['a']]) expect(isBrowseBucket(value)).toBe(false);
  });

  it('Writes leaf paths with ?page= only from page 2 on', () => {
    expect(browsePath('apps')).toBe('/browse');
    expect(browsePath('fonts')).toBe('/browse/fonts');
    expect(browsePath('apps', 's')).toBe('/browse/s');
    expect(browsePath('apps', 's', 1)).toBe('/browse/s');
    expect(browsePath('fonts', '0-9', 3)).toBe('/browse/fonts/0-9?page=3');
  });

  it('Links every page of a bucket and counts the whole directory', () => {
    expect(bucketPages({ pages: 1 })).toEqual([1]);
    expect(bucketPages({ pages: 3 })).toEqual([1, 2, 3]);
    expect(browseTotal([{ count: 300 }, { count: 12 }])).toBe(312);
    expect(browseTotal(undefined)).toBe(0);
  });

  it('Names buckets for headings and links', () => {
    expect(bucketHeading('apps', 's')).toEqual({ key: 'browse.bucket.letter', params: { letter: 'S' } });
    expect(bucketHeading('fonts', '0-9')).toEqual({ key: 'browse.fontBucket.digit', params: {} });
    expect(bucketHeading('apps', 'other')).toEqual({ key: 'browse.bucket.other', params: {} });
    const t = (key: string) => `<${key}>`;
    expect(bucketLabel('s', t)).toBe('S');
    expect(bucketLabel('0-9', t)).toBe('<browse.bucketLabel.digit>');
    expect(bucketLabel('other', t)).toBe('<browse.bucketLabel.other>');
  });

  it('App leaf pages keep ?page= in their canonical through the route query contract', () => {
    // /browse/fonts/:bucket still needs its own contract row (reported to Claude); app leaves have one.
    expect(routeQueryRule('/browse/s').rule.canonical).toEqual(['page']);
    expect(routeQueryRule('/zh/browse/0-9').rule.canonical).toEqual(['page']);
  });
});
