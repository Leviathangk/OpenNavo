import { describe, expect, it } from 'vitest';
import { canonicalPath } from '../app/utils/canonicalPath';

describe('canonical paths', () => {
  it('keep the literal + and @ of app tokens, as sitemaps and hreflang write them', () => {
    expect(canonicalPath('/apps/plus-app%2B%2B/versions')).toBe('/apps/plus-app++/versions');
    expect(canonicalPath('/ja/apps/plus-app++')).toBe('/ja/apps/plus-app++');
    expect(canonicalPath('/apps/versioned-app%4011')).toBe('/apps/versioned-app@11');
  });

  it('keep other characters escaped, and a slash or ? inside a segment too', () => {
    expect(canonicalPath('/search/a%20b')).toBe('/search/a%20b');
    expect(canonicalPath('/background-items/com.example%2Fagent')).toBe('/background-items/com.example%2Fagent');
    expect(canonicalPath('/x/a%3Fb')).toBe('/x/a%3Fb');
    // Malformed escapes are left as they are.
    expect(canonicalPath('/apps/bad%E0%A4%A')).toBe('/apps/bad%E0%A4%A');
  });
});
