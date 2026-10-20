import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { parseQuery } from 'vue-router';
import { canonicalQuery, queryNoindex } from '../shared/utils/routeQuery';
import { canonicalRequestUrl } from '../server/utils/canonicalUrl';

const cases = JSON.parse(
  readFileSync(new URL('../../server/testdata/seo/version-query.json', import.meta.url), 'utf8')
) as {
  query: string;
  notes: boolean;
  original: boolean;
  noindex: boolean;
  canonical: string;
}[];

describe('audit query fixtures match Vue Router and web SEO rules', () => {
  for (const fixture of cases)
    it(fixture.query, () => {
      const route = parseQuery(fixture.query);
      // The scalar comparisons used by PackageVersions; arrays never enable a filter.
      expect(route.notes === 'true' || route.notes === '1').toBe(fixture.notes);
      expect(route.original === 'true' || route.original === '1').toBe(fixture.original);
      const params = new URLSearchParams(fixture.query);
      expect(queryNoindex('/apps/app/versions', params)).toBe(fixture.noindex);
      expect(canonicalQuery('/apps/app/versions', params)).toBe(fixture.canonical);
      expect(canonicalRequestUrl('/apps/APP/versions?' + fixture.query)).toBe('/apps/app/versions?' + fixture.query);
    });
});

import { isValidToken } from '@opennavo/shared';

const paths = JSON.parse(
  readFileSync(new URL('../../server/testdata/seo/package-path.json', import.meta.url), 'utf8')
) as {
  path: string;
  valid: boolean;
  redirect: string | null;
}[];

describe('audit path fixtures match web normalization', () => {
  for (const fixture of paths)
    it(fixture.path, () => {
      const redirected = canonicalRequestUrl(fixture.path);
      expect(redirected ?? null).toBe(fixture.redirect);
      const routePath = redirected ?? fixture.path;
      const parts = routePath.split('/');
      const token = parts[parts.indexOf('apps') + 1]!;
      expect(isValidToken(decodeURIComponent(token))).toBe(fixture.valid);
    });
});
