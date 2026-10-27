import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const websiteId = '12345678-1234-4234-8234-123456789abc';
const config = { scriptUrl: 'https://stats.example/script.js', websiteId };

describe('GEOInflow consent and navigation', () => {
  let analytics: typeof import('../app/utils/geoinflow');
  const scripts: HTMLScriptElement[] = [];
  const track = vi.fn().mockResolvedValue(undefined);
  const page = {
    origin: 'https://site.example',
    hostname: 'site.example',
    pathname: '/apps/example'
  };

  beforeEach(async () => {
    vi.resetModules();
    track.mockClear();
    scripts.length = 0;
    page.pathname = '/apps/example';
    vi.stubGlobal('location', page);
    vi.stubGlobal('window', { screen: { width: 1440, height: 900 } });
    vi.stubGlobal('document', {
      title: 'Example app',
      referrer: 'https://search.example/answer?token=private#fragment',
      documentElement: { lang: 'en' },
      createElement: () => Object.assign(new EventTarget(), { dataset: {}, remove: vi.fn() }),
      head: { append: (script: HTMLScriptElement) => scripts.push(script) }
    });
    analytics = await import('../app/utils/geoinflow');
  });
  afterEach(() => vi.unstubAllGlobals());

  function loaded() {
    window.umami = { track, getSession: () => ({ website: websiteId }) };
    scripts.at(-1)!.dispatchEvent(new Event('load'));
  }

  it('requires an explicit valid HTTPS deployment configuration', () => {
    for (const url of [
      '',
      'http://stats.example/script.js',
      'https://user:secret@stats.example/script.js',
      `${config.scriptUrl}?token=secret`
    ])
      expect(analytics.geoinflowConfig(url, websiteId)).toBeNull();
    expect(analytics.geoinflowConfig(config.scriptUrl, '')).toBeNull();
    expect(analytics.geoinflowConfig(config.scriptUrl, websiteId)).toEqual(config);
  });

  it('loads only after activation and sends deduplicated, sanitized page views and explicit events', () => {
    analytics.trackGeoinflow();
    expect(scripts).toHaveLength(0);
    analytics.startGeoinflow(config);
    analytics.startGeoinflow(config);
    expect(scripts).toHaveLength(1);
    expect(scripts[0].dataset).toMatchObject({
      autoTrack: 'false',
      beforeSend: 'opennavoGeoinflowBeforeSend'
    });
    analytics.trackGeoinflow();
    analytics.trackGeoinflow();
    page.pathname = '/download';
    analytics.trackGeoinflow();
    analytics.trackGeoinflow('download_client');
    expect(track).not.toHaveBeenCalled();
    loaded();
    expect(track).toHaveBeenCalledTimes(3);
    expect(track.mock.calls[0][0]).toMatchObject({
      url: 'https://site.example/apps/example',
      referrer: 'https://search.example/answer'
    });
    expect(track.mock.calls[1][0]).toMatchObject({
      url: 'https://site.example/download',
      referrer: 'https://site.example/apps/example'
    });
    expect(track.mock.calls[2][0]).toMatchObject({
      name: 'download_client',
      data: { content_language: 'en' }
    });
  });

  it('drops queued visits when consent is withdrawn during loading and supports re-consent', () => {
    analytics.startGeoinflow(config);
    analytics.trackGeoinflow();
    analytics.stopGeoinflow();
    loaded();
    analytics.trackGeoinflow('get_app');
    expect(track).not.toHaveBeenCalled();
    expect(window.opennavoGeoinflowBeforeSend!('event', { website: websiteId })).toBeNull();
    analytics.startGeoinflow(config);
    analytics.trackGeoinflow();
    expect(scripts).toHaveLength(1);
    expect(track).toHaveBeenCalledTimes(1);
    expect(
      window.opennavoGeoinflowBeforeSend!('event', {
        website: websiteId,
        url: 'https://site.example/?token=secret#fragment',
        referrer: 'https://search.example/?q=private'
      })
    ).toMatchObject({
      url: 'https://site.example/',
      referrer: 'https://search.example/'
    });
    expect(window.opennavoGeoinflowBeforeSend!('identify', { website: websiteId })).toBeNull();
  });

  it('can retry a failed script without replaying abandoned events', () => {
    analytics.startGeoinflow(config);
    analytics.trackGeoinflow();
    scripts[0].dispatchEvent(new Event('error'));
    analytics.startGeoinflow(config);
    analytics.trackGeoinflow();
    loaded();
    expect(track).toHaveBeenCalledTimes(1);
  });
});
