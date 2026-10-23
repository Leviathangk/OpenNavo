import { IncomingMessage, ServerResponse } from 'node:http';
import { Socket } from 'node:net';
import { createEvent } from 'h3';
import { afterEach, describe, expect, it } from 'vitest';
import { LOAD_SHED_DEFAULTS, LoadShedState, shedRequest } from '../server/utils/loadShed';

const states: LoadShedState[] = [];
const sockets: Socket[] = [];
function event(path: string, ua = 'PerplexityBot') {
  const socket = new Socket();
  sockets.push(socket);
  const req = new IncomingMessage(socket);
  req.url = path;
  req.headers = { 'user-agent': ua };
  return createEvent(req, new ServerResponse(req));
}
function state() {
  const value = new LoadShedState();
  states.push(value);
  return value;
}
afterEach(() => {
  states.splice(0).forEach(s => s.stop());
  sockets.splice(0).forEach(s => s.destroy());
});

describe('image admission', () => {
  it('bounds concurrent image requests without consuming all page capacity, and releases slots once', async () => {
    const s = state();
    const a = event('/_og/a.png');
    const b = event('/_og/b.png');
    expect(shedRequest(a, LOAD_SHED_DEFAULTS, s)).toBeUndefined();
    expect(shedRequest(b, LOAD_SHED_DEFAULTS, s)).toBeUndefined();
    const rejected = event('/_og/c.png');
    await shedRequest(rejected, LOAD_SHED_DEFAULTS, s);
    expect(rejected.node.res.statusCode).toBe(503);
    expect(rejected.node.res.getHeader('cache-control')).toBe('no-store');
    expect(s.render()).toContain('level="image_capacity"} 1');
    const page = event('/apps/vlc', 'Googlebot');
    expect(shedRequest(page, LOAD_SHED_DEFAULTS, s)).toBeUndefined();
    expect(s.inFlight).toBe(3);
    expect(s.imageInFlight).toBe(2);
    a.context.releaseLoadShed();
    a.node.res.emit('close');
    expect(s.imageInFlight).toBe(1);
    expect(shedRequest(event('/_og/d.png'), LOAD_SHED_DEFAULTS, s)).toBeUndefined();
    expect(s.imageInFlight).toBe(2);
  });

  it('allows only an active admitted image’s inherited internal island without releasing its parent', async () => {
    const s = state();
    const parent = event('/_og/a.png');
    shedRequest(parent, LOAD_SHED_DEFAULTS, s);
    s.lagMs = 2000;
    const child = event('/__nuxt_island/OgPackage_hash.json');
    // eslint-disable-next-line no-underscore-dangle -- Match Nitro’s context forwarding API.
    child.context = { ...parent.context._platform }; // Nitro forwards the platform context only.
    expect(shedRequest(child, LOAD_SHED_DEFAULTS, s)).toBeUndefined();
    expect(child.context.releaseLoadShed).toBeUndefined();
    expect(s.inFlight).toBe(1);
    expect(s.imageInFlight).toBe(1);
    parent.context.releaseLoadShed();
    expect(s.inFlight).toBe(0);
    const expired = event('/__nuxt_island/OgPackage_hash.json');
    // eslint-disable-next-line no-underscore-dangle -- Match Nitro’s context forwarding API.
    expired.context = { ...parent.context._platform };
    await shedRequest(expired, LOAD_SHED_DEFAULTS, s);
    expect(expired.node.res.statusCode).toBe(503);
  });

  it('does not trust direct island requests, headers, forged objects or a copied token on another path', async () => {
    const s = state();
    const parent = event('/_og/a.png');
    shedRequest(parent, LOAD_SHED_DEFAULTS, s);
    s.lagMs = 2000;
    for (const [path, admission] of [
      ['/__nuxt_island/OgPackage_hash.json', undefined],
      ['/__nuxt_island/OgPackage_hash.json', { request: {} }],
      ['/apps/vlc', parent.context.ogAdmission]
    ] as const) {
      const request = event(path);
      request.node.req.headers['x-nuxt-og-image'] = 'true';
      request.node.req.headers['x-opennavo-og-admission'] = 'true';
      request.context.ogAdmission = admission;
      await shedRequest(request, LOAD_SHED_DEFAULTS, s);
      expect(request.node.res.statusCode).toBe(503);
    }
    expect(s.inFlight).toBe(1);
  });
});
