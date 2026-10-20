import { mount } from '@vue/test-utils';
import { describe, expect, it } from 'vitest';
import OnAppIcon from '../src/components/OnAppIcon.vue';
import { imageSrcset } from '../src/utils/image';

const icon = (width: number) => ({ width, url: `https://cdn.opennavo.example/icons/app-${width}.webp` });

describe('imageSrcset', () => {
  it('Lists stored renditions by width and adds the fallback only next to them', () => {
    expect(imageSrcset([icon(256), icon(64), icon(128)])).toBe(
      'https://cdn.opennavo.example/icons/app-64.webp 64w, https://cdn.opennavo.example/icons/app-128.webp 128w, https://cdn.opennavo.example/icons/app-256.webp 256w'
    );
    expect(imageSrcset([icon(318)], { width: 640, url: 'https://cdn.opennavo.example/shot-640.png' })).toBe(
      'https://cdn.opennavo.example/icons/app-318.webp 318w, https://cdn.opennavo.example/shot-640.png 640w'
    );
    // A stored rendition wins over a fallback of the same width; legacy assets have no renditions.
    expect(imageSrcset([icon(640)], { width: 640, url: 'https://cdn.opennavo.example/shot-640.png' })).toBe(
      'https://cdn.opennavo.example/icons/app-640.webp 640w'
    );
    expect(imageSrcset([], { width: 640, url: 'https://cdn.opennavo.example/shot-640.png' })).toBeUndefined();
    expect(imageSrcset(undefined)).toBeUndefined();
  });

  it('Lets the icon pick a rendition for its rendered size', () => {
    const wrapper = mount(OnAppIcon, {
      props: {
        kind: 'cask',
        token: 'app',
        name: 'App',
        src: 'https://cdn.opennavo.example/icons/app.png',
        variants: [icon(64), icon(128)],
        size: 44
      }
    });
    const img = wrapper.find('img');
    expect(img.attributes('srcset')).toContain('app-128.webp 128w');
    expect(img.attributes('sizes')).toBe('44px');
    const legacy = mount(OnAppIcon, {
      props: { kind: 'cask', token: 'app', name: 'App', src: 'https://cdn.opennavo.example/icons/app.png' }
    });
    expect(legacy.find('img').attributes('srcset')).toBeUndefined();
    expect(legacy.find('img').attributes('sizes')).toBeUndefined();
  });
});
