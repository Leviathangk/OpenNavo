import { describe, expect, it } from 'vitest';
import { OG_ICON_PATH, isOgIconKey, ogIconSrc } from '../shared/utils/ogIcon';

const CDN = 'https://cdn.opennavo.com';
const INTERNAL = 'http://minio:9000/opennavo';
const ICON = `${CDN}/icons/uploads/0381e918d648/6df636fed7ce-256.png`;

describe('Share-image icons', () => {
  it('Reads CDN icons through the same-origin path once the internal base is set', () => {
    expect(ogIconSrc(ICON, CDN, INTERNAL)).toBe(`${OG_ICON_PATH}icons/uploads/0381e918d648/6df636fed7ce-256.png`);
    expect(ogIconSrc(ICON, `${CDN}/`, INTERNAL)).toBe(`${OG_ICON_PATH}icons/uploads/0381e918d648/6df636fed7ce-256.png`);
  });

  it('Keeps every other address as it is', () => {
    // Not configured (development, CI) or in the browser, where private runtime config is absent.
    expect(ogIconSrc(ICON, CDN, '')).toBe(ICON);
    expect(ogIconSrc(ICON, CDN, undefined)).toBe(ICON);
    // Built-in site images, other hosts, other bucket content and query strings.
    expect(ogIconSrc('/icon-512.png', CDN, INTERNAL)).toBe('/icon-512.png');
    expect(ogIconSrc('https://example.com/icons/a.png', CDN, INTERNAL)).toBe('https://example.com/icons/a.png');
    expect(ogIconSrc(`${CDN}/screenshots/a.png`, CDN, INTERNAL)).toBe(`${CDN}/screenshots/a.png`);
    expect(ogIconSrc(`${ICON}?v=2`, CDN, INTERNAL)).toBe(`${ICON}?v=2`);
    expect(ogIconSrc(null, CDN, INTERNAL)).toBeNull();
    expect(ogIconSrc(undefined, CDN, INTERNAL)).toBeUndefined();
  });

  it('Serves only icon objects', () => {
    for (const key of [
      'icons/a.png',
      'icons/uploads/0381e918d648/6df636fed7ce-256.png',
      'icons/a.b.JPG',
      'icons/x/y.svg'
    ])
      expect(isOgIconKey(key), key).toBe(true);
    for (const key of [
      '',
      'icons/',
      'icons/a.gif',
      'screenshots/a.png',
      'snapshots/latest.json',
      'icons/../snapshots/a.png',
      'icons/./a.png',
      'icons//a.png',
      '/icons/a.png',
      'icons/a.png?x=1',
      'icons/%2e%2e/a.png'
    ])
      expect(isOgIconKey(key), key).toBe(false);
  });
});
