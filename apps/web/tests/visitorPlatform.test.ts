import { describe, expect, it } from 'vitest';
import { chipFromArchitecture, chipFromRenderer, visitorSystem } from '../app/utils/visitorPlatform';

const UA = {
  macSafari:
    'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15',
  windows:
    'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36',
  linux: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36',
  chromeOs: 'Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36',
  iphone: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148',
  android: 'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/140.0.0.0 Mobile Safari/537.36'
};

describe('Visitor system', () => {
  it('Tells a Mac, Windows, Linux, iOS and Android apart', () => {
    expect(visitorSystem(UA.macSafari, undefined, 0)).toBe('mac');
    expect(visitorSystem(UA.windows, 'Windows', 0)).toBe('windows');
    expect(visitorSystem(UA.linux, undefined, 0)).toBe('linux');
    expect(visitorSystem(UA.chromeOs, undefined, 0)).toBe('linux');
    expect(visitorSystem(UA.iphone, undefined, 5)).toBe('ios');
    expect(visitorSystem(UA.android, 'Android', 5)).toBe('android');
  });

  it('Trusts the client hints’ platform name, and takes a touch "Macintosh" for an iPad', () => {
    expect(visitorSystem('', 'macOS', 0)).toBe('mac');
    expect(visitorSystem(UA.macSafari, undefined, 5)).toBe('ios');
    expect(visitorSystem('', undefined, 0)).toBe('other');
  });
});

describe('Mac chip', () => {
  it('Reads the chip from the graphics hardware that browsers report', () => {
    // Chrome and Firefox on Apple silicon name the chip.
    expect(chipFromRenderer('ANGLE (Apple, ANGLE Metal Renderer: Apple M2 Pro, Unspecified Version)')).toBe('arm64');
    expect(chipFromRenderer('ANGLE (Apple, Apple M1, OpenGL 4.1)')).toBe('arm64');
    // Intel Macs, with integrated or discrete graphics.
    expect(chipFromRenderer('ANGLE (Intel Inc., Intel(R) UHD Graphics 630, OpenGL 4.1)')).toBe('x86_64');
    expect(chipFromRenderer('ANGLE (AMD, AMD Radeon Pro 5500M OpenGL Engine, OpenGL 4.1)')).toBe('x86_64');
    expect(chipFromRenderer('NVIDIA GeForce GT 750M OpenGL Engine')).toBe('x86_64');
  });

  it('Does not take WebKit’s "Apple GPU" for Apple silicon: Safari says it on Intel Macs too', () => {
    expect(chipFromRenderer('Apple GPU')).toBeNull();
    expect(chipFromRenderer('Apple Software Renderer')).toBeNull();
    expect(chipFromRenderer('Apple')).toBeNull();
  });

  it('Says nothing when the browser hides the hardware', () => {
    expect(chipFromRenderer('Mozilla')).toBeNull();
    expect(chipFromRenderer('')).toBeNull();
    expect(chipFromRenderer(null)).toBeNull();
    expect(chipFromRenderer(undefined)).toBeNull();
  });

  it('Falls back to the client hints’ architecture', () => {
    expect(chipFromArchitecture('arm')).toBe('arm64');
    expect(chipFromArchitecture('x86')).toBe('x86_64');
    expect(chipFromArchitecture('wasm')).toBeNull();
    expect(chipFromArchitecture(undefined)).toBeNull();
  });
});
